package obs

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// unknownRoute là nhãn dùng khi không xác định được route pattern.
//
// Gộp mọi request lạ vào một nhãn cố định chứ không dùng path thật: một scanner
// quét vài nghìn URL không tồn tại sẽ sinh ra vài nghìn series và làm Prometheus
// hết bộ nhớ.
const unknownRoute = "unknown"

// HTTPOptions cấu hình HTTPMetrics.
type HTTPOptions struct {
	// RoutePattern trả về **route pattern** của request, ví dụ "/users/{id}".
	//
	// Đây là field quan trọng nhất của cả package. Dùng path thật làm label sẽ
	// sinh một series cho mỗi ID khác nhau — vài triệu series cho một endpoint, và
	// Prometheus sẽ chết vì hết bộ nhớ. Prometheus gọi vấn đề này là nổ cardinality.
	//
	// Với ServeMux của Go 1.22 trở lên thì dùng ServeMuxRoute — đừng dùng thẳng
	// r.Pattern, vì nó gồm cả method ("GET /users/{id}") nên label route sẽ lặp
	// lại thông tin đã có ở label method:
	//
	//	RoutePattern: obs.ServeMuxRoute
	//
	// Với chi:
	//
	//	RoutePattern: func(r *http.Request) string { return chi.RouteContext(r.Context()).RoutePattern() }
	//
	// nil hoặc trả về chuỗi rỗng thì dùng nhãn "unknown". Cố tình không mặc định
	// về r.URL.Path: mặc định an toàn quan trọng hơn mặc định tiện.
	RoutePattern func(*http.Request) string

	// Buckets là mốc chia của histogram thời gian xử lý, tính theo giây.
	// nil thì dùng DefaultDurationBuckets.
	Buckets []float64

	// Namespace là tiền tố tên metric, ví dụ "myapp". Rỗng thì không có tiền tố.
	Namespace string
}

// DefaultDurationBuckets là mốc chia mặc định cho histogram thời gian xử lý.
//
// Dày ở khoảng 5ms–1s vì đó là vùng mà API nội bộ thực sự nằm, và có mốc tới 10s
// để thấy được phần đuôi thay vì dồn hết vào +Inf.
var DefaultDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// HTTPMetrics trả về middleware ghi metric cho mọi request đi qua.
//
// Metric đăng ký vào reg:
//
//	http_server_requests_total{method,route,status}
//	http_server_request_duration_seconds{method,route}   (histogram)
//	http_server_requests_in_flight
//
// Panic nếu đăng ký metric thất bại: chuyện đó chỉ xảy ra khi cùng một registry
// được gắn hai lần, tức là lỗi khi dựng app, và phát hiện lúc khởi động rẻ hơn
// nhiều so với việc chạy tiếp với metric thiếu.
func HTTPMetrics(reg *prometheus.Registry, opts HTTPOptions) func(http.Handler) http.Handler {
	buckets := opts.Buckets
	if buckets == nil {
		buckets = DefaultDurationBuckets
	}

	total := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: opts.Namespace,
		Name:      "http_server_requests_total",
		Help:      "Tổng số request HTTP đã xử lý.",
	}, []string{"method", "route", "status"})

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: opts.Namespace,
		Name:      "http_server_request_duration_seconds",
		Help:      "Thời gian xử lý request HTTP, tính theo giây.",
		Buckets:   buckets,
	}, []string{"method", "route"})

	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: opts.Namespace,
		Name:      "http_server_requests_in_flight",
		Help:      "Số request HTTP đang được xử lý.",
	})

	reg.MustRegister(total, duration, inFlight)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inFlight.Inc()
			defer inFlight.Dec()

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			// Đo cả khi handler panic: recover middleware nằm ngoài sẽ bắt panic,
			// và nếu không đo trong defer thì mọi request panic đều mất metric —
			// đúng những request cần thấy nhất.
			defer func() {
				route := routeOf(r, opts.RoutePattern)
				elapsed := time.Since(start).Seconds()

				total.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
				duration.WithLabelValues(r.Method, route).Observe(elapsed)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

// routeOf lấy route pattern, và luôn trả về một nhãn có giới hạn.
//
// Route pattern chỉ biết được **sau** khi router đã khớp, nên hàm này phải được
// gọi sau khi handler chạy xong, không phải trước.
func routeOf(r *http.Request, fn func(*http.Request) string) string {
	if fn == nil {
		return unknownRoute
	}
	if route := fn(r); route != "" {
		return route
	}
	return unknownRoute
}

// statusRecorder ghi lại status code để làm label.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.wroteHeader {
		// net/http đã cảnh báo "superfluous WriteHeader"; ở đây chỉ cần giữ status
		// đầu tiên, vì đó là status thật sự đi ra dây.
		return
	}
	s.wroteHeader = true
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// Handler ghi body mà không gọi WriteHeader thì net/http hiểu là 200; phải ghi
	// nhận điều đó ở đây, nếu không status mặc định sẽ sai với mọi handler kiểu này.
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap cho phép http.ResponseController tìm tới ResponseWriter gốc, nhờ vậy
// Flush và Hijack vẫn dùng được qua middleware này.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// ServeMuxRoute lấy route pattern từ http.ServeMux của Go 1.22 trở lên, đã bỏ phần
// method.
//
//	obs.HTTPMetrics(reg, obs.HTTPOptions{RoutePattern: obs.ServeMuxRoute})
//
// r.Pattern của ServeMux có dạng "GET /users/{id}" — cả method lẫn path. Dùng thẳng
// nó làm label route sẽ nhân đôi thông tin đã có ở label method, và làm hai series
// "GET /x" với "POST /x" không gộp được khi cần xem tổng lưu lượng của một path.
func ServeMuxRoute(r *http.Request) string {
	pattern := r.Pattern
	// Phần method nằm trước dấu cách đầu tiên, và chỉ có khi pattern khai method.
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = pattern[i+1:]
	}
	return strings.TrimSpace(pattern)
}
