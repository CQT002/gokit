package idemstore_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache/idemstore"
	"github.com/cqt002/gokit/httpx/idempotency"
)

// newStore dựng Store trên một Redis giả.
func newStore(t *testing.T) (*idemstore.Store, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	t.Cleanup(func() { _ = rdb.Close() })

	s, err := idemstore.New(idemstore.Config{Redis: rdb})
	if err != nil {
		t.Fatalf("idemstore.New: %v", err)
	}
	return s, mr
}

func TestNew_ThieuRedis(t *testing.T) {
	if _, err := idemstore.New(idemstore.Config{}); err == nil {
		t.Fatal("thiếu Redis mà New không báo lỗi")
	}
}

func TestReserve_LanDau(t *testing.T) {
	s, mr := newStore(t)

	rec, found, err := s.Reserve(t.Context(), "k1", "hash-1", time.Minute)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if found || rec != nil {
		t.Errorf("found = %v, rec = %+v — key mới phải chưa có gì", found, rec)
	}

	keys := mr.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], idemstore.DefaultPrefix) {
		t.Errorf("keys = %v, muốn một key có tiền tố %q", keys, idemstore.DefaultPrefix)
	}
}

// Kiểm tra rồi đánh dấu phải là **một** thao tác. Hai request đến cùng lúc mà cả
// hai đều thấy "chưa có" là đúng cái mà lớp idempotency tồn tại để ngăn.
func TestReserve_LanHaiBaoDangXuLy(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()

	if _, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	_, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute)
	if !errors.Is(err, idempotency.ErrInFlight) {
		t.Fatalf("err = %v, muốn ErrInFlight", err)
	}
}

func TestReserve_NguyenTuKhiChayDongThoi(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()

	const n = 30
	var won atomic.Int32
	var inFlight atomic.Int32
	done := make(chan struct{})

	for range n {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute)
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, idempotency.ErrInFlight):
				inFlight.Add(1)
			default:
				t.Errorf("Reserve: %v", err)
			}
		}()
	}
	for range n {
		<-done
	}

	if got := won.Load(); got != 1 {
		t.Errorf("%d request cùng giành được quyền xử lý, muốn 1", got)
	}
	if got := inFlight.Load(); got != n-1 {
		t.Errorf("%d request nhận ErrInFlight, muốn %d", got, n-1)
	}
}

func TestCommitVaPhatLai(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()

	if _, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	want := idempotency.Record{
		Status:  http.StatusCreated,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"id":"gd-1"}`),
		ReqHash: "hash-1",
	}
	if err := s.Commit(ctx, "k1", want, time.Hour); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	rec, found, err := s.Reserve(ctx, "k1", "hash-1", time.Minute)
	if err != nil {
		t.Fatalf("Reserve sau Commit: %v", err)
	}
	if !found {
		t.Fatal("found = false sau Commit")
	}
	if rec.Status != want.Status {
		t.Errorf("Status = %d, muốn %d", rec.Status, want.Status)
	}
	if string(rec.Body) != string(want.Body) {
		t.Errorf("Body = %s, muốn %s", rec.Body, want.Body)
	}
	if rec.Headers["Content-Type"] != "application/json" {
		t.Errorf("Headers = %v", rec.Headers)
	}
	if rec.ReqHash != want.ReqHash {
		t.Errorf("ReqHash = %q — không có nó thì không phát hiện được việc dùng lại khoá", rec.ReqHash)
	}
}

// Không có Release thì một request panic sẽ khoá key tới hết TTL, và client nhận
// 409 suốt thời gian đó dù chẳng có gì đang chạy.
func TestRelease_ChoPhepThuLai(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()

	if _, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := s.Release(ctx, "k1"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	_, found, err := s.Reserve(ctx, "k1", "hash-1", time.Minute)
	if err != nil {
		t.Fatalf("Reserve sau Release: %v", err)
	}
	if found {
		t.Error("found = true sau Release — Release không được để lại kết quả")
	}
}

// Release sau Commit **không** được xoá kết quả: xoá nghĩa là lần gửi lại tiếp
// theo sẽ chạy handler lần nữa.
func TestRelease_SauCommitKhongXoaKetQua(t *testing.T) {
	s, _ := newStore(t)
	ctx := t.Context()

	if _, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	rec := idempotency.Record{Status: 201, Body: []byte("ok"), ReqHash: "hash-1"}
	if err := s.Commit(ctx, "k1", rec, time.Hour); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := s.Release(ctx, "k1"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	got, found, err := s.Reserve(ctx, "k1", "hash-1", time.Minute)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if !found {
		t.Fatal("kết quả đã bị Release xoá mất")
	}
	if string(got.Body) != "ok" {
		t.Errorf("Body = %s", got.Body)
	}
}

func TestRelease_KeyKhongTonTai(t *testing.T) {
	s, _ := newStore(t)

	if err := s.Release(t.Context(), "khong-co"); err != nil {
		t.Errorf("Release trên key không tồn tại: %v", err)
	}
}

func TestReserve_HetHan(t *testing.T) {
	s, mr := newStore(t)
	ctx := t.Context()

	if _, _, err := s.Reserve(ctx, "k1", "hash-1", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	mr.FastForward(2 * time.Minute)

	_, found, err := s.Reserve(ctx, "k1", "hash-1", time.Minute)
	if err != nil {
		t.Fatalf("Reserve sau khi hết hạn: %v", err)
	}
	if found {
		t.Error("cờ đang xử lý không hết hạn theo TTL")
	}
}

// TTL của kết quả tính từ lúc có kết quả, không phải từ lúc Reserve.
func TestCommit_DatLaiTTL(t *testing.T) {
	s, mr := newStore(t)
	ctx := t.Context()

	if _, _, err := s.Reserve(ctx, "k1", "hash-1", time.Second); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	rec := idempotency.Record{Status: 200, ReqHash: "hash-1"}
	if err := s.Commit(ctx, "k1", rec, time.Hour); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	mr.FastForward(10 * time.Second)

	_, found, err := s.Reserve(ctx, "k1", "hash-1", time.Second)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if !found {
		t.Error("kết quả hết hạn theo TTL của Reserve thay vì của Commit")
	}
}

func TestPrefix(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	defer rdb.Close()

	s, err := idemstore.New(idemstore.Config{Redis: rdb, Prefix: "tt:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err := s.Reserve(t.Context(), "k1", "h", time.Minute); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	if keys := mr.Keys(); len(keys) != 1 || keys[0] != "tt:k1" {
		t.Errorf("keys = %v, muốn [tt:k1]", keys)
	}
}

// Test đầu-cuối với chính middleware: bằng chứng là Store này thoả đúng hợp đồng
// mà httpx/idempotency mong đợi, không chỉ thoả từng method rời rạc.
func TestVoiMiddleware_PhatLaiThayVeChayLai(t *testing.T) {
	s, _ := newStore(t)

	var calls atomic.Int32
	mw, err := idempotency.Middleware(idempotency.Config{Store: s})
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"gd-1"}`))
	}))

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"amount":1000}`))
		req.Header.Set("Idempotency-Key", "key-1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	first := do()
	if first.Code != http.StatusCreated {
		t.Fatalf("lần đầu: status = %d, body = %s", first.Code, first.Body)
	}

	second := do()
	if second.Code != http.StatusCreated {
		t.Fatalf("lần hai: status = %d", second.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("body lần hai = %s, muốn giống lần đầu", second.Body)
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Error("thiếu header Idempotent-Replay ở lần phát lại")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("handler chạy %d lần, muốn 1", got)
	}
}

// Dùng lại cùng một khoá cho payload khác là lỗi phía client, và trả về response
// cũ sẽ tệ hơn nhiều — client tưởng payload mới đã được xử lý.
func TestVoiMiddleware_CungKhoaKhacPayload(t *testing.T) {
	s, _ := newStore(t)

	mw, err := idempotency.Middleware(idempotency.Config{Store: s})
	if err != nil {
		t.Fatalf("Middleware: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	do := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "key-1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do(`{"amount":1000}`); code != http.StatusCreated {
		t.Fatalf("lần đầu: %d", code)
	}
	if code := do(`{"amount":9999}`); code == http.StatusCreated {
		t.Error("payload khác mà vẫn trả 201 — client tưởng số tiền mới đã được xử lý")
	}
}
