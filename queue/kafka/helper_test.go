package kafka_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/cqt002/gokit/queue/kafka"
)

// newCluster dựng một cluster Kafka giả chạy trong process.
//
// kfake là bản cài đặt protocol Kafka thuần Go của chính franz-go: nó có
// partition thật, consumer group thật, commit offset thật. Nhờ vậy test đơn vị
// kiểm được những thứ chỉ lộ ra khi nói chuyện với broker — rebalance, commit,
// thứ tự trong partition — mà không cần Docker.
func newCluster(t *testing.T, partitions int32, topics ...string) []string {
	t.Helper()

	c, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.SeedTopics(partitions, topics...),
	)
	if err != nil {
		t.Fatalf("kfake.NewCluster: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

// quietLogger chỉ ghi từ mức Error, để log của franz-go không lấp output test.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// captureLogger trả về logger ghi JSON vào buffer đọc lại được.
func captureLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// syncBuffer là bytes.Buffer an toàn khi ghi từ nhiều goroutine — cần vì
// consumer ghi log từ goroutine của từng partition.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// hasLog cho biết log có dòng nào ở level và chứa msg.
func (b *syncBuffer) hasLog(level, msg string) bool {
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		s, _ := m["msg"].(string)
		if m["level"] == level && strings.Contains(s, msg) {
			return true
		}
	}
	return false
}

// newProducer dựng Producer trỏ vào cluster giả.
func newProducer(t *testing.T, addrs []string, mutate ...func(*kafka.ProducerConfig)) *kafka.Producer {
	t.Helper()

	cfg := kafka.ProducerConfig{Brokers: addrs, Logger: quietLogger()}
	for _, fn := range mutate {
		fn(&cfg)
	}

	p, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// newConsumer dựng Consumer trỏ vào cluster giả, đọc từ đầu topic.
func newConsumer(t *testing.T, addrs []string, group string, topics []string, mutate ...func(*kafka.ConsumerConfig)) *kafka.Consumer {
	t.Helper()

	cfg := kafka.ConsumerConfig{
		Brokers:    addrs,
		Group:      group,
		Topics:     topics,
		FromOldest: true,
		Logger:     quietLogger(),
	}
	for _, fn := range mutate {
		fn(&cfg)
	}

	c, err := kafka.NewConsumer(cfg)
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// readAll đọc tối đa want message từ topic bằng một client thô, không qua
// consumer group — dùng để kiểm tra thứ producer đã ghi.
func readAll(t *testing.T, addrs []string, topic string, want int) []*kgo.Record {
	t.Helper()

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(addrs...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("kgo.NewClient: %v", err)
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out []*kgo.Record
	for len(out) < want {
		fetches := cl.PollRecords(ctx, want-len(out))
		if err := fetches.Err0(); err != nil {
			t.Fatalf("đọc topic %s: %v (đã có %d/%d)", topic, err, len(out), want)
		}
		fetches.EachRecord(func(r *kgo.Record) { out = append(out, r) })
	}
	return out
}

// runConsumer chạy Run trong goroutine và trả về hàm dừng nó, cùng channel lỗi.
func runConsumer(t *testing.T, c *kafka.Consumer, h kafka.Handler) (stop func(), errCh <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { ch <- c.Run(ctx, h) }()

	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Error("Run không dừng sau khi cancel")
		}
	}, ch
}

// waitFor chờ cond thành true, hoặc fail sau timeout.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hết %v mà chưa: %s", timeout, what)
}

// header lấy giá trị một header của bản ghi.
func header(rec *kgo.Record, key string) string {
	for _, h := range rec.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// sprintf in một giá trị theo kiểu %+v, dùng để kiểm tra bí mật không lọt ra.
func sprintf(v any) string { return fmt.Sprintf("%+v", v) }

// kgoOpt là bí danh ngắn cho kgo.Opt, dùng trong literal của test.
type kgoOpt = kgo.Opt

// metricNames trả về tập tên metric có trong registry.
func metricNames(t *testing.T, reg *prometheus.Registry) map[string]bool {
	t.Helper()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	out := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = true
	}
	return out
}

// metricValue trả về tổng giá trị counter của một metric khớp mọi nhãn trong want.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	total := 0.0
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := make(map[string]string, len(m.GetLabel()))
			for _, lp := range m.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			match := true
			for k, v := range want {
				if labels[k] != v {
					match = false
					break
				}
			}
			if match && m.GetCounter() != nil {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}

// keys trả về danh sách khoá của một tập, cho thông báo lỗi đọc được.
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hasPrefix cho biết trong tập có tên nào bắt đầu bằng prefix và khác các metric
// do package này tự đăng ký.
func hasPrefix(m map[string]bool, prefix string) bool {
	for k := range m {
		if strings.HasPrefix(k, prefix) && !strings.HasSuffix(k, "_messages_total") {
			return true
		}
	}
	return false
}
