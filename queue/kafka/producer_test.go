package kafka_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cqt002/gokit/queue/kafka"
)

func TestSend_NhieuMessage(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	msgs := []kafka.Message{
		{Topic: "t", Key: "a", Value: []byte("1")},
		{Topic: "t", Key: "b", Value: []byte("2")},
		{Topic: "t", Key: "c", Value: []byte("3")},
	}
	if err := p.Send(t.Context(), msgs...); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 3)
	if len(recs) != 3 {
		t.Fatalf("đọc được %d message", len(recs))
	}
}

func TestSend_KhongCoMessage(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	if err := p.Send(t.Context()); err != nil {
		t.Errorf("Send() rỗng: %v", err)
	}
}

func TestSend_ThieuTopic(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("1")}, kafka.Message{Value: []byte("2")})
	if err == nil {
		t.Fatal("message thiếu Topic mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "thứ 1") {
		t.Errorf("lỗi không chỉ ra message nào: %v", err)
	}
}

func TestSendJSON(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	type order struct {
		ID     string `json:"id"`
		Amount int    `json:"amount"`
	}
	want := order{ID: "od-1", Amount: 1000}

	if err := p.SendJSON(t.Context(), "t", "od-1", want); err != nil {
		t.Fatalf("SendJSON: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	var got order
	if err := json.Unmarshal(recs[0].Value, &got); err != nil {
		t.Fatalf("giải mã: %v", err)
	}
	if got != want {
		t.Errorf("got = %+v, muốn %+v", got, want)
	}
	// Header content-type để phía nhận biết cách giải mã mà không phải thoả
	// thuận ngầm.
	if ct := header(recs[0], "content-type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if string(recs[0].Key) != "od-1" {
		t.Errorf("Key = %q", recs[0].Key)
	}
}

func TestSendJSON_KhongMaHoaDuoc(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	err := p.SendJSON(t.Context(), "t", "k", make(chan int))
	if err == nil {
		t.Fatal("giá trị không mã hoá được mà không báo lỗi")
	}
}

// Chế độ async trả nil ngay; message chỉ chắc chắn đã gửi sau khi Flush.
func TestSend_AsyncCanFlush(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.Async = true })

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := p.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	if string(recs[0].Value) != "v" {
		t.Errorf("Value = %q", recs[0].Value)
	}
}

// Ở chế độ async, lỗi gửi chỉ xuất hiện trong log — nên dòng log đó là thứ duy
// nhất giữ message khỏi biến mất trong im lặng.
func TestSend_AsyncLoiChiHienTrongLog(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	log, buf := captureLogger()

	p, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers:    addrs,
		Async:      true,
		Logger:     log,
		ClientOpts: []kgoOpt{},
	})
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer p.Close()

	// Topic không tồn tại và broker giả không tự tạo topic.
	if err := p.Send(t.Context(), kafka.Message{Topic: "khong-ton-tai", Value: []byte("v")}); err != nil {
		t.Fatalf("Send async phải trả nil ngay: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = p.Flush(ctx)

	waitFor(t, 15*time.Second, "có dòng log lỗi gửi", func() bool {
		return buf.hasLog("ERROR", "gửi message Kafka thất bại")
	})
}

func TestPing(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	if err := p.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPing_BrokerKhongCo(t *testing.T) {
	p := newProducer(t, []string{"127.0.0.1:1"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := p.Ping(ctx); err == nil {
		t.Fatal("Ping thành công dù không có broker nào")
	}
}

func TestProducer_Client(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	if p.Client() == nil {
		t.Error("Client() trả nil")
	}
}

func TestProducer_Metrics(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	reg := prometheus.NewRegistry()
	p := newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.Metrics = reg })

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	names := metricNames(t, reg)
	if !names["kafka_producer_messages_total"] {
		t.Errorf("thiếu metric của package, có: %v", keys(names))
	}
	// kprom đo phần package này không thấy: byte đi qua dây, số lần kết nối.
	if !hasPrefix(names, "kafka_producer_") {
		t.Errorf("thiếu metric của kprom, có: %v", keys(names))
	}
}

// Producer và consumer trong cùng một process phải đăng ký được vào cùng một
// registry — kprom dùng chung tên metric nên namespace phải tách theo vai trò.
func TestMetrics_ProducerVaConsumerCungRegistry(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	reg := prometheus.NewRegistry()

	newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.Metrics = reg })
	newConsumer(t, addrs, "g", []string{"t"}, func(c *kafka.ConsumerConfig) { c.Metrics = reg })
}
