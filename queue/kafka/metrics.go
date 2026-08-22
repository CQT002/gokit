package kafka

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kprom"
)

// Namespace là tiền tố của mọi metric package này đăng ký.
const Namespace = "kafka"

// Kết quả xử lý một message, dùng làm nhãn `result`.
const (
	resultOK    = "ok"
	resultDLQ   = "dlq"
	resultError = "error"
)

// clientMetricsOpt dựng hook kprom cho metric ở tầng client.
//
// kprom đo phần mà package này không thấy được: số byte đọc/ghi, số lần kết nối
// lại, độ trễ của request tới broker, lỗi theo từng broker. Đó là nhóm trả lời
// "vấn đề nằm ở mạng hay ở code".
//
// reg nil nghĩa là không đo gì — trả về nil opt.
func clientMetricsOpt(reg *prometheus.Registry, role string) kgo.Opt {
	if reg == nil {
		return nil
	}
	// Namespace tách theo vai trò: producer và consumer trong cùng một process
	// đăng ký cùng bộ metric của kprom, và trùng tên là lỗi lúc đăng ký.
	m := kprom.NewMetrics(Namespace+"_"+role, kprom.Registry(reg))
	return kgo.WithHooks(m)
}

// consumerMetrics là các metric về **kết quả xử lý**, thứ kprom không biết.
//
// kprom đo được message đã lấy về, nhưng không đo được message đã xử lý xong,
// đã thử lại bao nhiêu lần, hay đã rơi vào DLQ. Ba con số đó mới là thứ người
// vận hành cần: DLQ tăng nghĩa là có dữ liệu đang không được xử lý.
type consumerMetrics struct {
	messages *prometheus.CounterVec
	retries  *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// newConsumerMetrics đăng ký metric của consumer. reg nil trả về nil.
func newConsumerMetrics(reg *prometheus.Registry, group string) (*consumerMetrics, error) {
	if reg == nil {
		return nil, nil
	}

	labels := prometheus.Labels{"group": group}
	m := &consumerMetrics{
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   "consumer",
			Name:        "messages_total",
			Help:        "Số message đã xử lý, chia theo topic và kết quả cuối cùng.",
			ConstLabels: labels,
		}, []string{"topic", "result"}),

		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   Namespace,
			Subsystem:   "consumer",
			Name:        "retries_total",
			Help:        "Số lần thử lại handler.",
			ConstLabels: labels,
		}, []string{"topic"}),

		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:   Namespace,
			Subsystem:   "consumer",
			Name:        "handler_duration_seconds",
			Help:        "Thời gian handler xử lý một message, tính cả các lần thử lại.",
			ConstLabels: labels,
			Buckets:     prometheus.DefBuckets,
		}, []string{"topic"}),
	}

	for _, c := range []prometheus.Collector{m.messages, m.retries, m.duration} {
		if err := reg.Register(c); err != nil {
			return nil, fmt.Errorf("kafka: đăng ký metric consumer: %w", err)
		}
	}
	return m, nil
}

// observe ghi kết quả xử lý một message. Nhận con trỏ nil được.
func (m *consumerMetrics) observe(topic, result string, seconds float64, retries int) {
	if m == nil {
		return
	}
	m.messages.WithLabelValues(topic, result).Inc()
	m.duration.WithLabelValues(topic).Observe(seconds)
	if retries > 0 {
		m.retries.WithLabelValues(topic).Add(float64(retries))
	}
}

// producerMetrics đếm kết quả gửi.
//
// Cần riêng với kprom vì ở chế độ async, lỗi gửi chỉ xuất hiện trong callback —
// không có chỗ nào khác trong code của app nhìn thấy nó.
type producerMetrics struct {
	messages *prometheus.CounterVec
}

// newProducerMetrics đăng ký metric của producer. reg nil trả về nil.
func newProducerMetrics(reg *prometheus.Registry) (*producerMetrics, error) {
	if reg == nil {
		return nil, nil
	}

	m := &producerMetrics{
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Subsystem: "producer",
			Name:      "messages_total",
			Help:      "Số message đã gửi, chia theo topic và kết quả.",
		}, []string{"topic", "result"}),
	}

	if err := reg.Register(m.messages); err != nil {
		return nil, fmt.Errorf("kafka: đăng ký metric producer: %w", err)
	}
	return m, nil
}

// observe ghi kết quả gửi một message. Nhận con trỏ nil được.
func (m *producerMetrics) observe(topic, result string) {
	if m == nil {
		return
	}
	m.messages.WithLabelValues(topic, result).Inc()
}
