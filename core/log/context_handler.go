package log

import (
	"context"
	"log/slog"

	"github.com/cqt002/gokit/core/ctxmeta"
	"github.com/cqt002/gokit/core/tracectx"
)

// Tên attribute mà ContextHandler tự thêm vào mỗi dòng log.
const (
	AttrTraceID       = "trace_id"
	AttrSpanID        = "span_id"
	AttrRequestID     = "request_id"
	AttrCorrelationID = "correlation_id"
	AttrUserID        = "user_id"
	AttrUserType      = "user_type"
)

// contextHandler đọc trace và metadata trong context rồi đính vào record.
type contextHandler struct {
	next slog.Handler
}

// NewContextHandler bọc next để mọi lời gọi có context tự mang theo trace_id,
// span_id, request_id, correlation_id, user_id và user_type.
//
//	logger.InfoContext(ctx, "đã xử lý")
//
// Nhờ vậy không chỗ nào phải nhớ đính tay, và một dòng log thiếu trace_id trở
// thành dấu hiệu rõ ràng là chỗ đó gọi thiếu context.
//
// Field nào rỗng thì không xuất hiện, để dòng log không đầy key rỗng.
func NewContextHandler(next slog.Handler) slog.Handler {
	return &contextHandler{next: next}
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx == nil {
		return h.next.Handle(ctx, r)
	}

	attrs := make([]slog.Attr, 0, 6)
	if sc, ok := tracectx.FromContext(ctx); ok {
		attrs = appendNonEmpty(attrs, AttrTraceID, sc.TraceID)
		attrs = appendNonEmpty(attrs, AttrSpanID, sc.SpanID)
	}

	m := ctxmeta.From(ctx)
	attrs = appendNonEmpty(attrs, AttrRequestID, m.RequestID)
	attrs = appendNonEmpty(attrs, AttrCorrelationID, m.CorrelationID)
	attrs = appendNonEmpty(attrs, AttrUserID, m.UserID)
	attrs = appendNonEmpty(attrs, AttrUserType, m.UserType)

	if len(attrs) == 0 {
		return h.next.Handle(ctx, r)
	}

	// Dựng record mới thay vì AddAttrs vào r: Record chia sẻ backing array nên
	// sửa trực tiếp có thể đè lên attribute của handler khác trong chain.
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	out.AddAttrs(attrs...)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(a)
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}

func appendNonEmpty(attrs []slog.Attr, key, val string) []slog.Attr {
	if val == "" {
		return attrs
	}
	return append(attrs, slog.String(key, val))
}
