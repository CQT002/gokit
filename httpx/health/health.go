// Package health cung cấp endpoint liveness và readiness.
//
// Phân biệt hai loại này là điều kiện để triển khai trên Kubernetes không gây sự cố:
//
//   - liveness (/healthz) fail → kubelet **restart pod**;
//   - readiness (/readyz) fail → pod bị **rút khỏi load balancer** nhưng vẫn chạy.
//
// Gộp hai thứ lại là một sai lầm đắt: nối health check với việc kiểm tra database
// nghĩa là database chập một nhịp sẽ làm Kubernetes restart toàn bộ pod cùng lúc,
// biến một sự cố nhỏ thành sự cố toàn hệ thống. Vì vậy /healthz ở đây **không**
// kiểm tra dependency nào.
package health

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Checker kiểm tra một dependency. Trả nil nghĩa là khoẻ.
//
// Phải tôn trọng context: Ready gọi mọi checker với deadline, và một checker treo
// sẽ làm cả endpoint treo.
type Checker func(context.Context) error

// DefaultTimeout là thời gian tối đa cho toàn bộ một lần kiểm tra readiness.
const DefaultTimeout = 2 * time.Second

// Health quản lý trạng thái sống và sẵn sàng của service.
type Health struct {
	// notReady dùng atomic vì SetNotReady được gọi từ goroutine xử lý tín hiệu
	// shutdown, còn Ready đọc từ goroutine của mỗi request.
	notReady atomic.Bool

	// Timeout là deadline cho một lần kiểm tra readiness. <= 0 dùng DefaultTimeout.
	Timeout time.Duration

	mu       sync.RWMutex
	checkers map[string]Checker
}

// NewHealth tạo Health chưa có checker nào.
func NewHealth() *Health {
	return &Health{checkers: make(map[string]Checker)}
}

// Register thêm hoặc thay một checker cho readiness.
//
// Chỉ đăng ký những dependency mà **thiếu nó thì service không phục vụ được**. Một
// dependency chỉ dùng cho một endpoint phụ mà lại nằm trong readiness sẽ rút cả pod
// khỏi load balancer khi nó chập.
func (h *Health) Register(name string, c Checker) {
	if c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checkers[name] = c
}

// SetNotReady đánh dấu service không còn sẵn sàng nhận traffic mới.
//
// Gọi ngay khi bắt đầu shutdown, **trước** khi đóng server. Thứ tự đúng: SetNotReady
// → chờ load balancer rút traffic (vài giây) → đóng server. Bỏ bước chờ thì những
// request đang trên đường sẽ nhận connection reset.
func (h *Health) SetNotReady() { h.notReady.Store(true) }

// SetReady đánh dấu lại là sẵn sàng. Dùng khi service tự phục hồi.
func (h *Health) SetReady() { h.notReady.Store(false) }

// IsReady cho biết service có đang nhận traffic không.
func (h *Health) IsReady() bool { return !h.notReady.Load() }

// Live trả handler cho /healthz.
//
// Luôn trả 200 khi process còn chạy. Cố tình không kiểm tra dependency: xem godoc
// của package để biết vì sao.
func (h *Health) Live() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
}

// Ready trả handler cho /readyz.
//
// Chạy mọi checker song song và trả 200 nếu tất cả khoẻ, 503 nếu có cái nào lỗi
// hoặc SetNotReady đã được gọi. Body liệt kê từng checker cùng lỗi của nó — không
// có phần này thì người vận hành thấy 503 mà không biết vì sao.
//
// Chạy song song vì các checker độc lập với nhau; chạy tuần tự thì thời gian đáp
// ứng bằng tổng, và probe của Kubernetes có timeout riêng.
func (h *Health) Ready() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.IsReady() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "shutting_down",
			})
			return
		}

		timeout := h.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		h.mu.RLock()
		checkers := maps.Clone(h.checkers)
		h.mu.RUnlock()

		results := make(map[string]string, len(checkers))
		var (
			mu   sync.Mutex
			wg   sync.WaitGroup
			fail bool
		)
		for name, check := range checkers {
			wg.Add(1)
			go func(name string, check Checker) {
				defer wg.Done()

				// Checker của app có thể panic; một panic ở đây làm sập process và
				// biến việc kiểm tra sức khoẻ thành nguyên nhân gây sự cố.
				var err error
				func() {
					defer func() {
						if v := recover(); v != nil {
							err = &panicError{value: v}
						}
					}()
					err = check(ctx)
				}()

				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					results[name] = err.Error()
					fail = true
				} else {
					results[name] = "ok"
				}
			}(name, check)
		}
		wg.Wait()

		status, label := http.StatusOK, "ok"
		if fail {
			status, label = http.StatusServiceUnavailable, "degraded"
		}
		writeJSON(w, status, map[string]any{"status": label, "checks": results})
	})
}

// panicError bọc giá trị panic của một checker thành error.
type panicError struct{ value any }

func (e *panicError) Error() string { return "checker panic" }

// Handle gắn /healthz và /readyz vào mux.
func (h *Health) Handle(mux *http.ServeMux) {
	mux.Handle("GET /healthz", h.Live())
	mux.Handle("GET /readyz", h.Ready())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
