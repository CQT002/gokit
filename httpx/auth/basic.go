package auth

import (
	"context"
	"net/http"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/httpx"
)

// APIKey xác thực bằng API key trong header.
//
// validate nhận key thô và trả về true nếu hợp lệ. Nó nhận context để tra được key
// trong DB hoặc cache, và **phải** so sánh bằng thời gian không đổi —
// secret.Secret.Equal có sẵn cho việc đó. So bằng == làm thời gian thực thi phụ
// thuộc số byte khớp đầu chuỗi, đủ để dò dần ra key qua nhiều lần thử.
//
// header rỗng thì dùng "X-API-Key".
func APIKey(header string, validate func(ctx context.Context, key string) bool) func(http.Handler) http.Handler {
	if header == "" {
		header = "X-API-Key"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(header)
			if key == "" || validate == nil || !validate(r.Context(), key) {
				writeUnauthorized(w, r, "API key không hợp lệ")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BasicAuth xác thực bằng HTTP Basic.
//
// realm rỗng thì dùng "restricted". Khi từ chối, trả header WWW-Authenticate theo
// đúng đặc tả — thiếu nó thì client HTTP thông thường sẽ không biết phải gửi lại
// kèm thông tin đăng nhập.
//
// validate cũng phải so sánh bằng thời gian không đổi, cùng lý do như APIKey.
func BasicAuth(realm string, validate func(ctx context.Context, user, pass string) bool) func(http.Handler) http.Handler {
	if realm == "" {
		realm = "restricted"
	}
	challenge := `Basic realm="` + realm + `", charset="UTF-8"`

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || validate == nil || !validate(r.Context(), user, pass) {
				w.Header().Set("WWW-Authenticate", challenge)
				writeUnauthorized(w, r, "thông tin đăng nhập không hợp lệ")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeUnauthorized trả 401 theo đúng Envelope chung của httpx.
//
// Thông báo luôn chung chung. Phân biệt "token hết hạn" với "signature sai" là thông
// tin có giá trị với người đang thử tấn công; lý do thật thuộc về log.
func writeUnauthorized(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.Fail(w, r, errs.New(errs.CodeUnauthorized, msg))
}
