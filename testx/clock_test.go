package testx_test

import (
	"sync"
	"testing"
	"time"

	"github.com/cqt002/gokit/testx"
)

// service mô phỏng code nhận nguồn thời gian thay vì gọi time.Now trực tiếp.
type service struct {
	now func() time.Time
}

func (s service) expired(createdAt time.Time, ttl time.Duration) bool {
	return s.now().Sub(createdAt) > ttl
}

func TestFreezeTime_DungYen(t *testing.T) {
	at := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	clock := testx.FreezeTime(t, at)

	first := clock.Now()
	time.Sleep(5 * time.Millisecond)
	second := clock.Now()

	if !first.Equal(at) || !second.Equal(at) {
		t.Errorf("đồng hồ chạy: %v rồi %v, muốn %v", first, second, at)
	}
}

// Đây là thứ đáng giá: test hành vi theo thời gian mà không sleep.
func TestFreezeTime_Advance(t *testing.T) {
	at := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	clock := testx.FreezeTime(t, at)
	svc := service{now: clock.Now}

	if svc.expired(at, 48*time.Hour) {
		t.Error("hết hạn ngay lúc tạo")
	}

	clock.Advance(47 * time.Hour)
	if svc.expired(at, 48*time.Hour) {
		t.Error("hết hạn sớm sau 47 giờ")
	}

	clock.Advance(2 * time.Hour)
	if !svc.expired(at, 48*time.Hour) {
		t.Error("không hết hạn sau 49 giờ")
	}
}

func TestFreezeTime_AdvanceAm(t *testing.T) {
	at := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	clock := testx.FreezeTime(t, at)

	clock.Advance(-time.Hour)
	if got := clock.Now(); !got.Equal(at.Add(-time.Hour)) {
		t.Errorf("Now = %v", got)
	}
}

func TestFreezeTime_Set(t *testing.T) {
	clock := testx.FreezeTime(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	want := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	clock.Set(want)
	if got := clock.Now(); !got.Equal(want) {
		t.Errorf("Now = %v, muốn %v", got, want)
	}
}

// Code cần test thường đọc thời gian từ goroutine khác với goroutine của test.
func TestFreezeTime_NhieuGoroutine(t *testing.T) {
	clock := testx.FreezeTime(t, time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC))

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = clock.Now()
			clock.Advance(time.Second)
		}()
	}
	wg.Wait()

	if got := clock.Now().Second(); got != 20 {
		t.Errorf("giây = %d, muốn 20", got)
	}
}
