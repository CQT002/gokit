package kafka_test

import (
	"context"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/tracectx"
	"github.com/cqt002/gokit/queue/kafka"
)

// Trace đi xuyên broker: producer chèn traceparent, consumer đọc ra và đặt vào
// ctx, nên log hai bên nối được với nhau bằng cùng một trace ID.
func TestTrace_XuyenBroker(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.PropagateTrace = true })
	c := newConsumer(t, addrs, "g", []string{"t"})

	root := tracectx.NewRoot()
	ctx := tracectx.WithSpanContext(t.Context(), root)

	if err := p.Send(ctx, kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := make(chan tracectx.SpanContext, 1)
	stop, _ := runConsumer(t, c, func(ctx context.Context, _ kafka.Message) error {
		sc, _ := tracectx.FromContext(ctx)
		select {
		case got <- sc:
		default:
		}
		return nil
	})
	defer stop()

	select {
	case sc := <-got:
		if sc.TraceID != root.TraceID {
			t.Errorf("TraceID = %q, muốn %q", sc.TraceID, root.TraceID)
		}
		// Span con, không phải span của producer: việc xử lý ở consumer là một
		// chặng riêng trong cùng một trace.
		if sc.SpanID == root.SpanID {
			t.Error("consumer dùng lại SpanID của producer thay vì tạo span con")
		}
		if !sc.Valid() {
			t.Errorf("SpanContext không hợp lệ: %+v", sc)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("không nhận được message")
	}
}

// PropagateTrace tắt thì không có header nào được thêm.
func TestTrace_TatThiKhongChenHeader(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)

	ctx := tracectx.WithSpanContext(t.Context(), tracectx.NewRoot())
	if err := p.Send(ctx, kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	if got := header(recs[0], tracectx.HeaderTraceparent); got != "" {
		t.Errorf("traceparent = %q dù PropagateTrace tắt", got)
	}
}

// Không có trace trong ctx thì tạo trace gốc mới, thay vì gửi header rỗng: một
// message không lần ra được nguồn thường là message đáng lần ra nhất.
func TestTrace_KhongCoTraceTrongCtx(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.PropagateTrace = true })

	if err := p.Send(context.Background(), kafka.Message{Topic: "t", Value: []byte("v")}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	tp := header(recs[0], tracectx.HeaderTraceparent)
	if tp == "" {
		t.Fatal("không có header traceparent")
	}
	if _, err := tracectx.ParseTraceparent(tp); err != nil {
		t.Errorf("traceparent %q không parse được: %v", tp, err)
	}
}

// Header do chỗ gọi tự đặt không bị ghi đè: đó là cách phát lại message từ DLQ
// mà vẫn giữ trace gốc.
func TestTrace_KhongGhiDeHeaderDaCo(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.PropagateTrace = true })

	original := tracectx.NewRoot()
	ctx := tracectx.WithSpanContext(t.Context(), tracectx.NewRoot())

	err := p.Send(ctx, kafka.Message{
		Topic:   "t",
		Value:   []byte("v"),
		Headers: map[string]string{tracectx.HeaderTraceparent: original.Traceparent()},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	recs := readAll(t, addrs, "t", 1)
	if got := header(recs[0], tracectx.HeaderTraceparent); got != original.Traceparent() {
		t.Errorf("traceparent = %q, muốn giữ nguyên %q", got, original.Traceparent())
	}
}

// Send không được sửa map Headers của chỗ gọi — nhất là khi cùng một Message
// được gửi lại lần hai.
func TestTrace_KhongSuaMapCuaChoGoi(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs, func(c *kafka.ProducerConfig) { c.PropagateTrace = true })

	headers := map[string]string{"a": "1"}
	msg := kafka.Message{Topic: "t", Value: []byte("v"), Headers: headers}

	ctx := tracectx.WithSpanContext(t.Context(), tracectx.NewRoot())
	if err := p.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(headers) != 1 {
		t.Errorf("map của chỗ gọi bị sửa: %v", headers)
	}
	if _, ok := headers[tracectx.HeaderTraceparent]; ok {
		t.Error("traceparent bị ghi vào map của chỗ gọi")
	}
}

// Header sai định dạng không phải lỗi của message: producer của đội khác hoặc
// ngôn ngữ khác đơn giản là không gửi header này.
func TestTrace_HeaderHongThiTaoTraceMoi(t *testing.T) {
	addrs := newCluster(t, 1, "t")
	p := newProducer(t, addrs)
	c := newConsumer(t, addrs, "g", []string{"t"})

	err := p.Send(t.Context(), kafka.Message{
		Topic:   "t",
		Value:   []byte("v"),
		Headers: map[string]string{tracectx.HeaderTraceparent: "khong-phai-traceparent"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := make(chan tracectx.SpanContext, 1)
	stop, _ := runConsumer(t, c, func(ctx context.Context, _ kafka.Message) error {
		sc, _ := tracectx.FromContext(ctx)
		select {
		case got <- sc:
		default:
		}
		return nil
	})
	defer stop()

	select {
	case sc := <-got:
		if !sc.Valid() {
			t.Errorf("không tạo trace mới khi header hỏng: %+v", sc)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("không nhận được message")
	}
}
