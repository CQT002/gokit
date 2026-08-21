package middleware

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// errRequestsPositive là lỗi cấu hình khi Requests không dương.
var errRequestsPositive = errors.New("middleware: RateLimit cần Requests > 0")

// RateLimitConfig cấu hình RateLimit.
type RateLimitConfig struct {
	// Requests là số request cho phép trong mỗi Window. Phải > 0.
	Requests int

	// Window là khoảng thời gian của hạn mức. <= 0 thì dùng 1 giây.
	Window time.Duration

	// Burst là số request cho phép dồn cục. <= 0 thì bằng Requests.
	//
	// Không có burst thì client gửi hai request liền nhau đã bị chặn dù hạn mức
	// theo phút còn rất nhiều — token bucket cần chỗ chứa.
	Burst int

	// KeyFunc quyết định hạn mức tính theo cái gì. nil thì tính theo IP client.
	//
	// Với API đã xác thực, tính theo user ID công bằng hơn IP: nhiều người dùng
	// sau cùng một NAT sẽ dùng chung một IP.
	KeyFunc func(*http.Request) string

	// MaxKeys là số key tối đa giữ trong bộ nhớ, <= 0 thì dùng DefaultMaxKeys.
	//
	// Đây là trần bắt buộc chứ không phải tối ưu: hạn mức theo IP với một map không
	// giới hạn là một đường làm hết bộ nhớ, và người tấn công chỉ cần đổi IP nguồn.
	MaxKeys int

	// IdleTTL là thời gian một key không dùng thì bị dọn, <= 0 dùng DefaultIdleTTL.
	IdleTTL time.Duration
}

// Giá trị mặc định của RateLimitConfig.
const (
	DefaultMaxKeys = 100_000
	DefaultIdleTTL = 10 * time.Minute
)

// RateLimit giới hạn số request theo key, trong phạm vi một process.
//
// "Trong phạm vi một process" là giới hạn cần nói rõ: chạy 4 replica thì hạn mức
// thực tế gấp 4. Nó vẫn đủ cho việc chặn một client đơn lẻ làm quá tải một instance,
// nhưng hạn mức nghiệp vụ chính xác thì cần bản dùng Redis dùng chung.
//
// Trả 429 kèm Retry-After và các header X-RateLimit-*.
func RateLimit(cfg RateLimitConfig) (Middleware, error) {
	if cfg.Requests <= 0 {
		return nil, errRequestsPositive
	}

	window := cfg.Window
	if window <= 0 {
		window = time.Second
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = cfg.Requests
	}
	maxKeys := cfg.MaxKeys
	if maxKeys <= 0 {
		maxKeys = DefaultMaxKeys
	}
	idleTTL := cfg.IdleTTL
	if idleTTL <= 0 {
		idleTTL = DefaultIdleTTL
	}
	keyFunc := cfg.KeyFunc
	if keyFunc == nil {
		keyFunc = clientIP
	}

	limit := rate.Limit(float64(cfg.Requests) / window.Seconds())
	store := &limiterStore{
		limit:   limit,
		burst:   burst,
		maxKeys: maxKeys,
		idleTTL: idleTTL,
		byKey:   make(map[string]*keyedLimiter),
	}

	retryAfter := strconv.Itoa(max(1, int(window.Seconds())))
	limitHeader := strconv.Itoa(cfg.Requests)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lim := store.get(keyFunc(r))

			w.Header().Set("X-RateLimit-Limit", limitHeader)
			if !lim.Allow() {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, "vượt hạn mức số request", http.StatusTooManyRequests)
				return
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(lim.Tokens())))
			next.ServeHTTP(w, r)
		})
	}, nil
}

// keyedLimiter là limiter của một key cùng mốc thời gian dùng gần nhất.
type keyedLimiter struct {
	*rate.Limiter
	lastSeen time.Time
}

// limiterStore giữ limiter theo key, có trần số lượng và dọn key cũ.
type limiterStore struct {
	limit   rate.Limit
	burst   int
	maxKeys int
	idleTTL time.Duration

	mu    sync.Mutex
	byKey map[string]*keyedLimiter
}

func (s *limiterStore) get(key string) *rate.Limiter {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if kl, ok := s.byKey[key]; ok {
		kl.lastSeen = now
		return kl.Limiter
	}

	if len(s.byKey) >= s.maxKeys {
		s.evictLocked(now)
	}

	kl := &keyedLimiter{Limiter: rate.NewLimiter(s.limit, s.burst), lastSeen: now}
	s.byKey[key] = kl
	return kl.Limiter
}

// evictLocked dọn key đã lâu không dùng; nếu vẫn không dọn được gì thì xoá sạch.
//
// Xoá sạch là lựa chọn có chủ ý cho tình huống bị tấn công bằng nhiều key khác nhau:
// mất trạng thái hạn mức trong một nhịp còn hơn để map lớn không giới hạn rồi hết
// bộ nhớ. Không dùng LRU vì nó cần thêm một cấu trúc dữ liệu nữa cho một đường
// hiếm khi chạy tới.
func (s *limiterStore) evictLocked(now time.Time) {
	for key, kl := range s.byKey {
		if now.Sub(kl.lastSeen) > s.idleTTL {
			delete(s.byKey, key)
		}
	}
	if len(s.byKey) >= s.maxKeys {
		s.byKey = make(map[string]*keyedLimiter, s.maxKeys)
	}
}
