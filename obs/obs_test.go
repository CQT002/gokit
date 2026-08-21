package obs_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cqt002/gokit/obs"
)

// scrape gọi /metrics và trả về nội dung dạng text.
func scrape(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	rec := httptest.NewRecorder()
	obs.Handler(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics trả %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// NewRegistry phải rỗng thật: một hàm tạo "đã có sẵn vài thứ" buộc người dùng phải
// nhớ chính xác nó đã có gì để tránh đăng ký trùng.
//
// Kiểm bằng Gather() chứ không qua scrape: Handler tự đăng ký counter lỗi của
// promhttp vào registry, nên scrape một registry rỗng vẫn ra vài dòng.
func TestNewRegistry_Rong(t *testing.T) {
	got, err := obs.NewRegistry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("registry mới có %d metric family, muốn 0", len(got))
	}
}

// Handler đăng ký counter lỗi của promhttp vào registry, và gọi hai lần không được
// panic vì trùng — dựng lại handler khi reload cấu hình là chuyện thường.
func TestHandler_GoiHaiLanKhongPanic(t *testing.T) {
	reg := obs.NewRegistry()
	_ = obs.Handler(reg)
	_ = obs.Handler(reg)

	if !strings.Contains(scrape(t, reg), "promhttp_metric_handler_errors_total") {
		t.Error("thiếu counter lỗi của promhttp")
	}
}

func TestRegisterRuntime(t *testing.T) {
	for _, detailed := range []bool{false, true} {
		reg := obs.NewRegistry()
		if err := obs.RegisterRuntime(reg, obs.RuntimeOptions{Detailed: detailed}); err != nil {
			t.Fatalf("RegisterRuntime(detailed=%v): %v", detailed, err)
		}

		body := scrape(t, reg)
		for _, want := range []string{"go_goroutines", "go_memstats_", "process_"} {
			if !strings.Contains(body, want) {
				t.Errorf("detailed=%v: thiếu metric %q", detailed, want)
			}
		}
		if detailed && !strings.Contains(body, "go_sched_latencies_seconds") {
			t.Error("detailed=true nhưng thiếu metric scheduler")
		}
	}
}

// Đăng ký hai lần phải trả lỗi chứ không panic: nhiều chỗ cùng dựng registry là
// tình huống thật, và service không nên chết lúc khởi động vì một dòng metric.
func TestRegisterRuntime_HaiLan(t *testing.T) {
	reg := obs.NewRegistry()
	if err := obs.RegisterRuntime(reg, obs.RuntimeOptions{}); err != nil {
		t.Fatalf("lần đầu: %v", err)
	}
	if err := obs.RegisterRuntime(reg, obs.RuntimeOptions{}); err == nil {
		t.Error("lần hai không báo lỗi")
	}
}

// ---------- HTTPMetrics ----------

func TestHTTPMetrics(t *testing.T) {
	reg := obs.NewRegistry()
	mw := obs.HTTPMetrics(reg, obs.HTTPOptions{RoutePattern: obs.ServeMuxRoute})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	srv := mw(mux)

	// Ba request tới cùng route nhưng khác ID: phải gộp vào một series.
	for _, id := range []string{"1", "2", "999999"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders", nil))

	body := scrape(t, reg)
	const wantSeries = `http_server_requests_total{method="GET",route="/users/{id}",status="200"} 3`
	if !strings.Contains(body, wantSeries) {
		t.Errorf("không thấy %q — ba ID khác nhau phải gộp cùng một series:\n%s", wantSeries, body)
	}

	// Đây là khẳng định quan trọng nhất của cả package: ID thật không được thành label.
	for _, id := range []string{"999999", "/users/1", "/users/2"} {
		if strings.Contains(body, id) {
			t.Errorf("path thật %q xuất hiện trong label — nổ cardinality:\n%s", id, body)
		}
	}
	if !strings.Contains(body, `route="/orders"`) {
		t.Errorf("thiếu series cho /orders:\n%s", body)
	}
	if !strings.Contains(body, "http_server_request_duration_seconds_bucket") {
		t.Error("thiếu histogram thời gian xử lý")
	}
	if !strings.Contains(body, "http_server_requests_in_flight 0") {
		t.Errorf("in_flight phải về 0 sau khi xong:\n%s", body)
	}
}

// Không xác định được route thì phải gộp vào nhãn cố định. Đây là đường mà một
// scanner quét vài nghìn URL lạ sẽ đi qua.
func TestHTTPMetrics_RouteKhongXacDinh(t *testing.T) {
	tests := []struct {
		name string
		opts obs.HTTPOptions
	}{
		{"RoutePattern nil", obs.HTTPOptions{}},
		{"RoutePattern trả rỗng", obs.HTTPOptions{RoutePattern: func(*http.Request) string { return "" }}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := obs.NewRegistry()
			srv := obs.HTTPMetrics(reg, tt.opts)(http.NotFoundHandler())

			for _, p := range []string{"/khong/co/1", "/khong/co/2", "/wp-admin.php"} {
				srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
			}

			body := scrape(t, reg)
			if !strings.Contains(body, `route="unknown"`) {
				t.Errorf("thiếu nhãn unknown:\n%s", body)
			}
			for _, p := range []string{"khong/co", "wp-admin"} {
				if strings.Contains(body, p) {
					t.Errorf("path lạ %q lọt vào label:\n%s", p, body)
				}
			}
		})
	}
}

func TestHTTPMetrics_StatusMacDinh(t *testing.T) {
	reg := obs.NewRegistry()

	// Handler chỉ ghi body, không gọi WriteHeader: net/http hiểu là 200.
	srv := obs.HTTPMetrics(reg, obs.HTTPOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("xong"))
		}))
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(scrape(t, reg), `status="200"`) {
		t.Error("handler không gọi WriteHeader phải được tính là 200")
	}
}

func TestHTTPMetrics_GhiNhanStatusDauTien(t *testing.T) {
	reg := obs.NewRegistry()
	srv := obs.HTTPMetrics(reg, obs.HTTPOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.WriteHeader(http.StatusOK) // net/http bỏ qua lần thứ hai
		}))
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(scrape(t, reg), `status="404"`) {
		t.Error("phải ghi status đầu tiên, vì đó là status thật đi ra dây")
	}
}

// Request panic là loại request cần thấy nhất trên dashboard, nên metric phải được
// ghi trong defer.
func TestHTTPMetrics_HandlerPanic(t *testing.T) {
	reg := obs.NewRegistry()
	srv := obs.HTTPMetrics(reg, obs.HTTPOptions{})(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic("bùm") }))

	func() {
		defer func() { _ = recover() }()
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()

	body := scrape(t, reg)
	if !strings.Contains(body, "http_server_requests_total") {
		t.Errorf("mất metric của request panic:\n%s", body)
	}
	if !strings.Contains(body, "http_server_requests_in_flight 0") {
		t.Errorf("in_flight bị rò sau khi panic:\n%s", body)
	}
}

func TestHTTPMetrics_Namespace(t *testing.T) {
	reg := obs.NewRegistry()
	srv := obs.HTTPMetrics(reg, obs.HTTPOptions{Namespace: "myapp"})(http.NotFoundHandler())
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if !strings.Contains(scrape(t, reg), "myapp_http_server_requests_total") {
		t.Error("Namespace không được áp vào tên metric")
	}
}

// ResponseController phải xuyên qua được, nếu không thì SSE và streaming vỡ.
func TestHTTPMetrics_FlushVanDungDuoc(t *testing.T) {
	reg := obs.NewRegistry()
	srv := obs.HTTPMetrics(reg, obs.HTTPOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("một phần"))
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("Flush: %v", err)
			}
		}))
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// ---------- RegisterDBStats ----------

func TestRegisterDBStats(t *testing.T) {
	stats := sql.DBStats{
		MaxOpenConnections: 25,
		OpenConnections:    10,
		InUse:              7,
		Idle:               3,
		WaitCount:          42,
		WaitDuration:       1500 * time.Millisecond,
		MaxIdleClosed:      5,
		MaxIdleTimeClosed:  2,
		MaxLifetimeClosed:  1,
	}

	reg := obs.NewRegistry()
	if err := obs.RegisterDBStats(reg, "primary", func() sql.DBStats { return stats }); err != nil {
		t.Fatalf("RegisterDBStats: %v", err)
	}

	body := scrape(t, reg)
	wants := []string{
		`db_pool_connections{name="primary",state="open"} 10`,
		`db_pool_connections{name="primary",state="in_use"} 7`,
		`db_pool_connections{name="primary",state="idle"} 3`,
		`db_pool_connections{name="primary",state="max_open"} 25`,
		`db_pool_waits_total{name="primary"} 42`,
		`db_pool_wait_duration_seconds_total{name="primary"} 1.5`,
		`db_pool_closed_total{name="primary",reason="max_idle"} 5`,
		`db_pool_closed_total{name="primary",reason="max_idle_time"} 2`,
		`db_pool_closed_total{name="primary",reason="max_lifetime"} 1`,
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("thiếu %q trong:\n%s", want, body)
		}
	}
}

// Số liệu phải đọc tại thời điểm scrape, không phải chốt lúc đăng ký.
func TestRegisterDBStats_DocLucScrape(t *testing.T) {
	var open int
	reg := obs.NewRegistry()
	if err := obs.RegisterDBStats(reg, "db", func() sql.DBStats {
		open++
		return sql.DBStats{OpenConnections: open}
	}); err != nil {
		t.Fatalf("RegisterDBStats: %v", err)
	}

	if !strings.Contains(scrape(t, reg), `state="open"} 1`) {
		t.Error("lần scrape đầu không đọc được giá trị")
	}
	if !strings.Contains(scrape(t, reg), `state="open"} 2`) {
		t.Error("lần scrape thứ hai vẫn ra giá trị cũ — số liệu bị chốt lúc đăng ký")
	}
}

func TestRegisterDBStats_NhieuDatabase(t *testing.T) {
	reg := obs.NewRegistry()
	for _, name := range []string{"primary", "replica"} {
		if err := obs.RegisterDBStats(reg, name, func() sql.DBStats {
			return sql.DBStats{OpenConnections: 1}
		}); err != nil {
			t.Fatalf("RegisterDBStats(%q): %v", name, err)
		}
	}

	body := scrape(t, reg)
	for _, name := range []string{"primary", "replica"} {
		if !strings.Contains(body, `name="`+name+`"`) {
			t.Errorf("thiếu database %q:\n%s", name, body)
		}
	}
}
