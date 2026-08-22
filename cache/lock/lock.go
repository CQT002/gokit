// Package lock cung cấp distributed lock trên Redis, với một điểm khác biệt:
// mất khoá thì context của công việc **bị cancel**.
//
// Vấn đề của cách làm thông thường: goroutine gia hạn khoá thấy gia hạn thất
// bại thì ghi một dòng log rồi thôi, còn công việc vẫn chạy tiếp. Lúc đó khoá
// đã sang tay instance khác, nên hai instance cùng làm một việc — đúng điều mà
// khoá tồn tại để ngăn, và không có dấu hiệu nào ngoài một dòng log không ai đọc.
//
// Ở đây [Lock.Context] là nguồn sự thật duy nhất: còn giữ khoá thì nó còn sống,
// mất khoá thì nó bị cancel. Công việc chỉ cần tôn trọng context như mọi chỗ
// khác trong Go.
//
//	lk, err := locker.AcquireWithRenew(ctx, "job:daily-report", 30*time.Second)
//	if errors.Is(err, lock.ErrNotAcquired) { return nil } // instance khác đang làm
//	if err != nil { return err }
//	defer lk.Release(context.WithoutCancel(ctx))
//
//	return doWork(lk.Context())
package lock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
)

// DefaultRenewDivisor quyết định chu kỳ gia hạn mặc định: ttl chia cho số này.
//
// Bằng 3 nghĩa là gia hạn ba lần trong mỗi vòng ttl, nên phải trượt hai lần
// liên tiếp mới mất khoá. Bằng 2 thì chỉ cần trượt một lần là mất — quá sát khi
// mạng có một nhịp chậm.
const DefaultRenewDivisor = 3

// MinRenewInterval là chu kỳ gia hạn nhỏ nhất, để ttl rất ngắn không biến thành
// một vòng lặp đập vào Redis.
const MinRenewInterval = 50 * time.Millisecond

var (
	// ErrNotAcquired là lỗi khi khoá đang do người khác giữ.
	//
	// Đây là kết quả **bình thường**, không phải sự cố: nó nghĩa là một instance
	// khác đang làm việc đó. Chỗ gọi thường xử lý bằng cách trả về luôn.
	ErrNotAcquired = errors.New("lock: khoá đang do instance khác giữ")

	// ErrLockLost là nguyên nhân cancel của [Lock.Context] khi mất khoá.
	//
	// Lấy ra bằng context.Cause(lk.Context()) để phân biệt "mất khoá" với "chỗ
	// gọi cancel" — hai chuyện cần xử lý khác nhau.
	ErrLockLost = errors.New("lock: đã mất khoá")

	// ErrReleased là nguyên nhân cancel của [Lock.Context] sau khi Release.
	ErrReleased = errors.New("lock: khoá đã được nhả")
)

// Locker giành khoá trên Redis.
type Locker struct {
	rc  *redislock.Client
	log *slog.Logger

	// renewInterval ghi đè chu kỳ gia hạn suy ra từ ttl. 0 nghĩa là dùng
	// ttl / DefaultRenewDivisor.
	renewInterval time.Duration
}

// Option tinh chỉnh Locker.
type Option func(*Locker)

// WithLogger đặt logger. Dùng để ghi log khi gia hạn thất bại.
func WithLogger(l *slog.Logger) Option {
	return func(lk *Locker) {
		if l != nil {
			lk.log = l
		}
	}
}

// WithRenewInterval đặt chu kỳ gia hạn cố định, thay cho ttl/3.
//
// Phải nhỏ hơn ttl một khoảng đủ rộng. Đặt bằng hoặc gần ttl nghĩa là mọi nhịp
// mạng chậm đều thành mất khoá.
func WithRenewInterval(d time.Duration) Option {
	return func(lk *Locker) {
		if d > 0 {
			lk.renewInterval = d
		}
	}
}

// NewLocker dựng Locker trên một client Redis.
func NewLocker(c redis.UniversalClient, opts ...Option) *Locker {
	l := &Locker{rc: redislock.New(c), log: slog.Default()}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Acquire giành khoá, không gia hạn.
//
// Không chờ: khoá đang có người giữ thì trả [ErrNotAcquired] ngay. Chờ sẵn là
// thứ chỗ gọi phải tự quyết — với một cron job thì bỏ qua lượt này là đúng, còn
// với một request thì chờ vài trăm millisecond mới đúng, và không có mặc định
// nào phù hợp cả hai.
//
// Context của khoá có **deadline bằng đúng thời điểm khoá hết hạn**, nên công
// việc tôn trọng context sẽ không bao giờ chạy quá lúc khoá còn hiệu lực. Việc
// dài hơn ttl thì dùng [Locker.AcquireWithRenew].
func (l *Locker) Acquire(ctx context.Context, key string, ttl time.Duration) (*Lock, error) {
	inner, err := l.obtain(ctx, key, ttl)
	if err != nil {
		return nil, err
	}

	// Hai tầng context: tầng ngoài mang deadline (nên ctx.Deadline() của công
	// việc nói đúng thời điểm khoá hết hạn), tầng trong mang cancel-cause (nên
	// Release phân biệt được với hết hạn). Cause của tầng ngoài truyền xuống
	// tầng trong, nên context.Cause vẫn cho ra ErrLockLost khi hết hạn.
	deadlineCtx, cleanup := context.WithDeadlineCause(ctx, time.Now().Add(ttl), ErrLockLost)
	lockCtx, cancel := context.WithCancelCause(deadlineCtx)

	return &Lock{
		inner:   inner,
		key:     key,
		ctx:     lockCtx,
		cancel:  cancel,
		cleanup: cleanup,
		log:     l.log,
	}, nil
}

// AcquireWithRenew giành khoá và gia hạn nó trong nền cho tới khi Release.
//
// Dùng cho việc không biết trước bao lâu. ttl ở đây không phải thời gian tối đa
// của công việc mà là **thời gian khoá sống nếu process chết**: process bị OOM
// kill thì khoá tự mất sau ttl, không treo mãi. Nên chọn ttl ngắn (10–60 giây)
// và để phần gia hạn lo việc kéo dài.
//
// Gia hạn thất bại thì [Lock.Context] bị cancel với cause [ErrLockLost].
func (l *Locker) AcquireWithRenew(ctx context.Context, key string, ttl time.Duration) (*Lock, error) {
	inner, err := l.obtain(ctx, key, ttl)
	if err != nil {
		return nil, err
	}

	lockCtx, cancel := context.WithCancelCause(ctx)
	lk := &Lock{
		inner:  inner,
		key:    key,
		ctx:    lockCtx,
		cancel: cancel,
		log:    l.log,
		stop:   make(chan struct{}),
	}

	go lk.renew(ctx, ttl, l.interval(ttl))
	return lk, nil
}

// obtain gọi redislock và dịch lỗi của nó sang lỗi của package này.
func (l *Locker) obtain(ctx context.Context, key string, ttl time.Duration) (*redislock.Lock, error) {
	if key == "" {
		return nil, errors.New("lock: key rỗng")
	}
	if ttl <= 0 {
		return nil, errors.New("lock: ttl phải lớn hơn 0")
	}

	inner, err := l.rc.Obtain(ctx, key, ttl, nil)
	switch {
	case errors.Is(err, redislock.ErrNotObtained):
		return nil, fmt.Errorf("%w: %s", ErrNotAcquired, key)
	case err != nil:
		return nil, fmt.Errorf("lock: giành khoá %q: %w", key, err)
	}
	return inner, nil
}

// interval tính chu kỳ gia hạn.
func (l *Locker) interval(ttl time.Duration) time.Duration {
	d := l.renewInterval
	if d <= 0 {
		d = ttl / DefaultRenewDivisor
	}
	return max(d, MinRenewInterval)
}

// Lock là một khoá đang được giữ.
type Lock struct {
	inner *redislock.Lock
	key   string
	log   *slog.Logger

	ctx    context.Context
	cancel context.CancelCauseFunc

	// cleanup giải phóng timer của context có deadline. nil với khoá có gia hạn.
	cleanup context.CancelFunc

	// stop đóng goroutine gia hạn. nil với khoá không gia hạn.
	stop chan struct{}

	// release đảm bảo việc nhả khoá chỉ chạy một lần, và mọi lần gọi sau đó
	// nhận lại đúng kết quả của lần đầu.
	release  sync.Once
	relErr   error
	stopOnce sync.Once
}

// Key trả về key của khoá.
func (lk *Lock) Key() string { return lk.key }

// Context trả về context sống đúng bằng thời gian khoá còn hiệu lực.
//
// Nó bị cancel khi: mất khoá (cause [ErrLockLost]), đã Release (cause
// [ErrReleased]), hoặc context truyền vào lúc giành khoá bị cancel.
//
// Truyền context này xuống công việc thay vì context gốc. Đó là toàn bộ lý do
// package này tồn tại: khoá mất thì công việc dừng, không cần ai kiểm tra gì.
func (lk *Lock) Context() context.Context { return lk.ctx }

// Release nhả khoá và cancel [Lock.Context].
//
// Gọi nhiều lần là an toàn: lần đầu làm việc thật, các lần sau trả lại đúng kết
// quả đó.
//
// ctx ở đây nên là context **không bị cancel cùng công việc** — thường là
// context.WithoutCancel(ctx). Dùng chính lk.Context() thì đúng lúc cần nhả khoá
// (công việc vừa xong hoặc vừa bị cancel) lại là lúc context đó đã chết, và
// khoá bị giữ tới hết ttl.
func (lk *Lock) Release(ctx context.Context) error {
	lk.release.Do(func() {
		lk.stopRenew()
		lk.cancel(ErrReleased)
		if lk.cleanup != nil {
			lk.cleanup()
		}

		err := lk.inner.Release(ctx)
		switch {
		case err == nil:
		case errors.Is(err, redislock.ErrLockNotHeld):
			// Khoá đã hết hạn hoặc đã sang tay người khác trước khi nhả. Đây là
			// tình huống cần biết, không phải chuyện vô hại: nghĩa là công việc
			// vừa rồi có thể đã chạy song song với một instance khác.
			lk.relErr = fmt.Errorf("%w: %s", ErrLockLost, lk.key)
		default:
			lk.relErr = fmt.Errorf("lock: nhả khoá %q: %w", lk.key, err)
		}
	})
	return lk.relErr
}

// stopRenew dừng goroutine gia hạn.
func (lk *Lock) stopRenew() {
	if lk.stop == nil {
		return
	}
	lk.stopOnce.Do(func() { close(lk.stop) })
}

// renew gia hạn khoá theo chu kỳ, và cancel context khi thất bại.
func (lk *Lock) renew(parent context.Context, ttl, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-lk.stop:
			return
		case <-parent.Done():
			// Chỗ gọi cancel. lk.ctx là con của parent nên nó cũng đã cancel,
			// và cause là của parent — không phải ErrLockLost, vì khoá không mất.
			return
		case <-ticker.C:
		}

		// Timeout cho mỗi lần gia hạn bằng chu kỳ: chậm hơn thế thì lần gia hạn
		// sau đã tới, và một Refresh treo sẽ giữ vòng lặp lại quá lâu.
		refreshCtx, cancel := context.WithTimeout(parent, every)
		err := lk.inner.Refresh(refreshCtx, ttl, nil)
		cancel()

		if err != nil {
			lk.log.WarnContext(parent, "mất khoá, huỷ context của công việc",
				slog.String("key", lk.key),
				slog.String("error", err.Error()))
			lk.cancel(fmt.Errorf("%w: %s: %w", ErrLockLost, lk.key, err))
			return
		}
	}
}
