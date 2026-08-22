// Package retry thử lại một thao tác với backoff luỹ tiến và jitter.
//
// Một cài đặt dùng chung cho HTTP client, Kafka consumer và DB reconnect — ba chỗ
// mà nếu để mỗi nơi tự viết thì sẽ có ba định nghĩa khác nhau của "lỗi tạm thời"
// và ba cách tính delay khác nhau.
//
// Hai điều package này bắt buộc làm đúng:
//
//   - Tôn trọng context. Mọi lần chờ đều thoát ngay khi context bị cancel, nên
//     một request đã bị client hủy không nằm đó chờ hết 5 lần thử.
//   - Có jitter. Không có jitter thì N instance cùng gặp lỗi sẽ cùng thử lại ở
//     đúng một mốc thời gian, và dịch vụ vừa hồi phục lại bị dập xuống lần nữa.
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"time"
)

// Giá trị mặc định cho Policy chưa khai.
const (
	DefaultMaxAttempts = 3
	DefaultBaseDelay   = 100 * time.Millisecond
	DefaultMaxDelay    = 30 * time.Second
	DefaultMultiplier  = 2.0
	DefaultJitter      = 0.2
)

// Policy là tham số của việc thử lại.
//
// Giá trị zero dùng được ngay: 3 lần thử, delay đầu 100ms, nhân đôi mỗi lần, trần
// 30s, jitter 20%, và thử lại mọi lỗi trừ lỗi do context.
type Policy struct {
	// MaxAttempts là tổng số lần gọi, tính cả lần đầu. 1 nghĩa là không thử lại.
	// <= 0 dùng DefaultMaxAttempts.
	MaxAttempts int

	// BaseDelay là thời gian chờ trước lần thử thứ hai. <= 0 dùng DefaultBaseDelay.
	BaseDelay time.Duration

	// MaxDelay là trần cho một lần chờ, áp sau khi đã tính jitter.
	// <= 0 dùng DefaultMaxDelay.
	MaxDelay time.Duration

	// Multiplier là hệ số nhân delay sau mỗi lần thất bại. < 1 bị nâng lên 1:
	// hệ số nhỏ hơn 1 làm delay co lại sau mỗi lần lỗi, gần như luôn là lỗi khai.
	// 1 nghĩa là delay không đổi, và đó là lựa chọn hợp lệ.
	Multiplier float64

	// Jitter là biên độ ngẫu nhiên quanh delay, 0..1. 0.2 nghĩa là delay thật rơi
	// vào khoảng ±20% quanh giá trị tính được.
	//
	// Giá trị 0 (tức chưa khai) dùng DefaultJitter, không phải "tắt jitter": mất
	// jitter làm hỏng đúng cái tính chất mà package này tồn tại để bảo đảm, còn
	// bị jitter khi không mong đợi thì không gây hại gì. Muốn tắt hẳn thì khai số
	// âm. Giá trị lớn hơn 1 bị kẹp về 1.
	Jitter float64

	// Retryable quyết định lỗi nào đáng thử lại. nil nghĩa là dùng
	// DefaultRetryable: thử lại mọi lỗi trừ lỗi do context.
	//
	// Đây là chỗ để chặn việc thử lại những lỗi không bao giờ tự khỏi — 400, 401,
	// vi phạm ràng buộc unique — vì thử lại chúng chỉ tốn thời gian và nhân bản
	// tải lên hệ thống đang lỗi.
	Retryable func(error) bool
}

// DefaultRetryable thử lại mọi lỗi trừ lỗi phát sinh từ context bị cancel hoặc
// hết hạn.
//
// Lỗi context nghĩa là chính chỗ gọi đã bỏ cuộc; thử lại lúc đó là làm việc cho
// một kết quả không ai còn chờ.
func DefaultRetryable(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// Do gọi fn, thử lại theo p cho tới khi thành công, hết lượt, hoặc gặp lỗi không
// đáng thử lại.
//
// Trả về lỗi của lần thử cuối. Nếu context kết thúc trong lúc đang chờ, lỗi trả
// về gộp cả lỗi lần cuối và lỗi của context bằng errors.Join, nên errors.Is kiểm
// tra được cả hai.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context) error) error {
	_, err := DoValue(ctx, p, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// DoValue như Do nhưng fn trả về một giá trị.
//
// Giá trị trả về chỉ có nghĩa khi err là nil; các trường hợp lỗi đều trả về giá
// trị zero của T.
func DoValue[T any](ctx context.Context, p Policy, fn func(ctx context.Context) (T, error)) (T, error) {
	p = p.normalize()

	var zero T
	if err := ctx.Err(); err != nil {
		// Không gọi fn lần nào: context đã kết thúc trước khi bắt đầu.
		return zero, err
	}

	var lastErr error
	for attempt := 1; ; attempt++ {
		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}
		lastErr = err

		if attempt >= p.MaxAttempts || !p.Retryable(err) {
			return zero, lastErr
		}
		if err := wait(ctx, p.delayFor(attempt)); err != nil {
			return zero, errors.Join(lastErr, err)
		}
	}
}

// delayFor tính thời gian chờ trước lần thử thứ attempt+1.
func (p Policy) delayFor(attempt int) time.Duration {
	// Tính bằng float64 rồi mới kẹp: BaseDelay * Multiplier^attempt tràn khỏi
	// int64 rất nhanh, và một Duration âm do tràn số sẽ thành lần chờ 0 giây.
	d := float64(p.BaseDelay) * math.Pow(p.Multiplier, float64(attempt-1))
	maxD := float64(p.MaxDelay)
	if d > maxD || math.IsInf(d, 0) || math.IsNaN(d) {
		d = maxD
	}

	if p.Jitter > 0 {
		// math/rand là đúng chỗ ở đây: jitter chỉ cần rải đều các instance, không
		// phải chống ai đoán. Dùng crypto/rand cho việc này là trả giá vô ích.
		//nolint:gosec // jitter không phải giá trị cần bảo mật
		d *= 1 + p.Jitter*(2*rand.Float64()-1)
	}

	if d > maxD {
		d = maxD
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// normalize điền giá trị mặc định và kẹp các tham số về khoảng hợp lệ.
func (p Policy) normalize() Policy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = DefaultMaxAttempts
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = DefaultBaseDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultMaxDelay
	}
	if p.Multiplier == 0 {
		p.Multiplier = DefaultMultiplier
	}
	if p.Multiplier < 1 {
		p.Multiplier = 1
	}
	switch {
	case p.Jitter == 0:
		p.Jitter = DefaultJitter
	case p.Jitter < 0:
		p.Jitter = 0 // khai số âm là cách tắt jitter tường minh
	case p.Jitter > 1:
		p.Jitter = 1
	}
	if p.Retryable == nil {
		p.Retryable = DefaultRetryable
	}
	return p
}

// wait chờ d, hoặc thoát sớm nếu context kết thúc.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
