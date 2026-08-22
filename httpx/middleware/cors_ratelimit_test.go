package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cqt002/gokit/httpx/middleware"
)

// ---------- CORS ----------

func mustCORS(t *testing.T, cfg middleware.CORSConfig) middleware.Middleware {
	t.Helper()
	mw, err := middleware.CORS(cfg)
	if err != nil {
		t.Fatalf("CORS: %v", err)
	}
	return mw
}

// Cấu hình "*" cùng AllowCredentials bị trình duyệt từ chối theo đặc tả, nên phải
// báo lỗi lúc dựng thay vì tạo ra một cấu hình không bao giờ chạy.
func TestCORS_CauHinhTuMauThuan(t *testing.T) {
	tests := []struct {
		name string
		cfg  middleware.CORSConfig
	}{
		{"sao và credentials", middleware.CORSConfig{
			AllowedOrigins: []string{"*"}, AllowCredentials: true,
		}},
		{"không có origin nào", middleware.CORSConfig{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := middleware.CORS(tt.cfg); err == nil {
				t.Error("muốn lỗi, không có lỗi")
			}
		})
	}
}

func TestCORS_OriginDuocPhep(t *testing.T) {
	h := mustCORS(t, middleware.CORSConfig{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
		ExposedHeaders:   []string{"X-Total-Count"},
	})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, phải trả đúng origin của request", got)
	}
	// Thiếu Vary thì cache trung gian sẽ trả response của origin này cho origin khác.
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Errorf("thiếu Vary: Origin (được %q)", rec.Header().Get("Vary"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("thiếu Allow-Credentials")
	}
	if rec.Header().Get("Access-Control-Expose-Headers") != "X-Total-Count" {
		t.Error("thiếu Expose-Headers")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestCORS_OriginBiTuChoi(t *testing.T) {
	h := mustCORS(t, middleware.CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
	})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Origin", "https://ke-tan-cong.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("origin lạ vẫn nhận được Allow-Origin")
	}
	// Request vẫn tới handler: chặn là việc của trình duyệt, không phải của server.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestCORS_Preflight(t *testing.T) {
	h := mustCORS(t, middleware.CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
		MaxAge:         time.Hour,
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("preflight không được đi tới handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, muốn 204", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("Allow-Methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
	}
	if rec.Header().Get("Access-Control-Max-Age") != "3600" {
		t.Errorf("Max-Age = %q", rec.Header().Get("Access-Control-Max-Age"))
	}
}

// OPTIONS không phải preflight (thiếu Access-Control-Request-Method) phải đi tới
// handler: đó là một method HTTP hợp lệ, không phải riêng của CORS.
func TestCORS_OptionsKhongPhaiPreflight(t *testing.T) {
	reached := false
	h := mustCORS(t, middleware.CORSConfig{AllowedOrigins: []string{"*"}})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodOptions, "/api", nil)
	req.Header.Set("Origin", "https://bat-ky.com")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Error("OPTIONS thường không tới được handler")
	}
}

// Request không có Origin không phải từ trình duyệt: không thêm header CORS nào.
func TestCORS_KhongCoOrigin(t *testing.T) {
	h := mustCORS(t, middleware.CORSConfig{AllowedOrigins: []string{"*"}})(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("thêm header CORS cho request không có Origin")
	}
}

func TestCORS_ChoPhepMoiOrigin(t *testing.T) {
	h := mustCORS(t, middleware.CORSConfig{AllowedOrigins: []string{"*"}})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Header.Set("Origin", "https://bat-ky.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Allow-Origin = %q, muốn *", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

// ---------- RateLimit ----------

func mustRateLimit(t *testing.T, cfg middleware.RateLimitConfig) middleware.Middleware {
	t.Helper()
	mw, err := middleware.RateLimit(cfg)
	if err != nil {
		t.Fatalf("RateLimit: %v", err)
	}
	return mw
}

func TestRateLimit_ChanKhiVuotHanMuc(t *testing.T) {
	h := mustRateLimit(t, middleware.RateLimitConfig{
		Requests: 3,
		Window:   time.Minute,
		KeyFunc:  func(*http.Request) string { return "cùng-một-key" },
	})(okHandler())

	var allowed, blocked int
	for range 10 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		switch rec.Code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			blocked++
			if rec.Header().Get("Retry-After") == "" {
				t.Error("thiếu header Retry-After — client không biết chờ bao lâu")
			}
		default:
			t.Fatalf("status lạ = %d", rec.Code)
		}
	}

	if allowed != 3 {
		t.Errorf("cho qua %d request, muốn 3", allowed)
	}
	if blocked != 7 {
		t.Errorf("chặn %d request, muốn 7", blocked)
	}
}

// Hạn mức phải tính riêng theo từng key: một client vượt hạn mức không được làm
// client khác bị chặn.
func TestRateLimit_TinhRiengTheoKey(t *testing.T) {
	var key string
	h := mustRateLimit(t, middleware.RateLimitConfig{
		Requests: 1,
		Window:   time.Minute,
		KeyFunc:  func(*http.Request) string { return key },
	})(okHandler())

	send := func(k string) int {
		key = k
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Code
	}

	if got := send("client-a"); got != http.StatusOK {
		t.Errorf("client-a lần 1 = %d", got)
	}
	if got := send("client-a"); got != http.StatusTooManyRequests {
		t.Errorf("client-a lần 2 = %d, muốn 429", got)
	}
	if got := send("client-b"); got != http.StatusOK {
		t.Errorf("client-b lần 1 = %d — bị chặn oan vì client-a", got)
	}
}

func TestRateLimit_KeyMacDinhTheoIP(t *testing.T) {
	h := mustRateLimit(t, middleware.RateLimitConfig{Requests: 1, Window: time.Minute})(okHandler())

	send := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := send("1.1.1.1"); got != http.StatusOK {
		t.Errorf("IP 1 lần 1 = %d", got)
	}
	if got := send("1.1.1.1"); got != http.StatusTooManyRequests {
		t.Errorf("IP 1 lần 2 = %d, muốn 429", got)
	}
	if got := send("2.2.2.2"); got != http.StatusOK {
		t.Errorf("IP 2 lần 1 = %d", got)
	}
}

func TestRateLimit_CauHinhSai(t *testing.T) {
	for _, requests := range []int{0, -1} {
		if _, err := middleware.RateLimit(middleware.RateLimitConfig{Requests: requests}); err == nil {
			t.Errorf("Requests=%d không báo lỗi", requests)
		}
	}
}

// Trần số key là bắt buộc, không phải tối ưu: hạn mức theo IP với map không giới hạn
// là một đường làm hết bộ nhớ, và người tấn công chỉ cần đổi IP nguồn.
func TestRateLimit_TranSoKey(t *testing.T) {
	h := mustRateLimit(t, middleware.RateLimitConfig{
		Requests: 1,
		Window:   time.Minute,
		MaxKeys:  10,
		KeyFunc:  func(r *http.Request) string { return r.URL.Path },
	})(okHandler())

	// 500 key khác nhau: bộ nhớ không được phình theo số key.
	for i := range 500 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/key-"+strings.Repeat("x", i%50), nil))
		if rec.Code != http.StatusOK && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status lạ = %d", rec.Code)
		}
	}
}

func TestRateLimit_HeaderHanMuc(t *testing.T) {
	h := mustRateLimit(t, middleware.RateLimitConfig{
		Requests: 5,
		Window:   time.Minute,
		KeyFunc:  func(*http.Request) string { return "k" },
	})(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("X-RateLimit-Limit = %q", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("thiếu X-RateLimit-Remaining")
	}
}

// ---------- AccessLog ----------

func TestAccessLog_MucTheoStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusOK, "INFO"},
		{http.StatusCreated, "INFO"},
		{http.StatusBadRequest, "WARN"},
		{http.StatusNotFound, "WARN"},
		{http.StatusInternalServerError, "ERROR"},
		{http.StatusServiceUnavailable, "ERROR"},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			l, buf := newLogger(t)
			h := middleware.AccessLog(l, middleware.LogOptions{})(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) }))

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

			logs := lines(t, buf)
			if len(logs) != 1 {
				t.Fatalf("số dòng log = %d", len(logs))
			}
			if logs[0]["level"] != tt.want {
				t.Errorf("level = %v, muốn %v", logs[0]["level"], tt.want)
			}
			if logs[0]["status"] != float64(tt.status) {
				t.Errorf("status = %v", logs[0]["status"])
			}
		})
	}
}

func TestAccessLog_NoiDung(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.AccessLog(l, middleware.LogOptions{})(okHandler())

	req := httptest.NewRequest(http.MethodPost, "/api/users?page=2", nil)
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	m := lines(t, buf)[0]
	wants := map[string]any{
		"method":     "POST",
		"path":       "/api/users",
		"query":      "page=2",
		"user_agent": "test-agent",
		"remote_ip":  "203.0.113.9",
	}
	for k, want := range wants {
		if m[k] != want {
			t.Errorf("%s = %#v, muốn %#v", k, m[k], want)
		}
	}
	if m["elapsed_ms"] == nil || m["bytes"] == nil {
		t.Errorf("thiếu elapsed_ms hoặc bytes: %#v", m)
	}
}

// Request panic là loại cần thấy nhất, nên dòng log phải được ghi trong defer.
func TestAccessLog_HandlerPanic(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.AccessLog(l, middleware.LogOptions{})(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic("bùm") }))

	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	if len(lines(t, buf)) != 1 {
		t.Errorf("mất dòng access log của request panic: %s", buf.String())
	}
}

func TestAccessLog_SkipPaths(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.AccessLog(l, middleware.LogOptions{
		SkipPaths: []string{"/healthz"},
	})(okHandler())

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if buf.Len() != 0 {
		t.Errorf("path bị skip vẫn ghi log: %s", buf.String())
	}
}

func TestAccessLog_NguongCham(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.AccessLog(l, middleware.LogOptions{
		SlowThreshold: time.Millisecond,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := lines(t, buf)[0]["level"]; got != "WARN" {
		t.Errorf("level = %v, request chậm phải là WARN", got)
	}
}
