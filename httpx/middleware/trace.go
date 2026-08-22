package middleware

import (
	"net/http"

	"github.com/cqt002/gokit/core/ctxmeta"
	"github.com/cqt002/gokit/core/idx"
	"github.com/cqt002/gokit/core/tracectx"
)

// Tên header mặc định cho request ID và correlation ID.
const (
	HeaderRequestID     = "X-Request-Id"
	HeaderCorrelationID = "X-Correlation-Id"
)

// TraceOptions cấu hình middleware Trace.
type TraceOptions struct {
	// RequestIDHeader là header mang request ID. Rỗng thì dùng HeaderRequestID.
	RequestIDHeader string

	// CorrelationIDHeader là header mang correlation ID.
	// Rỗng thì dùng HeaderCorrelationID.
	CorrelationIDHeader string

	// TrustIncomingRequestID cho phép dùng lại request ID do client gửi lên.
	//
	// Mặc định false, và đó là mặc định đúng cho API công khai: request ID do
	// client kiểm soát nghĩa là client trộn được log của mình vào log của request
	// khác, hoặc bơm ký tự lạ vào hệ thống log. Chỉ bật khi thượng nguồn là gateway
	// của chính mình.
	//
	// Correlation ID thì luôn nhận từ client — nó tồn tại chính để client nối các
	// request thuộc cùng một nghiệp vụ.
	TrustIncomingRequestID bool

	// MaxIDLen là độ dài tối đa của ID nhận từ client, mặc định 128.
	//
	// Không có trần thì một header dài vài MB sẽ đi thẳng vào mọi dòng log của
	// request đó.
	MaxIDLen int
}

// DefaultMaxIDLen là trần mặc định cho độ dài ID nhận từ client.
const DefaultMaxIDLen = 128

// Trace đọc hoặc sinh thông tin trace rồi gắn vào context và vào header response.
//
// Việc nó làm, theo thứ tự:
//
//  1. Đọc header traceparent. Hợp lệ thì tạo span con trong cùng trace; không hợp
//     lệ hoặc không có thì bắt đầu một trace mới. Giá trị rác được coi là "không có
//     trace thượng nguồn", không phải lỗi request — thượng nguồn gửi sai không phải
//     lý do để từ chối phục vụ người dùng.
//  2. Sinh request ID, hoặc nhận từ client nếu TrustIncomingRequestID bật.
//  3. Nhận correlation ID từ client nếu có.
//  4. Gắn tất cả vào context để core/log tự đính vào mọi dòng log.
//  5. Ghi traceparent và request ID vào header response, để client và người vận
//     hành đọc được ID mà tra log.
//
// Đây là middleware nên đặt gần ngoài cùng: mọi tầng bên trong đều muốn log có
// trace ID.
func Trace(opts TraceOptions) Middleware {
	requestIDHeader := opts.RequestIDHeader
	if requestIDHeader == "" {
		requestIDHeader = HeaderRequestID
	}
	correlationIDHeader := opts.CorrelationIDHeader
	if correlationIDHeader == "" {
		correlationIDHeader = HeaderCorrelationID
	}
	maxIDLen := opts.MaxIDLen
	if maxIDLen <= 0 {
		maxIDLen = DefaultMaxIDLen
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sc := spanFor(r)

			requestID := idx.NewUUIDv7()
			if opts.TrustIncomingRequestID {
				if v := sanitizeID(r.Header.Get(requestIDHeader), maxIDLen); v != "" {
					requestID = v
				}
			}

			ctx := tracectx.WithSpanContext(r.Context(), sc)
			ctx = ctxmeta.With(ctx, ctxmeta.Meta{
				RequestID:     requestID,
				CorrelationID: sanitizeID(r.Header.Get(correlationIDHeader), maxIDLen),
			})

			// Ghi header trước khi gọi handler: sau khi handler ghi status thì
			// header không đổi được nữa.
			w.Header().Set(tracectx.HeaderTraceparent, sc.Traceparent())
			w.Header().Set(requestIDHeader, requestID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// spanFor tạo SpanContext cho request: span con của thượng nguồn, hoặc trace mới.
func spanFor(r *http.Request) tracectx.SpanContext {
	parent, err := tracectx.ParseTraceparent(r.Header.Get(tracectx.HeaderTraceparent))
	if err != nil {
		return tracectx.NewRoot()
	}
	return parent.NewChild()
}

// sanitizeID cắt và làm sạch ID nhận từ client.
//
// Bỏ mọi ký tự không phải chữ, số, gạch ngang, gạch dưới hoặc dấu chấm. Lý do:
// giá trị này đi vào mọi dòng log của request, và một ký tự xuống dòng trong đó
// làm dòng log tách thành hai — đủ để giả mạo bản ghi log, kỹ thuật gọi là log
// injection.
func sanitizeID(v string, maxLen int) string {
	if v == "" {
		return ""
	}
	if len(v) > maxLen {
		v = v[:maxLen]
	}

	out := make([]byte, 0, len(v))
	for i := range len(v) {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.':
			out = append(out, c)
		}
	}
	return string(out)
}
