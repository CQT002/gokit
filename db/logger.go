package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm/logger"
)

// NewGormLogger dựng logger.Interface của GORM ghi qua slog.
//
// slow là ngưỡng coi một query là chậm; 0 thì dùng DefaultSlowThreshold. Query
// vượt ngưỡng được ghi ở mức Warn kèm attr slow=true, nên dashboard đếm được
// chúng mà không cần parse text.
//
// Mức log là Warn: chỉ lỗi và query chậm. Muốn ghi mọi câu query thì dùng Open
// với Config.LogLevel = "info" — hàm này để dành cho trường hợp tự dựng
// *gorm.DB mà vẫn muốn log giống phần còn lại của service.
//
// Logger trả về **không** ghi giá trị tham số của query. Xem Config.LogSQLParams.
func NewGormLogger(l *slog.Logger, slow time.Duration) logger.Interface {
	if slow <= 0 {
		slow = DefaultSlowThreshold
	}
	return newGormLogger(l, slow, logger.Warn, false)
}

// gormLogger nối logger.Interface của GORM vào slog.
//
// Mọi method đều nhận context và chuyển nó xuống slog, nên handler của
// core/log đính được trace_id vào từng dòng log SQL. Đây là lý do chính để
// không dùng logger mặc định của GORM: nó ghi ra stdout qua log.Printf và
// không có đường nào nối câu query với request đã sinh ra nó.
type gormLogger struct {
	log       *slog.Logger
	slow      time.Duration
	level     logger.LogLevel
	logParams bool
}

func newGormLogger(l *slog.Logger, slow time.Duration, level logger.LogLevel, logParams bool) *gormLogger {
	if l == nil {
		l = slog.Default()
	}
	if slow <= 0 {
		slow = DefaultSlowThreshold
	}
	return &gormLogger{log: l, slow: slow, level: level, logParams: logParams}
}

// LogMode trả về bản copy có mức log khác. GORM gọi nó khi dùng
// Session{Logger: ...} hoặc db.Debug().
func (g *gormLogger) LogMode(level logger.LogLevel) logger.Interface {
	c := *g
	c.level = level
	return &c
}

func (g *gormLogger) Info(ctx context.Context, msg string, data ...any) {
	if g.level >= logger.Info {
		g.log.InfoContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (g *gormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if g.level >= logger.Warn {
		g.log.WarnContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (g *gormLogger) Error(ctx context.Context, msg string, data ...any) {
	if g.level >= logger.Error {
		g.log.ErrorContext(ctx, fmt.Sprintf(msg, data...))
	}
}

// Trace ghi log cho một câu query đã chạy.
//
// Phân loại theo ba nhánh, và thứ tự có ý nghĩa:
//
//  1. Có lỗi → Error. Trừ ErrRecordNotFound: "không tìm thấy" là luồng nghiệp
//     vụ bình thường của First/Take, không phải sự cố. Log nó ở mức Error là
//     cách nhanh nhất để làm alert theo error rate thành vô dụng.
//  2. Vượt ngưỡng chậm → Warn kèm slow=true.
//  3. Còn lại → Debug, và chỉ khi mức log là Info. Ở production mặc định
//     (Warn) nhánh này không gọi fc() nên không phải dựng chuỗi SQL.
func (g *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if g.level <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)

	switch {
	case err != nil && !errors.Is(err, logger.ErrRecordNotFound):
		if g.level < logger.Error {
			return
		}
		sql, rows := fc()
		g.log.LogAttrs(ctx, slog.LevelError, "sql error", g.attrs(sql, rows, elapsed,
			slog.String("error", err.Error()))...)

	case elapsed > g.slow:
		if g.level < logger.Warn {
			return
		}
		sql, rows := fc()
		g.log.LogAttrs(ctx, slog.LevelWarn, "slow sql", g.attrs(sql, rows, elapsed,
			slog.Bool("slow", true),
			slog.Duration("threshold", g.slow))...)

	case g.level >= logger.Info:
		if !g.log.Enabled(ctx, slog.LevelDebug) {
			return
		}
		sql, rows := fc()
		g.log.LogAttrs(ctx, slog.LevelDebug, "sql", g.attrs(sql, rows, elapsed)...)
	}
}

// attrs gom các attr chung của một dòng log SQL.
//
// elapsed_ms là số thực millisecond thay vì time.Duration: query nhanh nằm ở
// khoảng dưới 1ms, và ở dạng số thì backend log tính được percentile.
func (g *gormLogger) attrs(sql string, rows int64, elapsed time.Duration, extra ...slog.Attr) []slog.Attr {
	attrs := make([]slog.Attr, 0, 3+len(extra))
	attrs = append(attrs,
		slog.String("sql", sql),
		slog.Float64("elapsed_ms", float64(elapsed.Nanoseconds())/1e6),
	)
	// -1 là cách GORM nói "câu lệnh này không có số dòng", ví dụ DDL. Ghi -1 ra
	// log thì mọi biểu đồ trung bình rows đều sai.
	if rows >= 0 {
		attrs = append(attrs, slog.Int64("rows", rows))
	}
	return append(attrs, extra...)
}

// ParamsFilter quyết định câu SQL đi vào log có kèm giá trị tham số không.
//
// GORM gọi hàm này nếu logger cài đặt nó (interface gorm.ParamsFilter). Trả về
// vars = nil làm GORM giữ nguyên dấu `?` trong câu lệnh thay vì thay bằng giá
// trị thật — vốn là dữ liệu người dùng.
func (g *gormLogger) ParamsFilter(_ context.Context, sql string, params ...any) (string, []any) {
	if g.logParams {
		return sql, params
	}
	return sql, nil
}
