package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cqt002/gokit/queue/kafka"
)

func TestRun_XuLyMessage(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"})

	for i := range 3 {
		if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte(strconv.Itoa(i))}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	var mu sync.Mutex
	var seen []string
	stop, _ := runConsumer(t, c, func(_ context.Context, m kafka.Message) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, string(m.Value))
		return nil
	})
	defer stop()

	waitFor(t, 20*time.Second, "nhận đủ 3 message", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 3
	})
}

func TestRun_ThieuHandler(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	c := newConsumer(t, addrs, "g", []string{"t"})

	if err := c.Run(t.Context(), nil); err == nil {
		t.Fatal("handler nil mà Run không báo lỗi")
	}
}

// Hai vòng lặp cùng poll một client sẽ chia nhau message theo cách không ai kiểm
// soát được, và commit của bên này ghi đè tiến độ của bên kia.
func TestRun_GoiHaiLan(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	c := newConsumer(t, addrs, "g", []string{"t"})

	stop, _ := runConsumer(t, c, func(context.Context, kafka.Message) error { return nil })
	defer stop()

	// Run đặt cờ ngay dòng đầu, trước cả lần poll đầu tiên, nên một nhịp ngắn là
	// đủ. Không thử lại trong vòng lặp: lần gọi thứ hai mà thắng cờ sẽ chiếm chỗ
	// của lần thứ nhất, và test tự tạo ra đúng cái nó đang muốn phát hiện.
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.Run(ctx, func(context.Context, kafka.Message) error { return nil })
	if err == nil {
		t.Fatal("Run lần hai chạy được trên cùng một Consumer")
	}
	if !strings.Contains(err.Error(), "đã đang chạy") {
		t.Errorf("err = %v, muốn báo Run đã chạy", err)
	}
}

// Offset chỉ được commit sau khi handler trả nil. Kiểm bằng cách dựng consumer
// mới cùng group: nó không được nhận lại message cũ.
func TestRun_CommitOffset(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	for i := range 3 {
		if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte(strconv.Itoa(i))}); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	first := newConsumer(t, addrs, "g", []string{"t"})
	var count atomic.Int32
	stop, _ := runConsumer(t, first, func(context.Context, kafka.Message) error {
		count.Add(1)
		return nil
	})
	waitFor(t, 20*time.Second, "consumer đầu nhận đủ 3 message", func() bool {
		return count.Load() == 3
	})
	stop()
	first.Close()

	// Message thứ tư, sau khi consumer đầu đã dừng.
	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("moi")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	second := newConsumer(t, addrs, "g", []string{"t"})
	var mu sync.Mutex
	var seen []string
	stop2, _ := runConsumer(t, second, func(_ context.Context, m kafka.Message) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, string(m.Value))
		return nil
	})
	defer stop2()

	waitFor(t, 20*time.Second, "consumer thứ hai nhận message mới", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) >= 1
	})
	// Cho thêm một nhịp để lộ ra nếu có message cũ bị gửi lại.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "moi" {
		t.Errorf("nhận %v — message đã xử lý bị gửi lại, tức là offset chưa được commit", seen)
	}
}

// Hết lượt thử lại thì message vào DLQ kèm đủ thông tin để tìm lại bản gốc.
func TestRun_DLQ(t *testing.T) {
	addrs := newCluster(t, 1, "t", "t-dlq")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.DLQTopic = "t-dlq"
		cfg.MaxRetries = 2
		cfg.RetryBackoff.BaseDelay = time.Millisecond
	})

	err := p.Send(t.Context(), kafka.Message{
		Topic:   "t",
		Key:     "k-1",
		Value:   []byte("hong"),
		Headers: map[string]string{"a": "1"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var attempts atomic.Int32
	stop, errCh := runConsumer(t, c, func(context.Context, kafka.Message) error {
		attempts.Add(1)
		return errors.New("khong xu ly duoc")
	})
	defer stop()

	recs := readAll(t, addrs, "t-dlq", 1)
	dlq := recs[0]

	if got := attempts.Load(); got != 3 {
		t.Errorf("handler được gọi %d lần, muốn 3 (1 lần đầu + 2 lần thử lại)", got)
	}
	if string(dlq.Value) != "hong" {
		t.Errorf("Value = %q", dlq.Value)
	}
	// Giữ Key để bản ghi DLQ vào cùng partition với bản gốc.
	if string(dlq.Key) != "k-1" {
		t.Errorf("Key = %q", dlq.Key)
	}
	// Header gốc phải còn.
	if header(dlq, "a") != "1" {
		t.Errorf("header gốc bị mất: %v", dlq.Headers)
	}

	if got := header(dlq, kafka.HeaderDLQTopic); got != "t" {
		t.Errorf("%s = %q", kafka.HeaderDLQTopic, got)
	}
	if got := header(dlq, kafka.HeaderDLQPartition); got != "0" {
		t.Errorf("%s = %q", kafka.HeaderDLQPartition, got)
	}
	if got := header(dlq, kafka.HeaderDLQOffset); got != "0" {
		t.Errorf("%s = %q", kafka.HeaderDLQOffset, got)
	}
	if got := header(dlq, kafka.HeaderDLQGroup); got != "g" {
		t.Errorf("%s = %q", kafka.HeaderDLQGroup, got)
	}
	if got := header(dlq, kafka.HeaderDLQError); !strings.Contains(got, "khong xu ly duoc") {
		t.Errorf("%s = %q", kafka.HeaderDLQError, got)
	}
	if got := header(dlq, kafka.HeaderDLQTime); got == "" {
		t.Errorf("thiếu %s", kafka.HeaderDLQTime)
	}

	// Vào DLQ xong thì consumer đi tiếp, không dừng.
	select {
	case err := <-errCh:
		t.Fatalf("Run dừng dù đã có DLQ: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}

// Không khai DLQTopic thì một message hỏng làm Run trả lỗi và service dừng —
// hai lựa chọn còn lại (bỏ qua trong im lặng, thử lại mãi) đều tệ hơn.
func TestRun_KhongCoDLQThiDung(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.MaxRetries = 1
		cfg.RetryBackoff.BaseDelay = time.Millisecond
	})

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("hong")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, errCh := runConsumer(t, c, func(context.Context, kafka.Message) error {
		return errors.New("khong xu ly duoc")
	})

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Run trả nil dù message không xử lý được và không có DLQ")
		}
		if !strings.Contains(err.Error(), "DLQTopic") {
			t.Errorf("lỗi không chỉ ra cách khắc phục: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run không dừng")
	}
}

// Panic trong handler không được làm chết process: nó thành error như mọi lỗi
// khác, và đi vào DLQ.
func TestRun_HandlerPanic(t *testing.T) {
	addrs := newCluster(t, 1, "t", "t-dlq")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.DLQTopic = "t-dlq"
		cfg.MaxRetries = -1
	})

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("no")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop, _ := runConsumer(t, c, func(context.Context, kafka.Message) error {
		panic("no tung")
	})
	defer stop()

	recs := readAll(t, addrs, "t-dlq", 1)
	if got := header(recs[0], kafka.HeaderDLQError); !strings.Contains(got, "panic") {
		t.Errorf("%s = %q, muốn nhắc panic", kafka.HeaderDLQError, got)
	}
}

// MaxRetries < 0 nghĩa là không thử lại: đúng một lần gọi.
func TestRun_KhongThuLai(t *testing.T) {
	addrs := newCluster(t, 1, "t", "t-dlq")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.DLQTopic = "t-dlq"
		cfg.MaxRetries = -1
	})

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("hong")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var attempts atomic.Int32
	stop, _ := runConsumer(t, c, func(context.Context, kafka.Message) error {
		attempts.Add(1)
		return errors.New("hong")
	})
	defer stop()

	readAll(t, addrs, "t-dlq", 1)
	if got := attempts.Load(); got != 1 {
		t.Errorf("handler được gọi %d lần, muốn 1", got)
	}
}

// Đẩy vào DLQ thất bại thì **không** commit: commit lúc đó là mất hẳn message.
func TestRun_DLQKhongGhiDuoc(t *testing.T) {
	addrs := newCluster(t, 1, "t") // topic DLQ không tồn tại
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.DLQTopic = "khong-ton-tai"
		cfg.MaxRetries = -1
	})

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("hong")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, errCh := runConsumer(t, c, func(context.Context, kafka.Message) error {
		return errors.New("hong")
	})

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Run trả nil dù không đẩy được vào DLQ")
		}
		if !strings.Contains(err.Error(), "DLQ") {
			t.Errorf("lỗi không nói rõ nguyên nhân: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run không dừng")
	}
}

// Handler trả nil nghĩa là "đã xử lý xong", kể cả khi kết luận là message không
// dùng được — không có DLQ, không có thử lại.
func TestRun_HandlerTraNilLaXong(t *testing.T) {
	addrs := newCluster(t, 1, "t", "t-dlq")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.DLQTopic = "t-dlq"
	})

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("rac")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var calls atomic.Int32
	stop, _ := runConsumer(t, c, func(context.Context, kafka.Message) error {
		calls.Add(1)
		return nil // "message này bỏ được"
	})
	defer stop()

	waitFor(t, 20*time.Second, "handler được gọi", func() bool { return calls.Load() >= 1 })
	time.Sleep(500 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Errorf("handler được gọi %d lần, muốn 1", got)
	}
}

// Kafka chỉ đảm bảo thứ tự **trong** một partition. Đây là khẳng định quan
// trọng nhất của consumer: tăng Concurrency không được phá đảm bảo đó.
func TestRun_GiuThuTuTrongPartition(t *testing.T) {
	addrs := newCluster(t, 4, "t")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.Concurrency = 4
	})

	const keys = 4
	const perKey = 25
	for i := range perKey {
		for k := range keys {
			msg := kafka.Message{
				Topic: "t",
				Key:   fmt.Sprintf("k-%d", k),
				Value: []byte(strconv.Itoa(i)),
			}
			if err := p.Send(t.Context(), msg); err != nil {
				t.Fatalf("Send: %v", err)
			}
		}
	}

	var mu sync.Mutex
	byPartition := map[int32][]int64{}
	total := 0

	stop, _ := runConsumer(t, c, func(_ context.Context, m kafka.Message) error {
		// Ngủ ngẫu nhiên một nhịp để hai partition thật sự chạy chồng lên nhau.
		time.Sleep(time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		byPartition[m.Partition] = append(byPartition[m.Partition], m.Offset)
		total++
		return nil
	})
	defer stop()

	waitFor(t, 60*time.Second, "nhận đủ message", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return total == keys*perKey
	})

	mu.Lock()
	defer mu.Unlock()
	for part, offsets := range byPartition {
		for i := 1; i < len(offsets); i++ {
			if offsets[i] <= offsets[i-1] {
				t.Fatalf("partition %d nhận sai thứ tự: offset %d sau %d",
					part, offsets[i], offsets[i-1])
			}
		}
	}
	if len(byPartition) < 2 {
		t.Errorf("chỉ dùng %d partition — test không kiểm được điều nó muốn kiểm", len(byPartition))
	}
}

func TestRun_ContextCancel(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	c := newConsumer(t, addrs, "g", []string{"t"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, func(context.Context, kafka.Message) error { return nil }) }()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, muốn nil khi ctx cancel", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run không dừng sau khi ctx cancel")
	}
}

// Close làm Run đang chạy trả về nil. Đây là đường mà httpx.App dùng khi dừng
// service, và cũng là chỗ dễ treo nhất: rời group là một lần rebalance, mà
// rebalance đang bị chặn bởi lần poll cuối.
func TestRun_Close(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	c := newConsumer(t, addrs, "g", []string{"t"})

	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background(), func(context.Context, kafka.Message) error { return nil }) }()

	time.Sleep(500 * time.Millisecond)
	c.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, muốn nil sau Close", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run không dừng sau Close")
	}
}

func TestConsumer_Metrics(t *testing.T) {
	addrs := newCluster(t, 1, "t", "t-dlq")
	reg := prometheus.NewRegistry()
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"}, func(cfg *kafka.ConsumerConfig) {
		cfg.Metrics = reg
		cfg.DLQTopic = "t-dlq"
		cfg.MaxRetries = 1
		cfg.RetryBackoff.BaseDelay = time.Millisecond
	})

	if err := p.Send(t.Context(),
		kafka.Message{Topic: "t", Key: "ok", Value: []byte("ok")},
		kafka.Message{Topic: "t", Key: "hong", Value: []byte("hong")},
	); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop, _ := runConsumer(t, c, func(_ context.Context, m kafka.Message) error {
		if string(m.Value) == "hong" {
			return errors.New("hong")
		}
		return nil
	})
	defer stop()

	readAll(t, addrs, "t-dlq", 1)
	waitFor(t, 20*time.Second, "metric ok được ghi", func() bool {
		return metricValue(t, reg, "kafka_consumer_messages_total",
			map[string]string{"topic": "t", "result": "ok", "group": "g"}) == 1
	})

	if got := metricValue(t, reg, "kafka_consumer_messages_total",
		map[string]string{"topic": "t", "result": "dlq", "group": "g"}); got != 1 {
		t.Errorf("số message vào DLQ = %v, muốn 1", got)
	}
	if got := metricValue(t, reg, "kafka_consumer_retries_total",
		map[string]string{"topic": "t", "group": "g"}); got != 1 {
		t.Errorf("số lần thử lại = %v, muốn 1", got)
	}

	names := metricNames(t, reg)
	if !names["kafka_consumer_handler_duration_seconds"] {
		t.Errorf("thiếu histogram thời gian xử lý, có: %v", keys(names))
	}
}

func TestConsumer_Client(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	c := newConsumer(t, addrs, "g", []string{"t"})

	if c.Client() == nil {
		t.Error("Client() trả nil")
	}
	if err := c.Ping(t.Context()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
