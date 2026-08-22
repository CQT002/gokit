package idempotency_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cqt002/gokit/httpx/idempotency"
)

const key = "khoa-idem-1"

func mustMW(t *testing.T, cfg idempotency.Config) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := idempotency.Middleware(cfg)
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}
	return mw
}

// countingHandler đếm số lần thực sự chạy.
func countingHandler(calls *atomic.Int32, status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func post(t *testing.T, h http.Handler, idemKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Tình huống 1: cùng key, cùng payload → trả lại response đã lưu, KHÔNG chạy handler.
func TestCungKeyCungPayload_TraLaiKetQuaCu(t *testing.T) {
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
		countingHandler(&calls, http.StatusCreated, `{"order_id":"od-1"}`))

	first := post(t, h, key, `{"amount":1000}`)
	second := post(t, h, key, `{"amount":1000}`)

	if got := calls.Load(); got != 1 {
		t.Errorf("handler chạy %d lần, muốn 1 — đây là cả lý do package này tồn tại", got)
	}
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Errorf("status = %d rồi %d, muốn 201 cả hai", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("body khác nhau:\n%q\n%q", first.Body.String(), second.Body.String())
	}
	// Thiếu header này thì client và người vận hành không phân biệt được bản phát
	// lại với lần xử lý mới.
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Error("lần thứ hai thiếu header Idempotent-Replay")
	}
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Error("lần đầu không được có header Idempotent-Replay")
	}
	if ct := second.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type không được giữ lại: %q", ct)
	}
}

// Tình huống 2: cùng key, KHÁC payload → 422. Trả về response cũ sẽ tệ hơn nhiều:
// client tưởng payload mới đã được xử lý.
func TestCungKeyKhacPayload_Tra422(t *testing.T) {
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
		countingHandler(&calls, http.StatusCreated, `{"ok":true}`))

	post(t, h, key, `{"amount":1000}`)
	second := post(t, h, key, `{"amount":9999999}`)

	if second.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, muốn 422", second.Code)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler chạy %d lần, payload khác không được chạy", got)
	}
	if !strings.Contains(second.Body.String(), "Idempotency-Key") {
		t.Errorf("body không chỉ ra field nào sai: %s", second.Body.String())
	}
}

// Tình huống 3: request trước đang chạy → 409 kèm Retry-After.
func TestDangXuLy_Tra409(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			close(started)
			<-release
			w.WriteHeader(http.StatusCreated)
		}))

	go func() { post(t, h, key, `{"a":1}`) }()
	<-started

	second := post(t, h, key, `{"a":1}`)
	if second.Code != http.StatusConflict {
		t.Errorf("status = %d, muốn 409", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("thiếu Retry-After — client không biết chờ bao lâu")
	}

	close(release)
	if got := calls.Load(); got != 1 {
		t.Errorf("handler chạy %d lần", got)
	}
}

// Hai request đến cùng lúc: đúng một cái được chạy handler. Đây là điều kiện cốt lõi
// mà Reserve phải là thao tác nguyên tử mới bảo đảm được.
func TestSongSong_ChiMotRequestChayHandler(t *testing.T) {
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			time.Sleep(30 * time.Millisecond)
			w.WriteHeader(http.StatusCreated)
		}))

	const n = 20
	var (
		wg       sync.WaitGroup
		statuses sync.Map
	)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := post(t, h, key, `{"a":1}`)
			statuses.Store(i, rec.Code)
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("handler chạy %d lần với %d request song song, muốn 1", got, n)
	}
}

// Handler panic phải nhả khoá, nếu không client nhận 409 suốt TTL dù không có gì
// đang chạy.
func TestHandlerPanic_NhaKhoa(t *testing.T) {
	store := idempotency.NewMemoryStore()
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{Store: store})(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			calls.Add(1)
			panic("bùm")
		}))

	func() {
		defer func() { _ = recover() }()
		post(t, h, key, `{"a":1}`)
	}()

	// Lần thử lại phải chạy được handler, không nhận 409.
	func() {
		defer func() { _ = recover() }()
		post(t, h, key, `{"a":1}`)
	}()

	if got := calls.Load(); got != 2 {
		t.Errorf("handler chạy %d lần, muốn 2 — khoá không được nhả sau panic", got)
	}
}

// Lỗi 5xx không được lưu: client retry sẽ nhận lại đúng lỗi đó mãi dù nguyên nhân
// đã hết.
func TestKhongLuuKetQuaLoi(t *testing.T) {
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := calls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}))

	if got := post(t, h, key, `{"a":1}`); got.Code != http.StatusInternalServerError {
		t.Fatalf("lần đầu status = %d", got.Code)
	}
	second := post(t, h, key, `{"a":1}`)
	if second.Code != http.StatusCreated {
		t.Errorf("lần hai status = %d, muốn 201 — lỗi 5xx đã bị lưu lại", second.Code)
	}
}

func TestChiApDungChoMethodDaKhai(t *testing.T) {
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{
		Store:   idempotency.NewMemoryStore(),
		Methods: []string{http.MethodPost},
	})(countingHandler(&calls, http.StatusOK, `{}`))

	// GET không bị chặn dù cùng key.
	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/orders", nil)
		req.Header.Set("Idempotency-Key", key)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("GET chạy %d lần, muốn 3 — GET đã idempotent theo đặc tả HTTP", got)
	}
}

func TestThieuHeader(t *testing.T) {
	t.Run("không bắt buộc thì cho qua", func(t *testing.T) {
		var calls atomic.Int32
		h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
			countingHandler(&calls, http.StatusOK, `{}`))

		for range 3 {
			post(t, h, "", `{"a":1}`)
		}
		if got := calls.Load(); got != 3 {
			t.Errorf("chạy %d lần, muốn 3", got)
		}
	})

	t.Run("bắt buộc thì trả 400", func(t *testing.T) {
		var calls atomic.Int32
		h := mustMW(t, idempotency.Config{
			Store:    idempotency.NewMemoryStore(),
			Required: true,
		})(countingHandler(&calls, http.StatusOK, `{}`))

		rec := post(t, h, "", `{"a":1}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, muốn 400", rec.Code)
		}
		if calls.Load() != 0 {
			t.Error("handler vẫn chạy")
		}
	})
}

func TestHeaderTuKhai(t *testing.T) {
	var calls atomic.Int32
	h := mustMW(t, idempotency.Config{
		Store:      idempotency.NewMemoryStore(),
		HeaderName: "X-Request-Token",
	})(countingHandler(&calls, http.StatusOK, `{}`))

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`))
		req.Header.Set("X-Request-Token", key)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("chạy %d lần, muốn 1", got)
	}
}

func TestHandlerVanDocDuocBody(t *testing.T) {
	const payload = `{"amount":1000,"note":"còn nguyên"}`

	var got string
	h := mustMW(t, idempotency.Config{Store: idempotency.NewMemoryStore()})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, len(payload))
			n, _ := r.Body.Read(b)
			got = string(b[:n])
			w.WriteHeader(http.StatusCreated)
		}))

	post(t, h, key, payload)
	if got != payload {
		t.Errorf("handler nhận %q, muốn %q — middleware đã ăn mất body", got, payload)
	}
}

func TestConfigThieuStore(t *testing.T) {
	if _, err := idempotency.Middleware(idempotency.Config{}); err == nil {
		t.Error("thiếu Store không báo lỗi")
	}
}

// ---------- MemoryStore ----------

func TestMemoryStore_ReserveVaCommit(t *testing.T) {
	s := idempotency.NewMemoryStore()
	ctx := context.Background()

	rec, found, err := s.Reserve(ctx, "k", "hash1", time.Minute)
	if err != nil || found || rec != nil {
		t.Fatalf("Reserve lần đầu = (%v, %v, %v)", rec, found, err)
	}

	// Đang xử lý: lần thứ hai phải nhận ErrInFlight.
	if _, _, inFlightErr := s.Reserve(ctx, "k", "hash1", time.Minute); inFlightErr == nil {
		t.Error("Reserve lần hai không trả ErrInFlight")
	}

	want := idempotency.Record{Status: 201, Body: []byte(`{"a":1}`), ReqHash: "hash1"}
	if commitErr := s.Commit(ctx, "k", want, time.Minute); commitErr != nil {
		t.Fatalf("Commit: %v", commitErr)
	}

	got, found, err := s.Reserve(ctx, "k", "hash1", time.Minute)
	if err != nil || !found {
		t.Fatalf("Reserve sau Commit = (%v, %v)", found, err)
	}
	if got.Status != 201 || string(got.Body) != `{"a":1}` {
		t.Errorf("record = %+v", got)
	}
}

// Store copy body: chỗ gọi có thể dùng lại slice đó cho việc khác.
func TestMemoryStore_CopyBody(t *testing.T) {
	s := idempotency.NewMemoryStore()
	ctx := context.Background()

	body := []byte(`{"a":1}`)
	if err := s.Commit(ctx, "k", idempotency.Record{Body: body, ReqHash: "h"}, time.Minute); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	body[2] = 'X' // chỗ gọi dùng lại slice

	got, _, err := s.Reserve(ctx, "k", "h", time.Minute)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if string(got.Body) != `{"a":1}` {
		t.Errorf("body = %q, store đã giữ tham chiếu tới slice của chỗ gọi", got.Body)
	}
}

func TestMemoryStore_Release(t *testing.T) {
	s := idempotency.NewMemoryStore()
	ctx := context.Background()

	if _, _, err := s.Reserve(ctx, "k", "h", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := s.Release(ctx, "k"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, found, err := s.Reserve(ctx, "k", "h", time.Minute); err != nil || found {
		t.Errorf("sau Release: (%v, %v), muốn giành lại được", found, err)
	}
}

// Release không được xoá kết quả đã commit.
func TestMemoryStore_ReleaseKhongXoaKetQua(t *testing.T) {
	s := idempotency.NewMemoryStore()
	ctx := context.Background()

	if err := s.Commit(ctx, "k", idempotency.Record{Status: 201, ReqHash: "h"}, time.Minute); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.Release(ctx, "k"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, found, _ := s.Reserve(ctx, "k", "h", time.Minute); !found {
		t.Error("Release đã xoá kết quả đã commit")
	}
}

func TestMemoryStore_HetHan(t *testing.T) {
	s := idempotency.NewMemoryStore()
	ctx := context.Background()

	if err := s.Commit(ctx, "k", idempotency.Record{Status: 201, ReqHash: "h"}, time.Millisecond); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	if _, found, _ := s.Reserve(ctx, "k", "h", time.Minute); found {
		t.Error("entry hết hạn vẫn còn")
	}
}
