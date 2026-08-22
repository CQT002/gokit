package client

import (
	"errors"
	"sync"
	"time"
)

// ErrBreakerOpen là lỗi trả về khi circuit breaker đang mở.
//
// Phân biệt được với lỗi mạng bằng errors.Is, và chỗ gọi cần phân biệt: breaker mở
// nghĩa là "đừng thử nữa, đích đang lỗi", còn lỗi mạng đơn lẻ thì thử lại được.
var ErrBreakerOpen = errors.New("client: circuit breaker đang mở")

// BreakerState là trạng thái của circuit breaker.
type BreakerState int

// Các trạng thái của breaker.
const (
	// StateClosed là trạng thái bình thường: request đi qua.
	StateClosed BreakerState = iota
	// StateOpen là trạng thái chặn: request bị từ chối ngay, không gọi ra ngoài.
	StateOpen
	// StateHalfOpen là trạng thái thử lại: cho một số request đi qua để xem đích
	// đã hồi phục chưa.
	StateHalfOpen
)

// String cài đặt fmt.Stringer.
func (s BreakerState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// BreakerConfig cấu hình circuit breaker.
type BreakerConfig struct {
	// FailureThreshold là số lần lỗi liên tiếp để mở breaker. <= 0 thì dùng 5.
	FailureThreshold int

	// OpenDuration là thời gian giữ breaker mở trước khi thử lại. <= 0 thì dùng 30s.
	OpenDuration time.Duration

	// HalfOpenMaxCalls là số request cho đi qua ở trạng thái nửa mở. <= 0 thì dùng 1.
	//
	// Giữ nhỏ: mục đích là thăm dò, không phải phục vụ. Cho quá nhiều request đi
	// qua khi đích vẫn đang lỗi sẽ dập nó xuống lần nữa ngay khi vừa hồi phục.
	HalfOpenMaxCalls int

	// SuccessThreshold là số lần thành công ở trạng thái nửa mở để đóng lại.
	// <= 0 thì dùng 1.
	SuccessThreshold int
}

// breaker là circuit breaker theo mẫu ba trạng thái.
//
// Lý do cần nó: khi một dịch vụ phía sau chết, mỗi request đi ra sẽ chờ hết timeout
// rồi mới lỗi. Với timeout 10 giây và 100 request mỗi giây, chỉ trong một phút là
// có 60 nghìn goroutine đang chờ — service của mình chết theo dịch vụ kia dù bản
// thân không có lỗi gì. Breaker cắt mạch để lỗi trả về ngay lập tức.
type breaker struct {
	failureThreshold int
	openDuration     time.Duration
	halfOpenMaxCalls int
	successThreshold int

	mu            sync.Mutex
	state         BreakerState
	failures      int
	successes     int
	halfOpenCalls int
	openedAt      time.Time
	now           func() time.Time
}

func newBreaker(cfg BreakerConfig) *breaker {
	b := &breaker{
		failureThreshold: cfg.FailureThreshold,
		openDuration:     cfg.OpenDuration,
		halfOpenMaxCalls: cfg.HalfOpenMaxCalls,
		successThreshold: cfg.SuccessThreshold,
		now:              time.Now,
	}
	if b.failureThreshold <= 0 {
		b.failureThreshold = 5
	}
	if b.openDuration <= 0 {
		b.openDuration = 30 * time.Second
	}
	if b.halfOpenMaxCalls <= 0 {
		b.halfOpenMaxCalls = 1
	}
	if b.successThreshold <= 0 {
		b.successThreshold = 1
	}
	return b
}

// allow cho biết request có được phép đi ra không.
func (b *breaker) allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return nil

	case StateOpen:
		if b.now().Sub(b.openedAt) < b.openDuration {
			return ErrBreakerOpen
		}
		// Hết thời gian mở: sang nửa mở và cho request này đi thăm dò.
		b.state = StateHalfOpen
		b.halfOpenCalls = 1
		b.successes = 0
		return nil

	case StateHalfOpen:
		if b.halfOpenCalls >= b.halfOpenMaxCalls {
			return ErrBreakerOpen
		}
		b.halfOpenCalls++
		return nil

	default:
		return nil
	}
}

// record ghi nhận kết quả một request.
func (b *breaker) record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if success {
		switch b.state {
		case StateHalfOpen:
			b.successes++
			if b.successes >= b.successThreshold {
				b.state = StateClosed
				b.failures = 0
				b.successes = 0
				b.halfOpenCalls = 0
			}
		default:
			b.failures = 0
		}
		return
	}

	b.failures++
	if b.state == StateHalfOpen || b.failures >= b.failureThreshold {
		// Lỗi ở trạng thái nửa mở mở lại ngay, không cần đủ ngưỡng: đích vừa cho
		// biết nó chưa hồi phục.
		b.state = StateOpen
		b.openedAt = b.now()
		b.halfOpenCalls = 0
		b.successes = 0
	}
}

// currentState trả về trạng thái hiện tại, dùng cho metric và test.
func (b *breaker) currentState() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
