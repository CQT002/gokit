package testx

import (
	"sync"
	"testing"
	"time"
)

// FreezeTime trả về nguồn thời gian đứng yên tại at.
//
// Go **không** có cách đóng băng time.Now toàn cục, và điều đó là tốt: một hàm
// đổi được đồng hồ của cả process sẽ làm hai test chạy song song can thiệp vào
// nhau. Ở đây thời gian là một giá trị truyền tay, giống mọi phụ thuộc khác
// trong gokit.
//
// Nghĩa là code cần test phải nhận nguồn thời gian thay vì gọi time.Now trực
// tiếp — thường là một field `now func() time.Time` mặc định bằng time.Now:
//
//	clock := testx.FreezeTime(t, time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC))
//	svc := Service{now: clock.Now}
//
//	svc.Create(ctx, order)
//	clock.Advance(48 * time.Hour)
//	if !svc.IsExpired(order) { t.Error("đơn phải hết hạn sau 48 giờ") }
//
// Đổi lấy được thứ đáng giá: test kiểm tra hành vi theo thời gian mà không sleep,
// nên nó chạy trong micro giây và không bao giờ chập chờn.
func FreezeTime(tb testing.TB, at time.Time) *Clock {
	tb.Helper()

	if at.IsZero() {
		tb.Fatal("testx: FreezeTime cần một mốc thời gian khác zero")
	}
	return &Clock{now: at}
}

// Clock là nguồn thời gian điều khiển được.
//
// An toàn khi dùng từ nhiều goroutine: code cần test thường đọc thời gian từ
// goroutine khác với goroutine của test.
type Clock struct {
	mu  sync.RWMutex
	now time.Time
}

// Now trả về thời điểm hiện tại của đồng hồ.
//
// Truyền chính method này vào chỗ cần: nó có đúng chữ ký func() time.Time của
// time.Now.
func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Advance đẩy đồng hồ lên d. d âm là đẩy lùi.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set đặt đồng hồ về đúng một mốc.
func (c *Clock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}
