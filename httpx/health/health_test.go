package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cqt002/gokit/httpx/health"
)

func call(t *testing.T, h http.Handler) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body không phải JSON: %v\n%s", err, rec.Body.String())
	}
	return rec.Code, body
}

// /healthz cố tình KHÔNG kiểm tra dependency: nối liveness với database nghĩa là
// database chập một nhịp sẽ làm Kubernetes restart toàn bộ pod cùng lúc.
func TestLive_KhongKiemTraDependency(t *testing.T) {
	h := health.NewHealth()
	h.Register("db", func(context.Context) error { return errors.New("db chết") })

	status, body := call(t, h.Live())
	if status != http.StatusOK {
		t.Errorf("status = %d, muốn 200 — liveness không được phụ thuộc dependency", status)
	}
	if body["status"] != "ok" {
		t.Errorf("body = %#v", body)
	}
}

func TestReady_MoiThuKhoe(t *testing.T) {
	h := health.NewHealth()
	h.Register("db", func(context.Context) error { return nil })
	h.Register("redis", func(context.Context) error { return nil })

	status, body := call(t, h.Ready())
	if status != http.StatusOK {
		t.Errorf("status = %d", status)
	}
	checks, ok := body["checks"].(map[string]any)
	if !ok || checks["db"] != "ok" || checks["redis"] != "ok" {
		t.Errorf("checks = %#v", body["checks"])
	}
}

// Body phải nói rõ checker nào lỗi: người vận hành thấy 503 mà không biết vì sao
// thì endpoint này gần như vô dụng.
func TestReady_MotDependencyLoi(t *testing.T) {
	h := health.NewHealth()
	h.Register("db", func(context.Context) error { return nil })
	h.Register("redis", func(context.Context) error { return errors.New("connection refused") })

	status, body := call(t, h.Ready())
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, muốn 503", status)
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %#v", body["status"])
	}
	checks := body["checks"].(map[string]any)
	if checks["db"] != "ok" {
		t.Errorf("db = %#v", checks["db"])
	}
	if checks["redis"] != "connection refused" {
		t.Errorf("redis = %#v, muốn nội dung lỗi", checks["redis"])
	}
}

func TestReady_KhongCoCheckerNao(t *testing.T) {
	status, _ := call(t, health.NewHealth().Ready())
	if status != http.StatusOK {
		t.Errorf("status = %d, không có checker thì coi là sẵn sàng", status)
	}
}

// SetNotReady là bước đầu tiên của shutdown: rút pod khỏi load balancer trước khi
// đóng server.
func TestSetNotReady(t *testing.T) {
	h := health.NewHealth()
	h.Register("db", func(context.Context) error { return nil })

	if status, _ := call(t, h.Ready()); status != http.StatusOK {
		t.Fatalf("status ban đầu = %d", status)
	}

	h.SetNotReady()
	status, body := call(t, h.Ready())
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, muốn 503 sau SetNotReady", status)
	}
	if body["status"] != "shutting_down" {
		t.Errorf("body = %#v", body)
	}
	if h.IsReady() {
		t.Error("IsReady vẫn true")
	}

	// Liveness vẫn phải OK: pod đang dừng có kiểm soát, không phải pod chết.
	if status, _ := call(t, h.Live()); status != http.StatusOK {
		t.Errorf("liveness = %d, không được fail khi đang shutdown", status)
	}

	h.SetReady()
	if !h.IsReady() {
		t.Error("SetReady không có tác dụng")
	}
}

// Checker treo không được làm cả endpoint treo: probe của Kubernetes có timeout riêng.
func TestReady_CheckerTreoBiCatTheoTimeout(t *testing.T) {
	h := health.NewHealth()
	h.Timeout = 30 * time.Millisecond
	h.Register("treo", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	start := time.Now()
	status, _ := call(t, h.Ready())
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("mất %v — timeout không có tác dụng", elapsed)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, muốn 503", status)
	}
}

// Checker của app có thể panic, và một panic ở đây biến việc kiểm tra sức khoẻ
// thành nguyên nhân gây sự cố.
func TestReady_CheckerPanic(t *testing.T) {
	h := health.NewHealth()
	h.Register("panic", func(context.Context) error { panic("bùm") })
	h.Register("ok", func(context.Context) error { return nil })

	status, body := call(t, h.Ready())
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, muốn 503", status)
	}
	checks := body["checks"].(map[string]any)
	if checks["panic"] == nil {
		t.Errorf("checker panic không có kết quả: %#v", checks)
	}
	if checks["ok"] != "ok" {
		t.Errorf("checker khác bị ảnh hưởng: %#v", checks)
	}
}

// Chạy song song: thời gian đáp ứng phải xấp xỉ checker chậm nhất, không phải tổng.
func TestReady_ChayCheckerSongSong(t *testing.T) {
	h := health.NewHealth()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		h.Register(name, func(context.Context) error {
			time.Sleep(50 * time.Millisecond)
			return nil
		})
	}

	start := time.Now()
	if status, _ := call(t, h.Ready()); status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	elapsed := time.Since(start)

	// Tuần tự sẽ mất 250ms.
	if elapsed > 200*time.Millisecond {
		t.Errorf("mất %v — checker đang chạy tuần tự", elapsed)
	}
}

func TestRegister_NilBiBoQua(t *testing.T) {
	h := health.NewHealth()
	h.Register("nil", nil)

	status, body := call(t, h.Ready())
	if status != http.StatusOK {
		t.Errorf("status = %d", status)
	}
	if checks, ok := body["checks"].(map[string]any); ok && len(checks) != 0 {
		t.Errorf("checker nil vẫn được đăng ký: %#v", checks)
	}
}

func TestRegister_GhiDeCungTen(t *testing.T) {
	h := health.NewHealth()
	h.Register("db", func(context.Context) error { return errors.New("cũ") })
	h.Register("db", func(context.Context) error { return nil })

	if status, _ := call(t, h.Ready()); status != http.StatusOK {
		t.Errorf("status = %d, checker mới phải thay checker cũ", status)
	}
}

func TestHandle(t *testing.T) {
	h := health.NewHealth()
	mux := http.NewServeMux()
	h.Handle(mux)

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, rec.Code)
		}
	}
}
