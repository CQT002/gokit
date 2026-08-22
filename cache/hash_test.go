package cache_test

import (
	"errors"
	"testing"

	"github.com/cqt002/gokit/cache"
)

func TestHash_SetGet(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	err := c.HSet(ctx, "u:1", map[string]any{
		"name": "An",
		"age":  30,
		"meta": user{ID: "u-1", Name: "An"},
	})
	if err != nil {
		t.Fatalf("HSet: %v", err)
	}

	var name string
	if err := c.HGet(ctx, "u:1", "name", &name); err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if name != "An" {
		t.Errorf("name = %q", name)
	}

	var age int
	if err := c.HGet(ctx, "u:1", "age", &age); err != nil {
		t.Fatalf("HGet age: %v", err)
	}
	if age != 30 {
		t.Errorf("age = %d", age)
	}

	var meta user
	if err := c.HGet(ctx, "u:1", "meta", &meta); err != nil {
		t.Fatalf("HGet meta: %v", err)
	}
	if meta.ID != "u-1" {
		t.Errorf("meta = %+v", meta)
	}
}

func TestHash_GetMiss(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	var v string
	if err := c.HGet(ctx, "khong-co", "f", &v); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("hash không tồn tại: err = %v, muốn ErrMiss", err)
	}

	if err := c.HSet(ctx, "u:1", map[string]any{"name": "An"}); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if err := c.HGet(ctx, "u:1", "khong-co", &v); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("field không tồn tại: err = %v, muốn ErrMiss", err)
	}
}

func TestHash_SetNX(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	ok, err := c.HSetNX(ctx, "u:1", "name", "An")
	if err != nil {
		t.Fatalf("HSetNX: %v", err)
	}
	if !ok {
		t.Fatal("lần đầu HSetNX phải ghi được")
	}

	if ok, _ = c.HSetNX(ctx, "u:1", "name", "Bình"); ok {
		t.Error("lần hai HSetNX không được ghi")
	}

	var name string
	if err := c.HGet(ctx, "u:1", "name", &name); err != nil {
		t.Fatalf("HGet: %v", err)
	}
	if name != "An" {
		t.Errorf("name = %q, giá trị đã bị ghi đè", name)
	}
}

func TestHash_Del(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.HSet(ctx, "u:1", map[string]any{"a": "1", "b": "2"}); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if err := c.HDel(ctx, "u:1", "a"); err != nil {
		t.Fatalf("HDel: %v", err)
	}

	all, err := c.HGetAll(ctx, "u:1")
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	if _, ok := all["a"]; ok {
		t.Error("field a chưa bị xoá")
	}
	if string(all["b"]) != "2" {
		t.Errorf("all = %v", all)
	}

	// Danh sách rỗng không được gửi lệnh sai cú pháp xuống Redis.
	if err := c.HDel(ctx, "u:1"); err != nil {
		t.Errorf("HDel rỗng: %v", err)
	}
	if err := c.HSet(ctx, "u:1", nil); err != nil {
		t.Errorf("HSet rỗng: %v", err)
	}
}

// Redis không phân biệt "hash rỗng" với "hash không có", nên một ErrMiss ở đây
// sẽ là lỗi bịa ra.
func TestHash_GetAllTrenHashKhongCo(t *testing.T) {
	c, _ := newClient(t)

	all, err := c.HGetAll(t.Context(), "khong-co")
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("all = %v, muốn rỗng", all)
	}
}

func TestHash_MGet(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.HSet(ctx, "u:1", map[string]any{"a": "1", "c": "3"}); err != nil {
		t.Fatalf("HSet: %v", err)
	}

	vals, err := c.HMGet(ctx, "u:1", "a", "b", "c")
	if err != nil {
		t.Fatalf("HMGet: %v", err)
	}
	if len(vals) != 3 {
		t.Fatalf("len = %d, muốn 3", len(vals))
	}
	if string(vals[0]) != "1" || vals[1] != nil || string(vals[2]) != "3" {
		t.Errorf("vals = %q", vals)
	}

	if got, err := c.HMGet(ctx, "u:1"); err != nil || got != nil {
		t.Errorf("HMGet không field: %v, %v", got, err)
	}
}

func TestHash_IncrBy(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	n, err := c.HIncrBy(ctx, "stats", "hits", 3)
	if err != nil {
		t.Fatalf("HIncrBy: %v", err)
	}
	if n != 3 {
		t.Errorf("n = %d", n)
	}
	if n, _ = c.HIncrBy(ctx, "stats", "hits", -1); n != 2 {
		t.Errorf("n = %d, muốn 2", n)
	}
}

// Các field của một hash thường khác kiểu nhau, nên HGetAll trả bytes thô và
// chỗ gọi tự giải mã bằng Decode.
func TestHash_GetAllGiaiMaTungField(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.HSet(ctx, "u:1", map[string]any{"name": "An", "meta": user{ID: "u-1"}}); err != nil {
		t.Fatalf("HSet: %v", err)
	}

	all, err := c.HGetAll(ctx, "u:1")
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}

	var name string
	if err := cache.Decode(all["name"], &name); err != nil {
		t.Fatalf("Decode name: %v", err)
	}
	var meta user
	if err := cache.Decode(all["meta"], &meta); err != nil {
		t.Fatalf("Decode meta: %v", err)
	}
	if name != "An" || meta.ID != "u-1" {
		t.Errorf("name = %q, meta = %+v", name, meta)
	}
}
