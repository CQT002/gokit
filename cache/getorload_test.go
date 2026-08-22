package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cqt002/gokit/cache"
)

func TestGetOrLoad_MissRoiHit(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	var calls atomic.Int32
	load := func(context.Context) (user, error) {
		calls.Add(1)
		return user{ID: "u-1", Name: "An"}, nil
	}

	got, err := cache.GetOrLoad(ctx, c, "u:1", time.Minute, load)
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if got.Name != "An" {
		t.Errorf("got = %+v", got)
	}

	// Lần hai đọc từ cache, không gọi load.
	got, err = cache.GetOrLoad(ctx, c, "u:1", time.Minute, load)
	if err != nil {
		t.Fatalf("GetOrLoad lần hai: %v", err)
	}
	if got.Name != "An" {
		t.Errorf("got = %+v", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("load được gọi %d lần, muốn 1", n)
	}
}

// Đây là lý do package này tồn tại: một key nóng hết hạn không được biến thành
// một trăm câu query giống nhau đập vào database cùng lúc.
func TestGetOrLoad_ChongStampede(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	var calls atomic.Int32
	start := make(chan struct{})

	load := func(context.Context) (user, error) {
		calls.Add(1)
		// Giữ lần load đầu lại một nhịp để các goroutine khác kịp vào chờ.
		time.Sleep(50 * time.Millisecond)
		return user{ID: "u-1", Name: "An"}, nil
	}

	const n = 50
	var wg sync.WaitGroup
	results := make([]user, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = cache.GetOrLoad(ctx, c, "u:1", time.Minute, load)
		}()
	}
	close(start)
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i].Name != "An" {
			t.Errorf("goroutine %d nhận %+v", i, results[i])
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("load được gọi %d lần với %d request đồng thời, muốn 1", got, n)
	}
}

// Hai key khác nhau không được gom vào nhau.
func TestGetOrLoad_KeyKhacNhauKhongGom(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	var calls atomic.Int32
	load := func(context.Context) (user, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return user{ID: "x"}, nil
	}

	var wg sync.WaitGroup
	for _, key := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.GetOrLoad(ctx, c, key, time.Minute, load); err != nil {
				t.Errorf("key %s: %v", key, err)
			}
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 3 {
		t.Errorf("load được gọi %d lần, muốn 3", got)
	}
}

func TestGetOrLoad_LoadLoi(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	boom := errors.New("database sập")
	_, err := cache.GetOrLoad(ctx, c, "u:1", time.Minute,
		func(context.Context) (user, error) { return user{}, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, muốn bọc lỗi gốc", err)
	}

	// Không được cache giá trị zero khi load lỗi.
	var got user
	if err := c.Get(ctx, "u:1", &got); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("load lỗi mà vẫn ghi cache: err = %v", err)
	}
}

func TestGetOrLoad_ThieuLoad(t *testing.T) {
	c, _ := newClient(t)

	_, err := cache.GetOrLoad[user](t.Context(), c, "u:1", time.Minute, nil)
	if err == nil {
		t.Fatal("load nil mà không báo lỗi")
	}
}

// Cache hỏng không được biến thành API hỏng.
func TestGetOrLoad_RedisSapVanTraDuLieu(t *testing.T) {
	c, mr, buf := newClientWithLog(t)
	mr.Close()

	got, err := cache.GetOrLoad(t.Context(), c, "u:1", time.Minute,
		func(context.Context) (user, error) { return user{ID: "u-1", Name: "An"}, nil })
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if got.Name != "An" {
		t.Errorf("got = %+v", got)
	}
	if !hasLog(t, buf, "WARN", "không đọc được cache") {
		t.Errorf("không có cảnh báo về việc Redis lỗi: %s", buf.String())
	}
}

// Ghi cache thất bại thì dữ liệu vẫn phải trả về: nó đã lấy được rồi.
func TestGetOrLoad_GhiCacheThatBaiVanTraDuLieu(t *testing.T) {
	kv := &fakeKV{
		getErr: cache.ErrMiss,
		setErr: errors.New("redis đầy bộ nhớ"),
	}

	got, err := cache.GetOrLoad(t.Context(), kv, "u:1", time.Minute,
		func(context.Context) (user, error) { return user{ID: "u-1", Name: "An"}, nil })
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if got.Name != "An" {
		t.Errorf("got = %+v", got)
	}
}

// Test này tồn tại để chứng minh một tuyên bố của thiết kế: KV hẹp đủ để mock
// bằng vài dòng. Nếu nó thành dài dòng thì interface đã phình ra.
func TestGetOrLoad_MockDuoc(t *testing.T) {
	kv := &fakeKV{getErr: cache.ErrMiss}

	got, err := cache.GetOrLoad(t.Context(), kv, "u:1", time.Minute,
		func(context.Context) (user, error) { return user{Name: "An"}, nil })
	if err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	if got.Name != "An" {
		t.Errorf("got = %+v", got)
	}
	if kv.sets != 1 {
		t.Errorf("sets = %d, muốn 1", kv.sets)
	}
}
