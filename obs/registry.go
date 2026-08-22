// Package obs cung cấp metrics Prometheus cho HTTP server, connection pool của
// database, và runtime của Go.
//
// Module này chỉ phụ thuộc prometheus/client_golang và stdlib — **không** phụ
// thuộc core. Nhờ vậy mọi module khác trong gokit import được nó mà không sợ chu
// trình phụ thuộc.
//
// Không có registry toàn cục. prometheus.DefaultRegisterer là state toàn cục và nó
// gây ra đúng hai vấn đề: hai test chạy cùng lúc đăng ký trùng metric rồi panic, và
// không có cách nào chạy hai instance trong một process. Registry ở đây truyền tay.
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry tạo registry rỗng.
//
// Rỗng thật, không có collector nào — kể cả metric của process và runtime. Gọi
// RegisterRuntime nếu muốn chúng. Lý do không gộp: đăng ký sẵn rồi lại đăng ký lần
// nữa là trùng collector và Prometheus trả lỗi, nên một hàm tạo "đã có sẵn vài thứ"
// buộc người dùng phải nhớ chính xác nó đã có gì.
func NewRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// Handler trả về http.Handler cho endpoint /metrics.
//
// Lỗi khi thu thập metric được trả về dưới dạng HTTP 500 kèm nội dung lỗi, thay vì
// im lặng bỏ qua: một metric hỏng mà endpoint vẫn trả 200 nghĩa là dashboard hiển
// thị số liệu thiếu và không ai biết.
//
// Hàm này **đăng ký thêm** promhttp_metric_handler_errors_total vào reg — chính là
// để cái lỗi ở trên đếm được. Gọi nhiều lần trên cùng registry là an toàn.
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
		Registry:      reg,
	})
}

// RuntimeOptions cấu hình RegisterRuntime.
type RuntimeOptions struct {
	// Detailed bật thêm nhóm metric /sched/latencies và /gc/... từ runtime/metrics.
	//
	// Hữu ích khi điều tra độ trễ do GC hoặc do scheduler, nhưng thêm khá nhiều
	// series nên để chỗ gọi tự quyết.
	Detailed bool
}

// RegisterRuntime đăng ký metric của process (file descriptor, CPU, RSS) và của Go
// runtime (số goroutine, thống kê GC, bộ nhớ).
//
// Trả về lỗi thay vì panic — đặc tả ở plan không có giá trị trả về, nhưng đăng ký
// trùng là lỗi rất dễ mắc khi có nhiều chỗ cùng dựng registry, và biến nó thành
// panic nghĩa là service chết lúc khởi động vì một dòng metric.
func RegisterRuntime(reg *prometheus.Registry, opts RuntimeOptions) error {
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return err
	}

	// Không gom option vào slice rồi truyền variadic: type của option là type
	// internal của client_golang, không đặt tên được từ ngoài package.
	goCollector := collectors.NewGoCollector()
	if opts.Detailed {
		goCollector = collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(
			collectors.MetricsScheduler,
			collectors.MetricsGC,
		))
	}
	return reg.Register(goCollector)
}
