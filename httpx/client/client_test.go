package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/core/retry"
	"github.com/cqt002/gokit/core/tracectx"
	"github.com/cqt002/gokit/httpx/client"
)

// fastRetry là policy cho test: delay nhỏ, không jitter.
func fastRetry(maxAttempts int) retry.Policy {
	return retry.Policy{
		MaxAttempts: maxAttempts,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Jitter:      -1,
	}
}

func mustClient(t *testing.T, cfg client.Config) *client.Client {
	t.Helper()
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("page") != "2" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":5}`))
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})

	resp, err := c.Do(context.Background(), client.Request{
		Method: http.MethodGet,
		Path:   "/api/users",
		Query:  map[string][]string{"page": {"2"}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("Status = %d", resp.Status)
	}
	if string(resp.Body) != `{"total":5}` {
		t.Errorf("Body = %q", resp.Body)
	}
}

// 4xx là dữ liệu, không phải lỗi của Go: chỗ gọi cần đọc được status và body.
func TestDo_4xxKhongPhaiLoi(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"không thấy"}`))
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(3)})

	resp, err := c.Do(context.Background(), client.Request{Path: "/x"})
	if err != nil {
		t.Fatalf("Do trả lỗi cho 404: %v", err)
	}
	if resp.Status != http.StatusNotFound {
		t.Errorf("Status = %d", resp.Status)
	}
}

// 4xx không tự khỏi nên không được retry; 5xx và 429 thì có.
func TestDo_RetryDungLoai(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantCalls int32
	}{
		{"400 không retry", http.StatusBadRequest, 1},
		{"404 không retry", http.StatusNotFound, 1},
		{"422 không retry", http.StatusUnprocessableEntity, 1},
		{"429 có retry", http.StatusTooManyRequests, 3},
		{"500 có retry", http.StatusInternalServerError, 3},
		{"502 có retry", http.StatusBadGateway, 3},
		{"503 có retry", http.StatusServiceUnavailable, 3},
		{"504 có retry", http.StatusGatewayTimeout, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(3)})
			resp, _ := c.Do(context.Background(), client.Request{Path: "/x"})

			if got := calls.Load(); got != tt.wantCalls {
				t.Errorf("gọi %d lần, muốn %d", got, tt.wantCalls)
			}
			if resp == nil || resp.Status != tt.status {
				t.Errorf("resp = %+v, muốn status %d ở lần cuối", resp, tt.status)
			}
		})
	}
}

func TestDo_RetryRoiThanhCong(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(5)})

	resp, err := c.Do(context.Background(), client.Request{Path: "/x"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("Status = %d", resp.Status)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("gọi %d lần, muốn 3", got)
	}
}

// Body phải gửi lại đầy đủ ở mỗi lần thử. Reader đã đọc hết thì lần retry sẽ gửi
// body rỗng — loại bug rất khó thấy.
func TestDo_BodyGuiLaiDuOMoiLanThu(t *testing.T) {
	const payload = `{"amount":1000}`

	var bodies []string
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		bodies = append(bodies, buf.String())

		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(5)})
	if _, err := c.Do(context.Background(), client.Request{
		Method: http.MethodPost,
		Path:   "/x",
		Body:   json.RawMessage(payload),
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if len(bodies) != 3 {
		t.Fatalf("số lần nhận body = %d, muốn 3", len(bodies))
	}
	for i, got := range bodies {
		if got != payload {
			t.Errorf("lần %d nhận body %q, muốn %q", i+1, got, payload)
		}
	}
}

func TestDo_EncodeBodyJSON(t *testing.T) {
	var got string
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		got = buf.String()
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})
	type req struct {
		Name string `json:"name"`
	}
	if _, err := c.Do(context.Background(), client.Request{
		Method: http.MethodPost, Path: "/x", Body: req{Name: "an"},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if got != `{"name":"an"}` {
		t.Errorf("body = %q", got)
	}
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Content-Type = %q", contentType)
	}
}

// Propagate trace là điều kiện để một trace đi xuyên nhiều service.
func TestDo_PropagateTrace(t *testing.T) {
	var gotTraceparent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1), PropagateTrace: true})

	sc := tracectx.NewRoot()
	ctx := tracectx.WithSpanContext(context.Background(), sc)
	if _, err := c.Do(ctx, client.Request{Path: "/x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if gotTraceparent == "" {
		t.Fatal("không gửi header traceparent")
	}
	if !strings.Contains(gotTraceparent, sc.TraceID) {
		t.Errorf("traceparent = %q, phải chứa trace ID %q", gotTraceparent, sc.TraceID)
	}
	// Phải là span con, không dùng lại span của chặng gọi.
	if strings.Contains(gotTraceparent, sc.SpanID) {
		t.Errorf("traceparent = %q, dùng lại span cha thay vì tạo span con", gotTraceparent)
	}
}

func TestDo_KhongPropagateKhiTat(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})
	ctx := tracectx.WithSpanContext(context.Background(), tracectx.NewRoot())
	if _, err := c.Do(ctx, client.Request{Path: "/x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if got != "" {
		t.Errorf("traceparent = %q, phải rỗng khi PropagateTrace tắt", got)
	}
}

// ---------- Circuit breaker ----------

// Lý do breaker tồn tại: khi đích chết, mỗi request chờ hết timeout mới lỗi, và
// service của mình chết theo dù bản thân không có lỗi gì.
func TestBreaker_MoSauNhieuLoi(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{
		BaseURL: srv.URL,
		Retry:   fastRetry(1),
		Breaker: &client.BreakerConfig{FailureThreshold: 3, OpenDuration: time.Hour},
	})

	for range 3 {
		if _, err := c.Do(context.Background(), client.Request{Path: "/x"}); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	if c.BreakerState() != client.StateOpen {
		t.Fatalf("trạng thái = %v, muốn open sau 3 lần lỗi", c.BreakerState())
	}

	before := calls.Load()
	_, err := c.Do(context.Background(), client.Request{Path: "/x"})
	if !errors.Is(err, client.ErrBreakerOpen) {
		t.Errorf("lỗi = %v, muốn ErrBreakerOpen", err)
	}
	if calls.Load() != before {
		t.Error("breaker mở mà request vẫn đi ra ngoài")
	}
}

// 4xx không được mở breaker: mở vì client gửi sai là nhầm hẳn nguyên nhân.
func TestBreaker_4xxKhongMo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{
		BaseURL: srv.URL,
		Retry:   fastRetry(1),
		Breaker: &client.BreakerConfig{FailureThreshold: 2},
	})

	for range 10 {
		if _, err := c.Do(context.Background(), client.Request{Path: "/x"}); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	if c.BreakerState() != client.StateClosed {
		t.Errorf("trạng thái = %v, 4xx không được mở breaker", c.BreakerState())
	}
}

func TestBreaker_DongLaiSauKhiHoiPhuc(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{
		BaseURL: srv.URL,
		Retry:   fastRetry(1),
		Breaker: &client.BreakerConfig{FailureThreshold: 2, OpenDuration: 20 * time.Millisecond},
	})

	for range 2 {
		_, _ = c.Do(context.Background(), client.Request{Path: "/x"})
	}
	if c.BreakerState() != client.StateOpen {
		t.Fatalf("trạng thái = %v, muốn open", c.BreakerState())
	}

	healthy.Store(true)
	time.Sleep(40 * time.Millisecond)

	if _, err := c.Do(context.Background(), client.Request{Path: "/x"}); err != nil {
		t.Fatalf("Do sau khi hồi phục: %v", err)
	}
	if c.BreakerState() != client.StateClosed {
		t.Errorf("trạng thái = %v, muốn closed sau khi đích hồi phục", c.BreakerState())
	}
}

// Retry lúc breaker mở chỉ làm rỗng ngân sách thời gian.
func TestBreaker_KhongRetryKhiMo(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{
		BaseURL: srv.URL,
		Retry:   fastRetry(5),
		Breaker: &client.BreakerConfig{FailureThreshold: 1, OpenDuration: time.Hour},
	})

	// Lần đầu: 1 request rồi breaker mở, nên các lần retry còn lại bị chặn.
	_, _ = c.Do(context.Background(), client.Request{Path: "/x"})
	if got := calls.Load(); got != 1 {
		t.Errorf("gọi %d lần, muốn 1 — retry vẫn chạy dù breaker đã mở", got)
	}
}

func TestBreakerState_KhongBatBreaker(t *testing.T) {
	c := mustClient(t, client.Config{BaseURL: "http://x", Retry: fastRetry(1)})
	if c.BreakerState() != client.StateClosed {
		t.Errorf("trạng thái = %v", c.BreakerState())
	}
}

// ---------- Log đã che ----------

func TestLog_CheDuLieuNhayCam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token-that-tu-server"}`))
	}))
	defer srv.Close()

	buf := &bytes.Buffer{}
	c := mustClient(t, client.Config{
		BaseURL: srv.URL,
		Retry:   fastRetry(1),
		Logger:  log.New(log.Options{Output: buf}),
	})

	if _, err := c.Do(context.Background(), client.Request{
		Method: http.MethodPost,
		Path:   "/token",
		Body:   map[string]any{"username": "an", "password": "mat-khau-that"},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("không ghi log nào")
	}
	for _, leaked := range []string{"mat-khau-that", "token-that-tu-server"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%q lọt vào log:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "an") {
		t.Errorf("field không nhạy cảm bị che oan:\n%s", out)
	}
}

// URL có mật khẩu trong phần userinfo là chỗ rất dễ bị lộ.
func TestLog_CheMatKhauTrongURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	buf := &bytes.Buffer{}
	base := strings.Replace(srv.URL, "http://", "http://user:mat-khau-trong-url@", 1)
	c := mustClient(t, client.Config{
		BaseURL: base,
		Retry:   fastRetry(1),
		Logger:  log.New(log.Options{Output: buf}),
	})
	_, _ = c.Do(context.Background(), client.Request{Path: "/x"})

	if strings.Contains(buf.String(), "mat-khau-trong-url") {
		t.Fatalf("mật khẩu trong URL lọt vào log:\n%s", buf.String())
	}
}

// ---------- Metrics ----------

func TestMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := prometheus.NewRegistry()
	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1), Metrics: reg})

	if _, err := c.Do(context.Background(), client.Request{Path: "/x"}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}
	for _, want := range []string{"http_client_requests_total", "http_client_request_duration_seconds"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("thiếu metric %q, có %v", want, names)
		}
	}
}

// ---------- Timeout ----------

// Chỉ có timeout mỗi lần thử thì 5 lần retry mỗi lần 10 giây thành 50 giây, vượt xa
// deadline của request đang gọi nó.
func TestTotalTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{
		BaseURL:      srv.URL,
		Timeout:      50 * time.Millisecond,
		TotalTimeout: 150 * time.Millisecond,
		Retry:        retry.Policy{MaxAttempts: 100, BaseDelay: time.Millisecond, Jitter: -1},
	})

	start := time.Now()
	_, err := c.Do(context.Background(), client.Request{Path: "/x"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if elapsed > time.Second {
		t.Errorf("mất %v — TotalTimeout không có tác dụng", elapsed)
	}
}

// ---------- Get / Post / JSON ----------

func TestGetVaPost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"od-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"u-1"}`))
	}))
	defer srv.Close()

	type result struct {
		ID string `json:"id"`
	}
	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})

	got, err := client.Get[result](context.Background(), c, "/users/1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "u-1" {
		t.Errorf("Get = %+v", got)
	}

	posted, err := client.Post[result](context.Background(), c, "/orders", map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if posted.ID != "od-1" {
		t.Errorf("Post = %+v", posted)
	}
}

// Status lỗi thành errs.Error giữ nguyên phân loại của hạ nguồn, nên tầng gọi phân
// biệt được "không tìm thấy" với "dịch vụ đang lỗi" mà không phải đọc status code.
func TestJSON_StatusLoiThanhErrsError(t *testing.T) {
	tests := []struct {
		status   int
		wantCode errs.Code
	}{
		{http.StatusNotFound, errs.CodeNotFound},
		{http.StatusUnauthorized, errs.CodeUnauthorized},
		{http.StatusForbidden, errs.CodeForbidden},
		{http.StatusConflict, errs.CodeConflict},
		{http.StatusTooManyRequests, errs.CodeTooManyReq},
		{http.StatusGatewayTimeout, errs.CodeTimeout},
		{http.StatusServiceUnavailable, errs.CodeUnavailable},
		{http.StatusBadRequest, errs.CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"error":"chi tiết"}`))
			}))
			defer srv.Close()

			c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})
			_, err := client.Get[map[string]any](context.Background(), c, "/x", nil)

			if err == nil {
				t.Fatal("muốn lỗi")
			}
			if !errs.Is(err, tt.wantCode) {
				t.Errorf("lỗi = %v, muốn mã %q", err, tt.wantCode)
			}
		})
	}
}

func TestJSON_BodyRong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})
	got, err := client.Get[map[string]any](context.Background(), c, "/x", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("got = %#v, muốn nil", got)
	}
}

// ---------- Cấu hình ----------

func TestNew_CauHinhSai(t *testing.T) {
	tests := []struct {
		name string
		cfg  client.Config
	}{
		{"BaseURL rác", client.Config{BaseURL: "://khong-hop-le"}},
		{"BaseURL thiếu scheme", client.Config{BaseURL: "example.com/api"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.New(tt.cfg); err == nil {
				t.Error("muốn lỗi")
			}
		})
	}
}

func TestDo_KhongCoBaseURL(t *testing.T) {
	c := mustClient(t, client.Config{Retry: fastRetry(1)})
	if _, err := c.Do(context.Background(), client.Request{Path: "/khong-co-host"}); err == nil {
		t.Error("muốn lỗi khi không có BaseURL và Path là tương đối")
	}
}

func TestDo_URLTuyetDoiKhongCanBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{Retry: fastRetry(1)})
	resp, err := c.Do(context.Background(), client.Request{Path: srv.URL + "/x"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Errorf("Status = %d", resp.Status)
	}
}

func TestDo_ContextDaCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(3)})
	if _, err := c.Do(ctx, client.Request{Path: "/x"}); err == nil {
		t.Error("muốn lỗi khi context đã cancel")
	}
}

func TestDo_HeaderTuKhai(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Partner-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})
	if _, err := c.Do(context.Background(), client.Request{
		Path:   "/x",
		Header: http.Header{"X-Partner-Id": {"p-1"}},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if got != "p-1" {
		t.Errorf("header = %q", got)
	}
}

// Body ở nhiều dạng: []byte và string đi thẳng, io.Reader phải đọc hết vào bộ nhớ
// (retry cần gửi lại), còn lại thì encode JSON.
func TestEncodeBody_CacDang(t *testing.T) {
	var got, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		got = buf.String()
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mustClient(t, client.Config{BaseURL: srv.URL, Retry: fastRetry(1)})

	tests := []struct {
		name       string
		body       any
		wantBody   string
		wantJSONCT bool
	}{
		{"nil", nil, "", false},
		{"bytes", []byte("thô"), "thô", false},
		{"string", "chuỗi", "chuỗi", false},
		{"reader", strings.NewReader("từ reader"), "từ reader", false},
		{"struct thành JSON", map[string]string{"k": "v"}, `{"k":"v"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, contentType = "", ""
			if _, err := c.Do(context.Background(), client.Request{
				Method: http.MethodPost, Path: "/x", Body: tt.body,
			}); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if got != tt.wantBody {
				t.Errorf("body = %q, muốn %q", got, tt.wantBody)
			}
			if isJSON := strings.Contains(contentType, "application/json"); isJSON != tt.wantJSONCT {
				t.Errorf("Content-Type = %q, muốn JSON = %v", contentType, tt.wantJSONCT)
			}
		})
	}
}

func TestBreakerState_String(t *testing.T) {
	tests := map[client.BreakerState]string{
		client.StateClosed:   "closed",
		client.StateOpen:     "open",
		client.StateHalfOpen: "half_open",
	}
	for state, want := range tests {
		if got := state.String(); got != want {
			t.Errorf("%d.String() = %q, muốn %q", state, got, want)
		}
	}
}
