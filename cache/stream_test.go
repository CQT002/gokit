package cache_test

import (
	"errors"
	"testing"
	"time"

	"github.com/cqt002/gokit/cache"
)

func TestStream_AddReadAck(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.XCreateGroup(ctx, "orders", "worker", "0"); err != nil {
		t.Fatalf("XCreateGroup: %v", err)
	}

	id, err := c.XAdd(ctx, "orders", map[string]any{"payload": user{ID: "u-1"}}, 1000)
	if err != nil {
		t.Fatalf("XAdd: %v", err)
	}
	if id == "" {
		t.Error("XAdd không trả về ID")
	}

	msgs, err := c.XReadGroup(ctx, "orders", "worker", "w1", 10, 0)
	if err != nil {
		t.Fatalf("XReadGroup: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len = %d, muốn 1", len(msgs))
	}

	// Giá trị trong stream đi qua cùng một codec với KV.
	var got user
	raw, ok := msgs[0].Values["payload"].(string)
	if !ok {
		t.Fatalf("payload không phải chuỗi: %T", msgs[0].Values["payload"])
	}
	if err := cache.Decode([]byte(raw), &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.ID != "u-1" {
		t.Errorf("got = %+v", got)
	}

	if err := c.XAck(ctx, "orders", "worker", msgs[0].ID); err != nil {
		t.Fatalf("XAck: %v", err)
	}

	// Đã ack thì không đọc lại nữa.
	if _, err := c.XReadGroup(ctx, "orders", "worker", "w1", 10, 0); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("err = %v, muốn ErrMiss sau khi ack hết", err)
	}
}

// MKSTREAM: gọi được trước khi có entry nào, nên thứ tự khởi động của producer
// và consumer không thành điều kiện để service chạy được.
func TestStream_CreateGroupTruocKhiCoEntry(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.XCreateGroup(ctx, "moi", "g", "$"); err != nil {
		t.Fatalf("XCreateGroup trên stream chưa tồn tại: %v", err)
	}
	// Gọi lại: mọi instance đều gọi lúc khởi động nên "đã có" là bình thường.
	if err := c.XCreateGroup(ctx, "moi", "g", "$"); err != nil {
		t.Fatalf("XCreateGroup lần hai: %v", err)
	}
}

func TestStream_ReadGroupKhongCoGi(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.XCreateGroup(ctx, "rong", "g", "0"); err != nil {
		t.Fatalf("XCreateGroup: %v", err)
	}

	_, err := c.XReadGroup(ctx, "rong", "g", "w1", 1, 0)
	if !errors.Is(err, cache.ErrMiss) {
		t.Errorf("err = %v, muốn ErrMiss khi stream rỗng", err)
	}
}

func TestStream_AckKhongCoID(t *testing.T) {
	c, _ := newClient(t)

	if err := c.XAck(t.Context(), "s", "g"); err != nil {
		t.Errorf("XAck không ID: %v", err)
	}
}

func TestStream_BlockDuoc(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.XCreateGroup(ctx, "s", "g", "0"); err != nil {
		t.Fatalf("XCreateGroup: %v", err)
	}

	start := time.Now()
	_, err := c.XReadGroup(ctx, "s", "g", "w1", 1, 100*time.Millisecond)
	if !errors.Is(err, cache.ErrMiss) {
		t.Errorf("err = %v, muốn ErrMiss khi hết thời gian chờ", err)
	}
	if took := time.Since(start); took < 50*time.Millisecond {
		t.Errorf("trả về sau %v — không thực sự chờ", took)
	}
}
