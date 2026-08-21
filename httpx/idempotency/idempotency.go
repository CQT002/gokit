// Package idempotency chống tạo trùng khi client gửi lại cùng một request.
//
// Client mobile retry khi mạng lag là chuyện xảy ra hàng ngày, và không có lớp này
// thì một lần retry sinh ra hai bản ghi giao dịch. Cách chuẩn của ngành: client gửi
// header Idempotency-Key, server lưu kết quả theo key đó và trả lại kết quả cũ cho
// mọi lần gửi lại.
//
// Interface Store nằm ở đây còn implementation Redis nằm ở cache/idemstore, nên
// module httpx không phải phụ thuộc Redis. Bản in-memory trong package này dùng cho
// test và cho service chạy một instance.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/httpx"
)

// ErrInFlight là lỗi Store trả về khi một request cùng key đang được xử lý.
var ErrInFlight = errors.New("idempotency: request cùng key đang được xử lý")

// Record là kết quả đã lưu của một request.
type Record struct {
	// Status là HTTP status đã trả.
	Status int
	// Headers là các header cần trả lại.
	Headers map[string]string
	// Body là response body đã trả.
	Body []byte
	// ReqHash là hash của request đã sinh ra kết quả này, để phát hiện việc dùng
	// lại key cho payload khác.
	ReqHash string
}

// Store lưu kết quả theo idempotency key.
type Store interface {
	// Reserve giành quyền xử lý cho key.
	//
	// Ba kết quả:
	//   - key chưa có: đánh dấu đang xử lý, trả (nil, false, nil);
	//   - key đã có kết quả: trả (bản ghi, true, nil);
	//   - key đang được xử lý: trả ErrInFlight.
	//
	// Việc kiểm tra và đánh dấu phải là **một thao tác nguyên tử**. Không thì hai
	// request đến cùng lúc đều thấy "chưa có" và đều chạy handler — đúng cái mà lớp
	// này tồn tại để ngăn.
	Reserve(ctx context.Context, key, reqHash string, ttl time.Duration) (*Record, bool, error)

	// Commit lưu kết quả cho key và mở khoá.
	Commit(ctx context.Context, key string, rec Record, ttl time.Duration) error

	// Release nhả khoá mà không lưu kết quả, dùng khi handler panic hoặc lỗi.
	//
	// Không có bước này thì một request panic sẽ khoá key đó tới hết TTL, và client
	// nhận 409 suốt thời gian đó dù chẳng có gì đang chạy.
	Release(ctx context.Context, key string) error
}

// Giá trị mặc định của Config.
const (
	DefaultTTL        = 24 * time.Hour
	DefaultHeaderName = "Idempotency-Key"
	DefaultMaxBody    = 1 << 20
)

// Config cấu hình Middleware.
type Config struct {
	// Store là nơi lưu kết quả. Bắt buộc.
	Store Store

	// TTL là thời gian giữ kết quả. <= 0 thì dùng DefaultTTL.
	//
	// 24 giờ là mức hợp lý: đủ dài để phủ mọi lần retry thật của client, đủ ngắn để
	// không giữ dữ liệu mãi.
	TTL time.Duration

	// HeaderName là tên header mang key. Rỗng thì dùng DefaultHeaderName.
	HeaderName string

	// Methods là các method áp dụng. Rỗng thì dùng POST và PATCH.
	//
	// GET và DELETE không cần: chúng đã idempotent theo đặc tả HTTP. PUT cũng vậy
	// về lý thuyết, nhưng thực tế nhiều API dùng PUT để tạo nên vẫn khai được.
	Methods []string

	// Required = true thì request thiếu header sẽ bị trả 400.
	//
	// Bật cho các endpoint tạo giao dịch: ở đó, một request không có key là một
	// request không an toàn khi retry, và tốt hơn là từ chối ngay.
	Required bool

	// MaxBody là số byte tối đa đọc để tính hash. <= 0 thì dùng DefaultMaxBody.
	MaxBody int64

	// Logger dùng để ghi log các tình huống bất thường. nil thì dùng slog.Default().
	Logger *slog.Logger
}

// Middleware chặn request trùng theo idempotency key.
//
// Ba tình huống và cách xử lý:
//
//	Cùng key, cùng request hash   → trả lại response đã lưu, KHÔNG chạy handler
//	Cùng key, khác request hash   → 422; client dùng lại key cho payload khác
//	Cùng key, request trước đang chạy → 409 kèm Retry-After
//
// Trường hợp thứ hai đáng nói: nó là lỗi phía client, và trả về response cũ sẽ tệ
// hơn nhiều — client tưởng payload mới đã được xử lý.
func Middleware(cfg Config) (func(http.Handler) http.Handler, error) {
	if cfg.Store == nil {
		return nil, errors.New("idempotency: Config thiếu Store")
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	header := cfg.HeaderName
	if header == "" {
		header = DefaultHeaderName
	}
	methods := cfg.Methods
	if len(methods) == 0 {
		methods = []string{http.MethodPost, http.MethodPatch}
	}
	maxBody := cfg.MaxBody
	if maxBody <= 0 {
		maxBody = DefaultMaxBody
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	retryAfter := strconv.Itoa(1)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !slices.Contains(methods, r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get(header)
			if key == "" {
				if cfg.Required {
					httpx.Fail(w, r, errs.New(errs.CodeBadRequest,
						"thiếu header "+header,
						errs.WithField(header, "bắt buộc với method này")))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			hash, err := hashBody(r, maxBody)
			if err != nil {
				httpx.Fail(w, r, errs.New(errs.CodeBadRequest, "không đọc được body", errs.WithCause(err)))
				return
			}

			rec, found, err := cfg.Store.Reserve(r.Context(), key, hash, ttl)
			switch {
			case errors.Is(err, ErrInFlight):
				w.Header().Set("Retry-After", retryAfter)
				httpx.Fail(w, r, errs.New(errs.CodeConflict,
					"request cùng khoá đang được xử lý, vui lòng thử lại sau"))
				return

			case err != nil:
				// Store lỗi: không biết được request này đã chạy chưa. Từ chối là
				// lựa chọn an toàn — chạy tiếp có thể tạo bản ghi trùng, đúng cái
				// mà lớp này tồn tại để ngăn.
				log.ErrorContext(r.Context(), "idempotency store lỗi",
					slog.String("key", key), slog.Any("error", err))
				httpx.Fail(w, r, errs.New(errs.CodeUnavailable,
					"không kiểm tra được trạng thái request, vui lòng thử lại"))
				return

			case found && rec.ReqHash != hash:
				httpx.Fail(w, r, errs.New(errs.CodeValidation,
					"khoá idempotency này đã dùng cho một request khác",
					errs.WithField(header, "đã dùng với payload khác")))
				return

			case found:
				replay(w, rec)
				return
			}

			// Chưa có: chạy handler và lưu kết quả.
			rw := &captureWriter{ResponseWriter: w, status: http.StatusOK}

			committed := false
			defer func() {
				if committed {
					return
				}
				// Handler panic hoặc thoát bất thường: nhả khoá để lần thử lại của
				// client không nhận 409 tới hết TTL.
				if relErr := cfg.Store.Release(context.WithoutCancel(r.Context()), key); relErr != nil {
					log.ErrorContext(r.Context(), "không nhả được khoá idempotency",
						slog.String("key", key), slog.Any("error", relErr))
				}
			}()

			next.ServeHTTP(rw, r)

			// Chỉ lưu kết quả thành công. Lưu cả lỗi 5xx nghĩa là client retry sẽ
			// nhận lại đúng lỗi đó mãi, dù nguyên nhân đã hết.
			if rw.status >= 200 && rw.status < 400 {
				rec := Record{
					Status:  rw.status,
					Headers: map[string]string{"Content-Type": rw.Header().Get("Content-Type")},
					Body:    rw.body,
					ReqHash: hash,
				}
				if err := cfg.Store.Commit(context.WithoutCancel(r.Context()), key, rec, ttl); err != nil {
					log.ErrorContext(r.Context(), "không lưu được kết quả idempotency",
						slog.String("key", key), slog.Any("error", err))
				}
				committed = true
			}
		})
	}, nil
}

// replay trả lại response đã lưu.
func replay(w http.ResponseWriter, rec *Record) {
	for k, v := range rec.Headers {
		if v != "" {
			w.Header().Set(k, v)
		}
	}
	// Header này để client và người vận hành biết đây là bản phát lại, không phải
	// lần xử lý mới — thiếu nó thì hai bên không cách nào phân biệt.
	w.Header().Set("Idempotent-Replay", "true")
	w.WriteHeader(rec.Status)
	// Body này là response do chính handler của service sinh ra ở lần gọi trước, đã
	// lưu nguyên văn kèm Content-Type. Phát lại nó không thêm rủi ro nào so với lần
	// trả đầu tiên.
	//nolint:gosec // G705: dữ liệu do chính service sinh ra, không phải input người dùng
	_, _ = w.Write(rec.Body)
}

// hashBody tính hash của body rồi trả body lại cho handler.
func hashBody(r *http.Request, maxBody int64) (string, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return hashOf(nil), nil
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return "", err
	}
	r.Body = &replayBody{Reader: newBytesReader(body), closer: r.Body}
	return hashOf(body), nil
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// captureWriter ghi lại status và body để lưu vào Store.
type captureWriter struct {
	http.ResponseWriter
	status      int
	body        []byte
	wroteHeader bool
}

func (c *captureWriter) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.wroteHeader = true
	c.body = append(c.body, b...)
	return c.ResponseWriter.Write(b)
}

// Unwrap cho http.ResponseController tìm tới ResponseWriter gốc.
func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
