package obs

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
)

// RegisterDBStats đăng ký metric cho connection pool của một database.
//
// Metric đăng ký vào reg:
//
//	db_pool_connections{name,state}     state = open | in_use | idle | max_open
//	db_pool_waits_total{name}
//	db_pool_wait_duration_seconds_total{name}
//	db_pool_closed_total{name,reason}   reason = max_idle | max_idle_time | max_lifetime
//
// name để phân biệt khi service nối tới nhiều database (ví dụ "primary", "replica").
//
// fn được gọi mỗi lần Prometheus scrape, nên nó phải nhanh và an toàn khi gọi từ
// nhiều goroutine — (*sql.DB).Stats đáp ứng cả hai.
//
// Đây là nhóm metric trả lời được câu hỏi hay gặp nhất khi service chậm mà CPU thấp:
// pool đã cạn chưa. WaitCount tăng nghĩa là có goroutine đang xếp hàng chờ
// connection, và lúc đó tăng MaxOpenConns hiệu quả hơn mọi việc tối ưu câu query.
func RegisterDBStats(reg *prometheus.Registry, name string, fn func() sql.DBStats) error {
	return reg.Register(newDBStatsCollector(name, fn))
}

// dbStatsCollector đọc sql.DBStats tại thời điểm scrape.
//
// Cài đặt prometheus.Collector chứ không dùng GaugeFunc cho từng số liệu: một lần
// gọi Stats() lấy được toàn bộ, còn dùng GaugeFunc riêng lẻ sẽ gọi Stats() một lần
// cho mỗi metric và các số liệu trong cùng một lần scrape có thể lệch nhau.
type dbStatsCollector struct {
	stats func() sql.DBStats

	connections *prometheus.Desc
	waits       *prometheus.Desc
	waitSeconds *prometheus.Desc
	closed      *prometheus.Desc
}

// newDBStatsCollector dựng Desc với name là **constant label**, không phải variable
// label.
//
// Đây là điểm bắt buộc để đăng ký được nhiều database vào cùng một registry:
// Prometheus so trùng collector theo tập Desc, và hai collector có Desc giống hệt
// nhau bị coi là trùng kể cả khi giá trị label khác. Đưa name vào constant label
// làm Desc của mỗi database khác nhau, còn output thì y như cũ.
func newDBStatsCollector(name string, fn func() sql.DBStats) *dbStatsCollector {
	labels := prometheus.Labels{"name": name}
	return &dbStatsCollector{
		stats: fn,
		connections: prometheus.NewDesc(
			"db_pool_connections",
			"Số connection trong pool, chia theo trạng thái.",
			[]string{"state"}, labels,
		),
		waits: prometheus.NewDesc(
			"db_pool_waits_total",
			"Tổng số lần phải chờ để lấy được connection.",
			nil, labels,
		),
		waitSeconds: prometheus.NewDesc(
			"db_pool_wait_duration_seconds_total",
			"Tổng thời gian đã chờ để lấy connection, tính theo giây.",
			nil, labels,
		),
		closed: prometheus.NewDesc(
			"db_pool_closed_total",
			"Tổng số connection đã bị đóng, chia theo lý do.",
			[]string{"reason"}, labels,
		),
	}
}

func (c *dbStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connections
	ch <- c.waits
	ch <- c.waitSeconds
	ch <- c.closed
}

func (c *dbStatsCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()

	gauge := func(desc *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, labels...)
	}
	counter := func(desc *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, v, labels...)
	}

	gauge(c.connections, float64(s.OpenConnections), "open")
	gauge(c.connections, float64(s.InUse), "in_use")
	gauge(c.connections, float64(s.Idle), "idle")
	gauge(c.connections, float64(s.MaxOpenConnections), "max_open")

	counter(c.waits, float64(s.WaitCount))
	counter(c.waitSeconds, s.WaitDuration.Seconds())
	counter(c.closed, float64(s.MaxIdleClosed), "max_idle")
	counter(c.closed, float64(s.MaxIdleTimeClosed), "max_idle_time")
	counter(c.closed, float64(s.MaxLifetimeClosed), "max_lifetime")
}
