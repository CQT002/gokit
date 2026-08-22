package cache_test

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache"
)

func TestPubSub(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	sub := c.Subscribe(ctx, "events")
	defer sub.Close()

	// Chờ subscribe xong trước khi publish: Pub/Sub của Redis là fire and
	// forget, publish sớm hơn một nhịp là thông điệp mất hẳn.
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("Receive (xác nhận subscribe): %v", err)
	}

	if err := c.Publish(ctx, "events", user{ID: "u-1", Name: "An"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-sub.Channel():
		var got user
		if err := cache.Decode([]byte(msg.Payload), &got); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got.ID != "u-1" {
			t.Errorf("got = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("không nhận được thông điệp")
	}
}

func TestPipelined(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	cmds, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, "a", "1", 0)
		p.Incr(ctx, "n")
		return nil
	})
	if err != nil {
		t.Fatalf("Pipelined: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("len = %d, muốn 2", len(cmds))
	}
	for _, cmd := range cmds {
		if cmd.Err() != nil {
			t.Errorf("%s: %v", cmd.Name(), cmd.Err())
		}
	}

	var got string
	if err := c.Get(ctx, "a", &got); err != nil || got != "1" {
		t.Errorf("got = %q, err = %v", got, err)
	}
}

// Một GET không tìm thấy trong pipeline là chuyện bình thường. Biến nó thành lỗi
// của cả pipeline sẽ khiến chỗ gọi tưởng cả lô đã thất bại.
func TestPipelined_MissKhongLamCaLoThatBai(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	cmds, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.Set(ctx, "a", "1", 0)
		p.Get(ctx, "khong-co")
		return nil
	})
	if err != nil {
		t.Fatalf("Pipelined: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("len = %d", len(cmds))
	}
	if cmds[0].Err() != nil {
		t.Errorf("lệnh SET lỗi: %v", cmds[0].Err())
	}
	if cmds[1].Err() == nil {
		t.Error("lệnh GET phải mang lỗi của riêng nó")
	}

	// SET vẫn phải chạy thật.
	var got string
	if err := c.Get(ctx, "a", &got); err != nil || got != "1" {
		t.Errorf("got = %q, err = %v", got, err)
	}
}

func TestTxPipelined(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	cmds, err := c.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Incr(ctx, "n")
		p.Incr(ctx, "n")
		return nil
	})
	if err != nil {
		t.Fatalf("TxPipelined: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("len = %d", len(cmds))
	}

	var n int
	if err := c.Get(ctx, "n", &n); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if n != 2 {
		t.Errorf("n = %d, muốn 2", n)
	}
}

// Hook thấy mọi lệnh, kể cả lệnh đi qua Redis(), nên không có đường nào lọt qua
// mà không được đo.
func TestLoggingHook_GhiLoiVaKhongGhiDoiSo(t *testing.T) {
	c, _, buf := newClientWithLog(t)
	ctx := t.Context()

	if err := c.Set(ctx, "k", "so-dien-thoai-0912345678", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// WRONGTYPE: lệnh sai kiểu dữ liệu, một lỗi thật.
	if err := c.HGet(ctx, "k", "f", new(string)); err == nil {
		t.Fatal("HGet trên key kiểu string phải lỗi")
	}

	if !hasLog(t, buf, "ERROR", "redis lỗi") {
		t.Errorf("không có dòng log cho lệnh lỗi: %s", buf.String())
	}
	// Đối số của Redis là key và giá trị, tức là dữ liệu người dùng.
	for _, l := range logLines(t, buf) {
		for _, v := range l {
			if s, ok := v.(string); ok && contains(s, "0912345678") {
				t.Errorf("giá trị người dùng lọt vào log: %v", l)
			}
		}
	}
}

// Cache miss không phải sự cố, nên nó không được ghi ở mức ERROR.
func TestLoggingHook_KhongGhiLoiChoMiss(t *testing.T) {
	c, _, buf := newClientWithLog(t)

	var got string
	_ = c.Get(t.Context(), "khong-co", &got)

	if hasLog(t, buf, "ERROR", "redis lỗi") {
		t.Errorf("cache miss bị ghi ở mức ERROR: %s", buf.String())
	}
}
