package kafka_test

import (
	"context"
	"testing"
	"time"

	"github.com/cqt002/gokit/queue/kafka"
)

// Message đi qua broker rồi về phải giữ nguyên nội dung, và nhận thêm ba field
// chỉ broker mới điền được.
func TestMessage_QuaBrokerVaVe(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	want := kafka.Message{
		Topic:   "t",
		Key:     "k-1",
		Value:   []byte("noi dung"),
		Headers: map[string]string{"a": "1", "b": "2"},
	}
	if err := p.Send(t.Context(), want); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	got := recs[0]

	if string(got.Key) != want.Key {
		t.Errorf("Key = %q", got.Key)
	}
	if string(got.Value) != string(want.Value) {
		t.Errorf("Value = %q", got.Value)
	}
	if header(got, "a") != "1" || header(got, "b") != "2" {
		t.Errorf("Headers = %v", got.Headers)
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp không được điền")
	}
	if got.Offset != 0 {
		t.Errorf("Offset = %d, muốn 0 cho message đầu tiên", got.Offset)
	}
}

// Key rỗng nghĩa là "rải đều", không phải "key là chuỗi rỗng": hai thứ đó cho
// ra hai cách chọn partition khác nhau.
func TestMessage_KeyRong(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	if recs[0].Key != nil {
		t.Errorf("Key = %v, muốn nil", recs[0].Key)
	}
}

func TestMessage_KhongCoHeader(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	if err := p.Send(t.Context(), kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	if len(recs[0].Headers) != 0 {
		t.Errorf("Headers = %v, muốn rỗng", recs[0].Headers)
	}
}

// Consumer nhận lại đủ mọi field, kể cả partition và offset — thứ cần để tìm
// lại message khi điều tra.
func TestMessage_ConsumerNhanDuField(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"})

	before := time.Now().Add(-time.Minute)
	if err := p.Send(t.Context(), kafka.Message{
		Topic:   "t",
		Key:     "k-1",
		Value:   []byte("v"),
		Headers: map[string]string{"a": "1"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := make(chan kafka.Message, 1)
	stop, _ := runConsumer(t, c, func(_ context.Context, m kafka.Message) error {
		select {
		case got <- m:
		default:
		}
		return nil
	})
	defer stop()

	select {
	case m := <-got:
		if m.Topic != "t" || m.Key != "k-1" || string(m.Value) != "v" {
			t.Errorf("message = %+v", m)
		}
		if m.Headers["a"] != "1" {
			t.Errorf("Headers = %v", m.Headers)
		}
		if m.Offset != 0 {
			t.Errorf("Offset = %d", m.Offset)
		}
		if m.Timestamp.Before(before) {
			t.Errorf("Timestamp = %v", m.Timestamp)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("không nhận được message")
	}
}
