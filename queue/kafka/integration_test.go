//go:build integration

package kafka_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/cqt002/gokit/core/retry"
	"github.com/cqt002/gokit/core/tracectx"
	"github.com/cqt002/gokit/queue/kafka"
	"github.com/cqt002/gokit/testx"
)

// Test này chạy trên Kafka **thật** trong Docker, không phải kfake.
//
// kfake cài đặt protocol rất sát, nhưng nó không phải Kafka: nó không có
// controller thật, không có replication, và không chạy cùng phiên bản broker mà
// production dùng. Một test trên broker thật là chốt cuối cho những khác biệt
// đó — nhất là ở phần consumer group và commit offset, nơi hành vi phụ thuộc
// vào cấu hình phía broker.
//
//	go test -tags=integration ./...

// setupTopics tạo topic trên broker thật.
//
// Tạo tường minh thay vì dựa vào auto-create: auto-create thường bị tắt ở cụm
// production, và một test dựa vào nó sẽ xanh ở local rồi đỏ ở nơi khác.
func setupTopics(t *testing.T, brokers []string, partitions int32, topics ...string) {
	t.Helper()

	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("kgo.NewClient: %v", err)
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := kadm.NewClient(cl).CreateTopics(ctx, partitions, 1, nil, topics...)
	if err != nil {
		t.Fatalf("tạo topic: %v", err)
	}
	for _, r := range resp.Sorted() {
		if r.Err != nil && !strings.Contains(r.Err.Error(), "already exists") {
			t.Fatalf("tạo topic %s: %v", r.Topic, r.Err)
		}
	}
}

func TestIntegration_ProducerConsumerDLQ(t *testing.T) {
	brokers := testx.KafkaContainer(t)
	setupTopics(t, brokers, 3, "orders", "orders-dlq")

	log, logs := testx.CaptureLogs(t)

	p, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers:        brokers,
		ClientID:       "gokit-integration",
		Compression:    "zstd",
		RequiredAcks:   kafka.AcksAll,
		PropagateTrace: true,
		Logger:         log,
	})
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer p.Close()

	if pingErr := p.Ping(t.Context()); pingErr != nil {
		t.Fatalf("Ping: %v", pingErr)
	}

	c, err := kafka.NewConsumer(kafka.ConsumerConfig{
		Brokers:      brokers,
		Group:        "gokit-integration",
		Topics:       []string{"orders"},
		FromOldest:   true,
		Concurrency:  3,
		MaxRetries:   1,
		RetryBackoff: retryFast(),
		DLQTopic:     "orders-dlq",
		Logger:       log,
	})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer c.Close()

	root := tracectx.NewRoot()
	ctx := tracectx.WithSpanContext(t.Context(), root)

	const n = 30
	for i := range n {
		msg := kafka.Message{
			Topic: "orders",
			Key:   "od-" + strconv.Itoa(i%5),
			Value: []byte(strconv.Itoa(i)),
		}
		if err := p.Send(ctx, msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	// Một message cố tình hỏng, để kiểm đường retry → DLQ trên broker thật.
	if err := p.Send(ctx, kafka.Message{Topic: "orders", Key: "hong", Value: []byte("hong")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var mu sync.Mutex
	seen := map[string]bool{}
	traceIDs := map[string]bool{}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Run(runCtx, func(ctx context.Context, m kafka.Message) error {
			if string(m.Value) == "hong" {
				return errors.New("message hong")
			}
			mu.Lock()
			defer mu.Unlock()
			seen[string(m.Value)] = true
			traceIDs[tracectx.TraceIDFrom(ctx)] = true
			return nil
		})
	}()

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(seen)
		mu.Unlock()
		if got == n {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	if len(seen) != n {
		mu.Unlock()
		t.Fatalf("nhận %d/%d message\n%s", len(seen), n, logs)
	}
	// Trace đi xuyên broker thật: mọi message chung một trace ID với producer.
	if len(traceIDs) != 1 || !traceIDs[root.TraceID] {
		mu.Unlock()
		t.Errorf("trace ID nhận được = %v, muốn chỉ %q", traceIDs, root.TraceID)
	}
	mu.Unlock()

	// Message hỏng phải nằm trong DLQ kèm thông tin nguồn gốc.
	dlq := readDLQ(t, brokers, "orders-dlq")
	if got := header(dlq, kafka.HeaderDLQTopic); got != "orders" {
		t.Errorf("%s = %q", kafka.HeaderDLQTopic, got)
	}
	if got := header(dlq, kafka.HeaderDLQError); !strings.Contains(got, "message hong") {
		t.Errorf("%s = %q", kafka.HeaderDLQError, got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, muốn nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run không dừng")
	}
}

// retryFast là chính sách thử lại gần như không chờ, cho test.
func retryFast() retry.Policy {
	return retry.Policy{BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
}

// readDLQ đọc bản ghi đầu tiên trong topic DLQ.
func readDLQ(t *testing.T, brokers []string, topic string) *kgo.Record {
	t.Helper()

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("kgo.NewClient: %v", err)
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for {
		fetches := cl.PollRecords(ctx, 1)
		if err := fetches.Err0(); err != nil {
			t.Fatalf("đọc DLQ: %v", err)
		}
		recs := fetches.Records()
		if len(recs) > 0 {
			return recs[0]
		}
	}
}
