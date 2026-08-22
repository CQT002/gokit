package lock_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache/lock"
)

// newLocker dựng Locker trên một Redis giả.
func newLocker(t *testing.T, opts ...lock.Option) (*lock.Locker, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	t.Cleanup(func() { _ = rdb.Close() })

	opts = append([]lock.Option{lock.WithLogger(quietLogger())}, opts...)
	return lock.NewLocker(rdb, opts...), mr
}

// quietLogger chỉ ghi từ mức Error, để log của test không lẫn vào output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func TestAcquire(t *testing.T) {
	l, _ := newLocker(t)
	ctx := t.Context()

	lk, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if lk.Key() != "job" {
		t.Errorf("Key = %q", lk.Key())
	}
	if err := lk.Context().Err(); err != nil {
		t.Errorf("context của khoá đã chết ngay sau khi giành: %v", err)
	}

	if err := lk.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// Khoá đang có người giữ là kết quả bình thường, không phải sự cố.
func TestAcquire_DaCoNguoiGiu(t *testing.T) {
	l, _ := newLocker(t)
	ctx := t.Context()

	first, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer first.Release(ctx)

	_, err = l.Acquire(ctx, "job", time.Minute)
	if !errors.Is(err, lock.ErrNotAcquired) {
		t.Fatalf("err = %v, muốn ErrNotAcquired", err)
	}
	// Không chờ: trả về ngay.
	if !errors.Is(err, lock.ErrNotAcquired) {
		t.Error("Acquire chờ thay vì trả về ngay")
	}
}

func TestAcquire_ThamSoSai(t *testing.T) {
	l, _ := newLocker(t)
	ctx := t.Context()

	if _, err := l.Acquire(ctx, "", time.Minute); err == nil {
		t.Error("key rỗng mà không báo lỗi")
	}
	if _, err := l.Acquire(ctx, "job", 0); err == nil {
		t.Error("ttl = 0 mà không báo lỗi")
	}
}

// Context của khoá không gia hạn có deadline bằng đúng lúc khoá hết hạn, nên
// công việc tôn trọng context không bao giờ chạy quá lúc khoá còn hiệu lực.
func TestAcquire_ContextCoDeadlineBangTTL(t *testing.T) {
	l, _ := newLocker(t)

	lk, err := l.Acquire(t.Context(), "job", 30*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = lk.Release(context.Background()) }()

	deadline, ok := lk.Context().Deadline()
	if !ok {
		t.Fatal("context của khoá không có deadline")
	}
	if d := time.Until(deadline); d <= 0 || d > 30*time.Second {
		t.Errorf("deadline còn %v, muốn khoảng 30s", d)
	}
}

func TestAcquire_ContextCancelKhiHetHan(t *testing.T) {
	l, _ := newLocker(t)

	lk, err := l.Acquire(t.Context(), "job", 80*time.Millisecond)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	select {
	case <-lk.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context không bị cancel khi khoá hết hạn")
	}
	if cause := context.Cause(lk.Context()); !errors.Is(cause, lock.ErrLockLost) {
		t.Errorf("cause = %v, muốn ErrLockLost", cause)
	}
}

// Chỗ gọi cancel context gốc thì context của khoá cũng chết — nhưng cause phải
// là của chỗ gọi, không phải ErrLockLost.
func TestAcquire_ContextGocCancel(t *testing.T) {
	l, _ := newLocker(t)

	ctx, cancel := context.WithCancel(context.Background())
	lk, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	cancel()
	<-lk.Context().Done()

	if cause := context.Cause(lk.Context()); !errors.Is(cause, context.Canceled) {
		t.Errorf("cause = %v, muốn context.Canceled", cause)
	}
	if errors.Is(context.Cause(lk.Context()), lock.ErrLockLost) {
		t.Error("chỗ gọi cancel bị báo là mất khoá")
	}
}

func TestRelease_CancelContext(t *testing.T) {
	l, _ := newLocker(t)
	ctx := t.Context()

	lk, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if relErr := lk.Release(ctx); relErr != nil {
		t.Fatalf("Release: %v", relErr)
	}

	if lk.Context().Err() == nil {
		t.Error("context vẫn sống sau Release")
	}
	if cause := context.Cause(lk.Context()); !errors.Is(cause, lock.ErrReleased) {
		t.Errorf("cause = %v, muốn ErrReleased", cause)
	}

	// Nhả rồi thì người khác giành được ngay.
	other, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire sau Release: %v", err)
	}
	_ = other.Release(ctx)
}

// `defer lk.Release(ctx)` cộng với một Release tường minh là mẫu rất thường gặp.
func TestRelease_GoiNhieuLan(t *testing.T) {
	l, _ := newLocker(t)
	ctx := t.Context()

	lk, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	first := lk.Release(ctx)
	second := lk.Release(ctx)
	if first != nil {
		t.Fatalf("Release lần đầu: %v", first)
	}
	if second != nil {
		t.Errorf("Release lần hai: %v — phải trả lại đúng kết quả lần đầu", second)
	}
}

// Khoá đã hết hạn trước khi nhả nghĩa là công việc vừa rồi **có thể** đã chạy
// song song với instance khác. Đó là chuyện cần biết, không phải chuyện vô hại.
func TestRelease_KhoaDaHetHan(t *testing.T) {
	l, mr := newLocker(t)
	ctx := t.Context()

	lk, err := l.Acquire(ctx, "job", time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	mr.FastForward(2 * time.Second)

	if err := lk.Release(ctx); !errors.Is(err, lock.ErrLockLost) {
		t.Errorf("err = %v, muốn ErrLockLost", err)
	}
}

func TestAcquireWithRenew_GiuKhoaQuaTTL(t *testing.T) {
	l, _ := newLocker(t, lock.WithRenewInterval(50*time.Millisecond))
	ctx := t.Context()

	lk, err := l.AcquireWithRenew(ctx, "job", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("AcquireWithRenew: %v", err)
	}
	defer func() { _ = lk.Release(context.Background()) }()

	// Chờ lâu hơn ttl. Không có gia hạn thì khoá đã mất.
	time.Sleep(500 * time.Millisecond)

	if err := lk.Context().Err(); err != nil {
		t.Fatalf("context chết dù đang được gia hạn: %v (cause %v)", err, context.Cause(lk.Context()))
	}
	// Và người khác vẫn không giành được.
	if _, err := l.AcquireWithRenew(ctx, "job", time.Second); !errors.Is(err, lock.ErrNotAcquired) {
		t.Errorf("instance khác giành được khoá đang giữ: %v", err)
	}
}

// Đây là điểm khác biệt của package: mất khoá thì công việc dừng, không phải
// "ghi một dòng log rồi chạy tiếp".
func TestAcquireWithRenew_MatKhoaThiCancelContext(t *testing.T) {
	l, mr := newLocker(t, lock.WithRenewInterval(50*time.Millisecond))

	lk, err := l.AcquireWithRenew(t.Context(), "job", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("AcquireWithRenew: %v", err)
	}

	// Xoá khoá dưới chân goroutine gia hạn: đúng những gì xảy ra khi Redis
	// failover hoặc khi khoá hết hạn vì mạng nghẽn.
	mr.Del("job")

	select {
	case <-lk.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("mất khoá mà context không bị cancel")
	}
	if cause := context.Cause(lk.Context()); !errors.Is(cause, lock.ErrLockLost) {
		t.Errorf("cause = %v, muốn ErrLockLost", cause)
	}
}

func TestAcquireWithRenew_ReleaseDungGoroutineGiaHan(t *testing.T) {
	l, _ := newLocker(t, lock.WithRenewInterval(20*time.Millisecond))
	ctx := t.Context()

	lk, err := l.AcquireWithRenew(ctx, "job", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("AcquireWithRenew: %v", err)
	}
	if relErr := lk.Release(ctx); relErr != nil {
		t.Fatalf("Release: %v", relErr)
	}

	// Sau Release, không còn ai gia hạn nên khoá không quay lại.
	time.Sleep(150 * time.Millisecond)
	other, err := l.Acquire(ctx, "job", time.Minute)
	if err != nil {
		t.Fatalf("Acquire sau Release: %v", err)
	}
	_ = other.Release(ctx)
}

// Khoá khác nhau không chặn nhau.
func TestAcquire_KhoaKhacNhau(t *testing.T) {
	l, _ := newLocker(t)
	ctx := t.Context()

	a, err := l.Acquire(ctx, "job-a", time.Minute)
	if err != nil {
		t.Fatalf("Acquire a: %v", err)
	}
	defer a.Release(ctx)

	b, err := l.Acquire(ctx, "job-b", time.Minute)
	if err != nil {
		t.Fatalf("Acquire b: %v", err)
	}
	defer b.Release(ctx)
}

func TestAcquire_RedisSap(t *testing.T) {
	l, mr := newLocker(t)
	mr.Close()

	_, err := l.Acquire(t.Context(), "job", time.Minute)
	if err == nil {
		t.Fatal("Redis sập mà Acquire vẫn thành công")
	}
	if errors.Is(err, lock.ErrNotAcquired) {
		t.Error("lỗi kết nối bị báo là ErrNotAcquired — hai chuyện khác nhau")
	}
}
