package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LogOptions cấu hình AccessLog.
type LogOptions struct {
	// SkipPaths là các path không ghi access log, thường là /healthz, /readyz,
	// /metrics. Probe của Kubernetes gọi chúng vài lần mỗi giây và không dòng nào
	// mang thông tin.
	SkipPaths []string

	// SlowThreshold là ngưỡng để nâng mức log lên Warn. 0 nghĩa là không nâng.
	SlowThreshold time.Duration
}

// AccessLog ghi một dòng log cho mỗi request đã hoàn tất.
//
// Mức log chọn theo status: 5xx là Error, 4xx là Warn, còn lại là Info. Nhờ vậy
// alert dựa trên mức log không cần biết gì về HTTP.
//
// Dùng InfoContext để core/log tự đính trace_id và request_id, nên middleware này
// phải nằm **trong** Trace.
func AccessLog(l *slog.Logger, opts LogOptions) Middleware {
	skip := opts.SkipPaths

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slices.Contains(skip, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			rec := newRecorder(w, 0)
			start := time.Now()

			// Ghi trong defer để request panic cũng có dòng log. Nếu chỉ ghi sau
			// ServeHTTP thì đúng những request lỗi nặng nhất lại không có log.
			defer func() {
				elapsed := time.Since(start)

				attrs := []any{
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", rec.status),
					slog.Int64("bytes", rec.bytesOut),
					slog.Int64("elapsed_ms", elapsed.Milliseconds()),
					slog.String("remote_ip", clientIP(r)),
				}
				if ua := r.UserAgent(); ua != "" {
					attrs = append(attrs, slog.String("user_agent", ua))
				}
				if q := r.URL.RawQuery; q != "" {
					attrs = append(attrs, slog.String("query", q))
				}

				l.LogAttrs(r.Context(), levelFor(rec.status, elapsed, opts.SlowThreshold),
					"request", toAttrs(attrs)...)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

func levelFor(status int, elapsed, slow time.Duration) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	case slow > 0 && elapsed >= slow:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func toAttrs(vals []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(vals))
	for _, v := range vals {
		if a, ok := v.(slog.Attr); ok {
			attrs = append(attrs, a)
		}
	}
	return attrs
}

// clientIP lấy IP của client, ưu tiên header do proxy đặt.
//
// Chỉ tin X-Forwarded-For và X-Real-IP khi service nằm sau proxy của chính mình —
// client tự đặt được hai header này. Ở đây vẫn đọc chúng vì mọi triển khai thực tế
// đều có ingress phía trước; nếu service phơi trực tiếp ra internet thì coi giá trị
// này là thông tin tham khảo, không phải căn cứ để chặn.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Phần tử đầu là client gốc; các phần tử sau là chuỗi proxy.
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if host, _, err := splitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func splitHostPort(addr string) (host, port string, err error) {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return "", "", errors.New("không có cổng")
	}
	return strings.Trim(addr[:i], "[]"), addr[i+1:], nil
}

// Recover bắt panic trong handler, ghi log kèm stack trace và trả về 500.
//
// Phải là middleware ngoài cùng: nó chỉ bắt được panic của các tầng bên trong nó.
//
// Không ghi gì vào response nếu handler đã ghi header trước khi panic — lúc đó
// status đã đi ra dây rồi, cố ghi thêm chỉ tạo ra một dòng "superfluous
// WriteHeader" trong log và một response body lẫn lộn.
//
// http.ErrAbortHandler được truyền tiếp thay vì bắt: đó là cách net/http báo hiệu
// "hủy response này có chủ ý".
func Recover(l *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := newRecorder(w, 0)

			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}

				l.ErrorContext(r.Context(), "panic trong handler",
					slog.Any("panic", v),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
				)

				if !rec.wroteHeader {
					http.Error(w, http.StatusText(http.StatusInternalServerError),
						http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

// Timeout huỷ context của request sau d, và trả 503 nếu handler chưa ghi gì.
//
// Không dùng http.TimeoutHandler của stdlib: nó buffer **toàn bộ** response trong
// RAM để có thể thay thế khi hết hạn, nên một endpoint trả file lớn sẽ giữ cả file
// trong bộ nhớ ở mọi request.
//
// Cách ở đây: response đi thẳng ra dây, và một writer có khoá đứng giữa. Khi hết
// hạn, writer ghi 503 rồi khoá lại; handler chạy tiếp thì mọi lần ghi sau đó nhận
// http.ErrHandlerTimeout thay vì làm hỏng response đã gửi. Nhờ vậy client nhận
// response ngay tại mốc timeout, không phải chờ handler chịu dừng.
//
// Handler vẫn nên tôn trọng ctx để giải phóng tài nguyên — mọi thao tác DB, Redis
// và HTTP client trong gokit đều tôn trọng.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			tw := &timeoutWriter{ResponseWriter: w}
			done := make(chan struct{})

			go func() {
				// Panic của handler phải nổi lên goroutine này, nếu không nó sẽ làm
				// sập cả process thay vì được Recover bắt. Chuyển sang goroutine gọi.
				defer close(done)
				defer func() {
					if v := recover(); v != nil {
						tw.setPanic(v)
					}
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case <-done:
				if v := tw.recovered(); v != nil {
					panic(v)
				}
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					tw.timedOut("request vượt thời gian xử lý cho phép")
				}
			}
		})
	}
}

// timeoutWriter cho phép ghi response từ handler và ghi thông báo timeout từ
// middleware mà hai bên không đè lên nhau.
type timeoutWriter struct {
	http.ResponseWriter

	mu          sync.Mutex
	wroteHeader bool
	expired     bool
	panicValue  any
}

func (t *timeoutWriter) WriteHeader(status int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.expired || t.wroteHeader {
		return
	}
	t.wroteHeader = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *timeoutWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.expired {
		// Trả lỗi thay vì im lặng bỏ qua: handler biết được là response đã đóng và
		// dừng sớm thay vì tiếp tục sinh dữ liệu không ai nhận.
		return 0, http.ErrHandlerTimeout
	}
	t.wroteHeader = true
	return t.ResponseWriter.Write(b)
}

// timedOut ghi thông báo hết hạn nếu handler chưa ghi gì, rồi đóng writer.
func (t *timeoutWriter) timedOut(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.expired {
		return
	}
	t.expired = true

	if t.wroteHeader {
		// Status đã đi ra dây; không sửa được nữa, và ghi thêm chỉ làm body lẫn lộn.
		return
	}
	t.wroteHeader = true
	http.Error(t.ResponseWriter, msg, http.StatusServiceUnavailable)
}

func (t *timeoutWriter) setPanic(v any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.panicValue = v
}

func (t *timeoutWriter) recovered() any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.panicValue
}

// Unwrap cho http.ResponseController tìm tới ResponseWriter gốc.
func (t *timeoutWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// MaxBodySize giới hạn số byte đọc được từ body của request.
//
// Vượt giới hạn thì lần Read tiếp theo trả lỗi, và Decode sẽ biến nó thành 400 —
// không phải panic, không phải cắt im lặng.
//
// n <= 0 thì middleware không làm gì.
func MaxBodySize(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		if n <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// CORSConfig cấu hình middleware CORS.
type CORSConfig struct {
	// AllowedOrigins là danh sách origin được phép. "*" cho phép mọi origin.
	//
	// "*" cùng AllowCredentials = true là cấu hình bị trình duyệt từ chối theo
	// đặc tả; CORS sẽ trả về lỗi thay vì tạo ra một cấu hình không bao giờ chạy.
	AllowedOrigins []string
	// AllowedMethods rỗng thì dùng GET, POST, PUT, PATCH, DELETE, OPTIONS.
	AllowedMethods []string
	// AllowedHeaders rỗng thì dùng Content-Type, Authorization và các header trace.
	AllowedHeaders []string
	// ExposedHeaders là header mà JavaScript của trang được phép đọc.
	ExposedHeaders []string
	// AllowCredentials cho phép gửi cookie và header Authorization.
	AllowCredentials bool
	// MaxAge là thời gian trình duyệt cache kết quả preflight.
	MaxAge time.Duration
}

// CORS trả về middleware xử lý CORS, kể cả request preflight OPTIONS.
//
// Trả lỗi khi cấu hình tự mâu thuẫn, thay vì âm thầm chạy sai: cấu hình CORS sai
// biểu hiện thành lỗi trên trình duyệt của người dùng cuối, cách rất xa chỗ khai.
func CORS(cfg CORSConfig) (Middleware, error) {
	allowAll := slices.Contains(cfg.AllowedOrigins, "*")
	if allowAll && cfg.AllowCredentials {
		return nil, errors.New(
			"middleware: CORS với AllowedOrigins \"*\" và AllowCredentials = true bị trình duyệt từ chối; hãy liệt kê origin cụ thể")
	}
	if len(cfg.AllowedOrigins) == 0 {
		return nil, errors.New("middleware: CORS cần ít nhất một origin")
	}

	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		}
	}
	headers := cfg.AllowedHeaders
	if len(headers) == 0 {
		headers = []string{
			"Content-Type", "Authorization",
			HeaderRequestID, HeaderCorrelationID, "traceparent",
		}
	}

	allowMethods := strings.Join(methods, ", ")
	allowHeaders := strings.Join(headers, ", ")
	exposeHeaders := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := strconv.Itoa(int(cfg.MaxAge.Seconds()))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Không phải request từ trình duyệt.
				next.ServeHTTP(w, r)
				return
			}

			allowed := allowAll || slices.Contains(cfg.AllowedOrigins, origin)
			if allowed {
				if allowAll {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					// Trả đúng origin của request, không trả cả danh sách — và báo
					// cho cache biết response phụ thuộc header Origin.
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
				if cfg.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if exposeHeaders != "" {
					w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
				}
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Methods", allowMethods)
					w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
					if cfg.MaxAge > 0 {
						w.Header().Set("Access-Control-Max-Age", maxAge)
					}
				}
				// Preflight không đi tới handler kể cả khi origin bị từ chối:
				// thiếu header Allow-Origin đã đủ để trình duyệt chặn.
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}
