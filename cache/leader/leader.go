// Package leader chọn đúng một instance trong nhóm làm leader, và cancel context
// của nó ngay khi nó mất quyền.
//
// Dùng khi một việc chỉ được chạy một lần dù service có bao nhiêu replica: gửi
// báo cáo cuối ngày, dọn dữ liệu cũ, chạy scheduler. Không có nó thì mười pod
// gửi mười email giống nhau.
//
// So với cách thường thấy — bốn callback elected/notElected/demoted/error —
// ở đây chỉ có **một** hàm onLead nhận context. Mất quyền leader thì context bị
// cancel, và công việc dừng như mọi công việc tôn trọng context. Không có
// callback nào để quên cài, và không có trạng thái nào để đọc sai.
//
//	e, err := leader.NewElector(leader.ElectorConfig{
//	    Key:   "report-service",
//	    Redis: c.Redis(),
//	})
//	if err != nil { return err }
//
//	// Chạy tới khi ctx cancel. Khi nào là leader thì onLead được gọi.
//	return e.Run(ctx, func(ctx context.Context) error { return scheduler.Run(ctx) })
package leader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache/lock"
	"github.com/cqt002/gokit/core/idx"
)

// Giá trị mặc định của ElectorConfig.
const (
	// DefaultTTL là thời gian quyền leader còn hiệu lực nếu không gia hạn.
	//
	// 15 giây là đánh đổi giữa hai thứ: ngắn hơn thì mọi nhịp mạng chậm đều
	// thành một lần đổi leader, dài hơn thì lúc pod leader bị kill đột ngột sẽ
	// không có ai làm việc trong khoảng đó.
	DefaultTTL = 15 * time.Second

	// DefaultRenewInterval là chu kỳ gia hạn và cũng là chu kỳ thử giành quyền
	// khi đang không phải leader. 0 trong config nghĩa là TTL / 3.
	DefaultRenewDivisor = 3

	// keyPrefix để khoá của leader election không lẫn với khoá nghiệp vụ khác
	// trong cùng một Redis.
	keyPrefix = "gokit:leader:"
)

// ElectorConfig cấu hình Elector.
type ElectorConfig struct {
	// Key phân biệt các cuộc bầu khác nhau. Bắt buộc.
	//
	// Mọi instance muốn tranh cùng một quyền phải dùng **cùng** Key, và hai
	// việc khác nhau phải dùng hai Key khác nhau.
	Key string

	// TTL là thời gian quyền leader còn hiệu lực nếu không gia hạn được.
	// 0 → DefaultTTL.
	TTL time.Duration

	// RenewInterval là chu kỳ gia hạn, và cũng là chu kỳ thử giành quyền khi
	// đang không phải leader. 0 → TTL / DefaultRenewDivisor.
	RenewInterval time.Duration

	// Redis là client Redis. Bắt buộc.
	Redis redis.UniversalClient

	// Logger ghi log các lần đổi quyền. nil thì dùng slog.Default().
	Logger *slog.Logger
}

// Elector tranh quyền leader và giữ nó bằng cách gia hạn.
type Elector struct {
	key      string
	ttl      time.Duration
	interval time.Duration
	locker   *lock.Locker
	log      *slog.Logger

	// id phân biệt instance này trong log. Không dùng để so khớp khoá — phần đó
	// do token của cache/lock lo.
	id string

	// leader dùng atomic vì IsLeader được gọi từ goroutine khác với Run —
	// thường là từ handler /readyz.
	leader atomic.Bool
}

// NewElector dựng Elector từ cấu hình.
//
// Trả lỗi thay vì panic khi thiếu Key hoặc Redis: đó là lỗi cấu hình, và nó
// phải lộ ra lúc khởi động chứ không phải ở vòng bầu đầu tiên. Đặc tả ở
// plan-code.md không có giá trị lỗi; thêm vào để nhất quán với httpx.NewServer
// và db.Open.
func NewElector(cfg ElectorConfig) (*Elector, error) {
	if cfg.Key == "" {
		return nil, errors.New("leader: ElectorConfig thiếu Key")
	}
	if cfg.Redis == nil {
		return nil, errors.New("leader: ElectorConfig thiếu Redis")
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	interval := cfg.RenewInterval
	if interval <= 0 {
		interval = ttl / DefaultRenewDivisor
	}
	if interval >= ttl {
		return nil, fmt.Errorf("leader: RenewInterval (%v) phải nhỏ hơn TTL (%v)", interval, ttl)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	return &Elector{
		key:      keyPrefix + cfg.Key,
		ttl:      ttl,
		interval: interval,
		locker:   lock.NewLocker(cfg.Redis, lock.WithLogger(log), lock.WithRenewInterval(interval)),
		log:      log.With(slog.String("election", cfg.Key)),
		id:       idx.NewUUIDv7(),
	}, nil
}

// IsLeader cho biết instance này có đang là leader không.
//
// Đọc được từ goroutine khác — thường là handler /readyz. Nhưng đừng dùng nó để
// bảo vệ một đoạn code: giữa lúc đọc true và lúc chạy, quyền leader có thể đã
// mất. Thứ dùng để bảo vệ công việc là context mà Run truyền cho onLead.
func (e *Elector) IsLeader() bool { return e.leader.Load() }

// Run tranh quyền leader tới khi ctx bị cancel.
//
// Vòng đời:
//
//   - Không giành được quyền → chờ RenewInterval rồi thử lại. Không phải lỗi:
//     nghĩa là instance khác đang làm.
//   - Giành được → gọi onLead(leadCtx) và **chờ nó xong**. leadCtx bị cancel khi
//     mất quyền hoặc khi ctx bị cancel.
//   - onLead trả về sau khi mất quyền → nhả khoá và tranh lại từ đầu.
//   - onLead trả về một lỗi không phải lỗi context → Run trả lỗi đó ra ngoài.
//     Một công việc thất bại thật thì để cả service chết còn hơn để nó im lặng
//     không chạy, cùng lý do như httpx.App.
//
// Trả nil khi dừng vì ctx bị cancel, khớp với httpx.App.Run nên ghép thẳng vào
// App được.
func (e *Elector) Run(ctx context.Context, onLead func(context.Context) error) error {
	if onLead == nil {
		return errors.New("leader: Run cần onLead")
	}

	for {
		// ctx đã cancel: dừng sạch, trả nil (xem godoc).
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		lk, err := e.locker.AcquireWithRenew(ctx, e.key, e.ttl)
		switch {
		case errors.Is(err, lock.ErrNotAcquired):
			if !sleep(ctx, e.interval) {
				return nil
			}
			continue

		case err != nil:
			// Redis không truy cập được. Ghi log và thử lại — một sự cố Redis
			// vài giây không nên làm cả service chết, và trong lúc đó cũng
			// không ai là leader nên không có nguy cơ chạy trùng.
			e.log.WarnContext(ctx, "không tranh được quyền leader",
				slog.String("error", err.Error()))
			if !sleep(ctx, e.interval) {
				return nil
			}
			continue
		}

		if err := e.lead(ctx, lk, onLead); err != nil {
			return err
		}
	}
}

// lead chạy onLead trong một nhiệm kỳ leader, rồi nhả khoá.
func (e *Elector) lead(ctx context.Context, lk *lock.Lock, onLead func(context.Context) error) error {
	e.leader.Store(true)
	e.log.InfoContext(ctx, "trở thành leader", slog.String("instance", e.id))

	defer func() {
		e.leader.Store(false)

		// WithoutCancel: lúc cần nhả khoá thì thường ctx đã cancel (SIGTERM),
		// và dùng ctx đó để nhả nghĩa là khoá bị giữ tới hết TTL — instance
		// khác phải chờ vô ích đúng khoảng đó.
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		if err := lk.Release(releaseCtx); err != nil {
			e.log.WarnContext(ctx, "nhả quyền leader không thành công",
				slog.String("error", err.Error()))
		}
		e.log.InfoContext(ctx, "thôi làm leader", slog.String("instance", e.id))
	}()

	err := onLead(lk.Context())
	switch {
	case err == nil:
		// onLead chủ động kết thúc. Nhả quyền rồi tranh lại: nếu công việc thật
		// sự đã xong hẳn thì chỗ gọi cancel ctx, không phải trả nil từ onLead.
		return nil

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// Mất quyền leader, hoặc chỗ gọi cancel. Cả hai đều không phải lỗi của
		// công việc — vòng lặp ở Run xử lý tiếp.
		return nil

	default:
		return fmt.Errorf("leader: công việc của leader thất bại: %w", err)
	}
}

// sleep chờ d, hoặc tới khi ctx cancel. Trả false nếu ctx đã cancel.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
