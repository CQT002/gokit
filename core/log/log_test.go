package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/ctxmeta"
	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/core/tracectx"
)

// newTestLogger dựng logger ghi vào buffer, có bỏ time để so sánh được.
func newTestLogger(t *testing.T, opts log.Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	opts.Output = buf
	return log.New(opts), buf
}

// safeGroup log một giá trị qua Safe rồi trả về object body đã parse.
func safeGroup(t *testing.T, v any) map[string]any {
	t.Helper()
	logger, buf := newTestLogger(t, log.Options{})
	logger.Info("m", "body", log.Safe(v))

	body := jsonField(t, buf.String(), "body")
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body không phải object: %#v (dòng log: %s)", body, buf.String())
	}
	return m
}

// jsonField parse dòng log JSON và lấy một field.
func jsonField(t *testing.T, line, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("dòng log không phải JSON: %v\n%s", err, line)
	}
	return m[key]
}

func TestNew_MacDinhLaJSON(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.Info("xin chào")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("mặc định phải là JSON: %v\n%s", err, buf.String())
	}
	if m["msg"] != "xin chào" {
		t.Errorf("msg = %#v", m["msg"])
	}
	if m["level"] != "INFO" {
		t.Errorf("level = %#v", m["level"])
	}
	if m["time"] == nil {
		t.Error("thiếu time")
	}
}

func TestNew_Text(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{Format: log.FormatText})
	logger.Info("xin chào", "k", "v")

	out := buf.String()
	if !strings.Contains(out, "msg=") || !strings.Contains(out, "k=v") {
		t.Errorf("không phải định dạng text: %s", out)
	}
}

func TestNew_Level(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{Level: slog.LevelWarn})
	logger.Info("bị lọc")
	logger.Debug("bị lọc")
	if buf.Len() != 0 {
		t.Errorf("dòng dưới mức Warn vẫn được ghi: %s", buf.String())
	}

	logger.Warn("được ghi")
	if buf.Len() == 0 {
		t.Error("dòng mức Warn bị lọc oan")
	}
}

// Mức mặc định phải là Info, vì slog.Level zero là Info — không được vô tình
// thành mức khác.
func TestNew_LevelMacDinhLaInfo(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.Debug("không ghi")
	if buf.Len() != 0 {
		t.Errorf("Debug được ghi khi chưa khai Level: %s", buf.String())
	}
	logger.Info("ghi")
	if buf.Len() == 0 {
		t.Error("Info không được ghi khi chưa khai Level")
	}
}

func TestNew_AppName(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{AppName: "dich-vu-a"})
	logger.Info("m")
	if got := jsonField(t, buf.String(), "app"); got != "dich-vu-a" {
		t.Errorf("app = %#v", got)
	}

	logger2, buf2 := newTestLogger(t, log.Options{})
	logger2.Info("m")
	var m map[string]any
	_ = json.Unmarshal(buf2.Bytes(), &m)
	if _, ok := m["app"]; ok {
		t.Error("không khai AppName mà vẫn có field app")
	}
}

func TestNew_AddSource(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{AddSource: true})
	logger.Info("m")

	src, ok := jsonField(t, buf.String(), "source").(map[string]any)
	if !ok {
		t.Fatalf("thiếu source: %s", buf.String())
	}
	// Phải trỏ về file test này, không phải vào ruột package log — chain handler
	// dựng lại record nên rất dễ làm mất PC.
	if f, _ := src["file"].(string); !strings.HasSuffix(f, "log_test.go") {
		t.Errorf("source.file = %#v, muốn log_test.go — record đã mất PC", src["file"])
	}
}

// ---------- ContextHandler ----------

func TestContextHandler_DinhTraceVaMeta(t *testing.T) {
	sc := tracectx.NewRoot()
	ctx := tracectx.WithSpanContext(context.Background(), sc)
	ctx = ctxmeta.With(ctx, ctxmeta.Meta{
		RequestID:     "req-1",
		CorrelationID: "corr-1",
		UserID:        "user-1",
		UserType:      "employee",
	})

	logger, buf := newTestLogger(t, log.Options{})
	logger.InfoContext(ctx, "đã xử lý", "k", "v")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("JSON lỗi: %v\n%s", err, buf.String())
	}
	want := map[string]string{
		log.AttrTraceID:       sc.TraceID,
		log.AttrSpanID:        sc.SpanID,
		log.AttrRequestID:     "req-1",
		log.AttrCorrelationID: "corr-1",
		log.AttrUserID:        "user-1",
		log.AttrUserType:      "employee",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %#v, muốn %q", k, m[k], v)
		}
	}
	if m["k"] != "v" {
		t.Errorf("attribute của chỗ gọi bị mất: %#v", m["k"])
	}
}

func TestContextHandler_BoQuaFieldRong(t *testing.T) {
	ctx := ctxmeta.WithRequestID(context.Background(), "req-1")
	logger, buf := newTestLogger(t, log.Options{})
	logger.InfoContext(ctx, "m")

	var m map[string]any
	_ = json.Unmarshal(buf.Bytes(), &m)
	if m[log.AttrRequestID] != "req-1" {
		t.Errorf("request_id = %#v", m[log.AttrRequestID])
	}
	for _, k := range []string{log.AttrTraceID, log.AttrSpanID, log.AttrUserID, log.AttrCorrelationID, log.AttrUserType} {
		if _, ok := m[k]; ok {
			t.Errorf("field rỗng %s vẫn xuất hiện trong dòng log", k)
		}
	}
}

func TestContextHandler_KhongCoContext(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.Info("không có context")

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("JSON lỗi: %v", err)
	}
	if _, ok := m[log.AttrTraceID]; ok {
		t.Error("có trace_id dù không truyền context")
	}
}

// ---------- With / WithGroup ----------

func TestWith_CungDuocChe(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.With("password", "không được lộ").Info("m")

	if strings.Contains(buf.String(), "không được lộ") {
		t.Fatalf("attribute thêm bằng With không bị che: %s", buf.String())
	}
	if got := jsonField(t, buf.String(), "password"); got != "********" {
		t.Errorf("password = %#v", got)
	}
}

func TestWithGroup(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.WithGroup("http").Info("m", "password", "không được lộ", "status", 200)

	if strings.Contains(buf.String(), "không được lộ") {
		t.Fatalf("attribute trong group không bị che: %s", buf.String())
	}
	g, ok := jsonField(t, buf.String(), "http").(map[string]any)
	if !ok {
		t.Fatalf("thiếu group http: %s", buf.String())
	}
	if g["password"] != "********" {
		t.Errorf("http.password = %#v", g["password"])
	}
	if g["status"] != float64(200) {
		t.Errorf("http.status = %#v", g["status"])
	}
}

func TestGroupLong_VanChe(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.Info("m", slog.Group("req", slog.Group("headers", slog.String("authorization", "Bearer abc"))))

	if strings.Contains(buf.String(), "Bearer abc") {
		t.Fatalf("group lồng group không được che: %s", buf.String())
	}
}

// ---------- Trần MaxLineBytes ----------

func TestMaxLineBytes_BoBodyGiuTraceIDVaStatus(t *testing.T) {
	sc := tracectx.NewRoot()
	ctx := tracectx.WithSpanContext(context.Background(), sc)

	const limit = 2048
	logger, buf := newTestLogger(t, log.Options{
		Mask: log.MaskConfig{
			// MaxLen lớn để lớp 1 không rút gọn trước, buộc trần dòng phải ra tay.
			MaxLen:       1 << 20,
			MaxLineBytes: limit,
		},
	})
	logger.InfoContext(ctx, "request",
		"status", 200,
		"method", "POST",
		"body", strings.Repeat("x", 16<<10),
	)

	out := buf.String()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("JSON lỗi: %v\n%s", err, out)
	}

	body, ok := m["body"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, muốn marker _dropped", m["body"])
	}
	if body["_dropped"] == nil {
		t.Errorf("body không có _dropped: %#v", body)
	}
	if !strings.Contains(body["_dropped"].(string), "2KB") {
		t.Errorf("_dropped = %#v, muốn nhắc tới giới hạn 2KB", body["_dropped"])
	}
	if ob, _ := body["original_bytes"].(float64); ob < 16<<10 {
		t.Errorf("original_bytes = %#v, muốn xấp xỉ kích thước gốc", body["original_bytes"])
	}

	// Đây là điều kiện quan trọng nhất: mất body nhưng không mất khả năng biết
	// request nào đã xảy ra.
	if m[log.AttrTraceID] != sc.TraceID {
		t.Errorf("trace_id = %#v, phải sống sót khi bỏ body", m[log.AttrTraceID])
	}
	if m["status"] != float64(200) {
		t.Errorf("status = %#v, phải sống sót khi bỏ body", m["status"])
	}
	if m["method"] != "POST" {
		t.Errorf("method = %#v, phải sống sót khi bỏ body", m["method"])
	}
	if len(out) > limit*2 {
		t.Errorf("dòng log dài %d byte, trần %d — vẫn quá xa trần", len(out), limit)
	}
}

func TestMaxLineBytes_DongNganKhongBiDungToi(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{})
	logger.Info("request", "status", 200, "body", map[string]any{"name": "an"})

	if strings.Contains(buf.String(), "_dropped") {
		t.Errorf("dòng log ngắn bị bỏ body oan: %s", buf.String())
	}
}

// Bỏ theo thứ tự lớn nhất trước: attribute nhỏ không được hy sinh trước
// attribute to.
func TestMaxLineBytes_BoAttributeLonNhatTruoc(t *testing.T) {
	logger, buf := newTestLogger(t, log.Options{
		Mask: log.MaskConfig{MaxLen: 1 << 20, MaxLineBytes: 4096},
	})
	logger.Info("m",
		"nho", strings.Repeat("a", 200),
		"to", strings.Repeat("b", 8<<10),
	)

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("JSON lỗi: %v\n%s", err, buf.String())
	}
	if _, dropped := m["to"].(map[string]any); !dropped {
		t.Errorf("attribute to = %#v, muốn bị bỏ", m["to"])
	}
	if s, _ := m["nho"].(string); len(s) != 200 {
		t.Errorf("attribute nho = %#v, không nên bị bỏ", m["nho"])
	}
}

// ---------- Golden: hình dạng dòng log ----------

func TestGolden_HinhDangDongLog(t *testing.T) {
	type req struct {
		UserID   string `json:"user_id"`
		Image    string `json:"image" log:"elide"`
		Password string `json:"password" log:"redact"`
		CardNo   string `json:"card_no" log:"edges=6,4"`
	}

	ctx := tracectx.WithSpanContext(context.Background(), tracectx.SpanContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: true,
	})
	ctx = ctxmeta.WithRequestID(ctx, "req-abc")

	logger, buf := newTestLogger(t, log.Options{AppName: "api"})
	logger.InfoContext(ctx, "request", "body", log.Safe(req{
		UserID:   "u-1",
		Image:    strings.Repeat("QUJD", 100),
		Password: "mật khẩu",
		CardNo:   "4111111111111111",
	}))

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("JSON lỗi: %v\n%s", err, buf.String())
	}
	delete(m, "time") // thay đổi mỗi lần chạy

	got, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{
  "app": "api",
  "body": {
    "card_no": "411111******1111",
    "image": {
      "_elided": "base64",
      "bytes": 400,
      "sha256": "fb1302f4"
    },
    "password": "********",
    "user_id": "u-1"
  },
  "level": "INFO",
  "msg": "request",
  "request_id": "req-abc",
  "span_id": "00f067aa0ba902b7",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
}`
	if string(got) != want {
		t.Errorf("hình dạng dòng log đổi:\n--- được ---\n%s\n--- muốn ---\n%s", got, want)
	}
}
