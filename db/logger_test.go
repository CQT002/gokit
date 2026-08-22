package db

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

// newCaptureLogger trả về logger ghi JSON vào buffer, mở tới mức Debug.
func newCaptureLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), &buf
}

// decodeLine đọc dòng log JSON đầu tiên trong buffer.
func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("không có dòng log nào")
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("dòng log không phải JSON: %v — %s", err, line)
	}
	return m
}

// tracer là fc mà GORM truyền cho Trace.
func tracer(sql string, rows int64) func() (string, int64) {
	return func() (string, int64) { return sql, rows }
}

func TestGormLogger_Loi(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, time.Second, logger.Warn, false)

	g.Trace(context.Background(), time.Now(), tracer("SELECT 1", 0), errors.New("boom"))

	line := decodeLine(t, buf)
	if line["level"] != "ERROR" {
		t.Errorf("level = %v, muốn ERROR", line["level"])
	}
	if line["msg"] != "sql error" {
		t.Errorf("msg = %v, muốn %q", line["msg"], "sql error")
	}
	if line["sql"] != "SELECT 1" {
		t.Errorf("sql = %v", line["sql"])
	}
	if line["error"] != "boom" {
		t.Errorf("error = %v", line["error"])
	}
	if _, ok := line["elapsed_ms"]; !ok {
		t.Error("thiếu attr elapsed_ms")
	}
}

// ErrRecordNotFound là luồng bình thường của First/Take. Log nó ở mức Error làm
// mọi alert theo error rate thành vô dụng.
func TestGormLogger_KhongLogRecordNotFound(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, time.Second, logger.Info, false)

	g.Trace(context.Background(), time.Now(), tracer("SELECT 1", 0), logger.ErrRecordNotFound)

	if got := buf.String(); !strings.Contains(got, `"level":"DEBUG"`) {
		t.Errorf("muốn dòng DEBUG bình thường, nhận: %s", got)
	}
	if strings.Contains(buf.String(), "ERROR") {
		t.Errorf("record not found bị log ở mức ERROR: %s", buf.String())
	}
}

func TestGormLogger_QueryCham(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, 10*time.Millisecond, logger.Warn, false)

	g.Trace(context.Background(), time.Now().Add(-50*time.Millisecond), tracer("SELECT 1", 3), nil)

	line := decodeLine(t, buf)
	if line["level"] != "WARN" {
		t.Errorf("level = %v, muốn WARN", line["level"])
	}
	if line["msg"] != "slow sql" {
		t.Errorf("msg = %v", line["msg"])
	}
	if line["slow"] != true {
		t.Errorf("thiếu attr slow=true: %v", line)
	}
	if line["rows"] != float64(3) {
		t.Errorf("rows = %v, muốn 3", line["rows"])
	}
}

// Mức Warn là mặc định của production: chỉ lỗi và query chậm, không log mọi câu
// query.
func TestGormLogger_MucWarnKhongLogQueryNhanh(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, time.Second, logger.Warn, false)

	g.Trace(context.Background(), time.Now(), tracer("SELECT 1", 1), nil)

	if buf.Len() != 0 {
		t.Errorf("mức Warn vẫn log query nhanh: %s", buf.String())
	}
}

func TestGormLogger_MucInfoLogMoiQuery(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, time.Second, logger.Info, false)

	g.Trace(context.Background(), time.Now(), tracer("SELECT 1", 1), nil)

	line := decodeLine(t, buf)
	if line["level"] != "DEBUG" {
		t.Errorf("level = %v, muốn DEBUG", line["level"])
	}
	if line["msg"] != "sql" {
		t.Errorf("msg = %v", line["msg"])
	}
}

func TestGormLogger_Silent(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, time.Millisecond, logger.Silent, false)

	g.Trace(context.Background(), time.Now().Add(-time.Second), tracer("SELECT 1", 1), errors.New("boom"))
	g.Info(context.Background(), "hello")
	g.Warn(context.Background(), "hello")
	g.Error(context.Background(), "hello")

	if buf.Len() != 0 {
		t.Errorf("mức Silent vẫn ghi log: %s", buf.String())
	}
}

// GORM dùng rows = -1 cho câu lệnh không có số dòng (DDL). Ghi -1 vào log làm
// mọi biểu đồ trung bình rows sai.
func TestGormLogger_KhongGhiRowsAm(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := newGormLogger(log, time.Second, logger.Info, false)

	g.Trace(context.Background(), time.Now(), tracer("CREATE TABLE t (id int)", -1), nil)

	line := decodeLine(t, buf)
	if _, ok := line["rows"]; ok {
		t.Errorf("rows = %v được ghi ra dù là -1", line["rows"])
	}
}

func TestGormLogger_LogMode(t *testing.T) {
	log, buf := newCaptureLogger(t)
	base := newGormLogger(log, time.Second, logger.Silent, false)

	verbose := base.LogMode(logger.Info)
	verbose.Trace(context.Background(), time.Now(), tracer("SELECT 1", 1), nil)
	if buf.Len() == 0 {
		t.Error("logger sau LogMode(Info) không ghi gì")
	}

	buf.Reset()
	base.Trace(context.Background(), time.Now(), tracer("SELECT 1", 1), nil)
	if buf.Len() != 0 {
		t.Errorf("LogMode sửa luôn logger gốc: %s", buf.String())
	}
}

func TestGormLogger_InfoWarnError(t *testing.T) {
	tests := []struct {
		level logger.LogLevel
		call  func(g *gormLogger)
		want  string
	}{
		{logger.Info, func(g *gormLogger) { g.Info(context.Background(), "n=%d", 1) }, "INFO"},
		{logger.Warn, func(g *gormLogger) { g.Warn(context.Background(), "n=%d", 1) }, "WARN"},
		{logger.Error, func(g *gormLogger) { g.Error(context.Background(), "n=%d", 1) }, "ERROR"},
	}
	for _, tc := range tests {
		log, buf := newCaptureLogger(t)
		g := newGormLogger(log, time.Second, tc.level, false)
		tc.call(g)

		line := decodeLine(t, buf)
		if line["level"] != tc.want {
			t.Errorf("level = %v, muốn %s", line["level"], tc.want)
		}
		if line["msg"] != "n=1" {
			t.Errorf("msg = %v, muốn %q (format string chưa được áp dụng)", line["msg"], "n=1")
		}
	}
}

// Tham số của query là dữ liệu người dùng. Mặc định chúng không được đi vào log.
func TestGormLogger_ParamsFilter(t *testing.T) {
	log, _ := newCaptureLogger(t)

	g := newGormLogger(log, time.Second, logger.Info, false)
	sql, vars := g.ParamsFilter(context.Background(), "SELECT ?", "0912345678")
	if sql != "SELECT ?" {
		t.Errorf("sql = %q", sql)
	}
	if vars != nil {
		t.Errorf("vars = %v, muốn nil khi LogSQLParams = false", vars)
	}

	g = newGormLogger(log, time.Second, logger.Info, true)
	_, vars = g.ParamsFilter(context.Background(), "SELECT ?", "0912345678")
	if len(vars) != 1 || vars[0] != "0912345678" {
		t.Errorf("vars = %v, muốn giữ nguyên khi LogSQLParams = true", vars)
	}
}

func TestNewGormLogger_MacDinh(t *testing.T) {
	log, buf := newCaptureLogger(t)
	g := NewGormLogger(log, 0)

	// slow = 0 phải rơi về DefaultSlowThreshold, không phải "mọi query đều chậm".
	g.Trace(context.Background(), time.Now(), tracer("SELECT 1", 1), nil)
	if buf.Len() != 0 {
		t.Errorf("query nhanh bị coi là chậm: %s", buf.String())
	}

	g.Trace(context.Background(), time.Now().Add(-time.Second), tracer("SELECT 1", 1), nil)
	if line := decodeLine(t, buf); line["msg"] != "slow sql" {
		t.Errorf("query 1s không bị coi là chậm: %v", line)
	}
}

func TestNewGormLogger_LoggerNil(t *testing.T) {
	// Không panic là yêu cầu duy nhất: nil rơi về slog.Default().
	NewGormLogger(nil, time.Second).Trace(
		context.Background(), time.Now(), tracer("SELECT 1", 1), nil)
}
