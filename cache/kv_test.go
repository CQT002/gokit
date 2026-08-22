package cache_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cqt002/gokit/cache"
	"github.com/cqt002/gokit/core/errs"
)

func TestGetSet_Struct(t *testing.T) {
	c, mr := newClient(t)
	ctx := t.Context()

	want := user{ID: "u-1", Name: "An", Age: 30}
	if err := c.Set(ctx, "u", want, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Struct lưu dạng JSON: đọc bằng redis-cli hay bằng service khác đều được.
	if raw := rawValue(t, mr, "u"); !strings.HasPrefix(raw, "{") {
		t.Errorf("giá trị lưu = %q, muốn JSON", raw)
	}

	var got user
	if err := c.Get(ctx, "u", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("got = %+v, muốn %+v", got, want)
	}
}

// string và []byte lưu thô: giá trị đếm và dữ liệu do ngôn ngữ khác ghi vào phải
// đọc được, và `redis-cli get` không được trả về chuỗi có dấu nháy.
func TestGetSet_ChuoiVaBytesLuuTho(t *testing.T) {
	c, mr := newClient(t)
	ctx := t.Context()

	if err := c.Set(ctx, "s", "xin chào", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if raw := rawValue(t, mr, "s"); raw != "xin chào" {
		t.Errorf("giá trị lưu = %q, muốn thô", raw)
	}

	if err := c.Set(ctx, "b", []byte{1, 2, 3}, 0); err != nil {
		t.Fatalf("Set bytes: %v", err)
	}
	var gotB []byte
	if err := c.Get(ctx, "b", &gotB); err != nil {
		t.Fatalf("Get bytes: %v", err)
	}
	if string(gotB) != "\x01\x02\x03" {
		t.Errorf("gotB = %v", gotB)
	}

	var gotS string
	if err := c.Get(ctx, "s", &gotS); err != nil {
		t.Fatalf("Get string: %v", err)
	}
	if gotS != "xin chào" {
		t.Errorf("gotS = %q", gotS)
	}
}

// bool là chỗ cách của go-redis sai: nó ghi "1", và đọc lại vào *bool là lỗi.
func TestGetSet_BoolVaSoRoundTrip(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.Set(ctx, "flag", true, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var flag bool
	if err := c.Get(ctx, "flag", &flag); err != nil {
		t.Fatalf("Get bool: %v", err)
	}
	if !flag {
		t.Error("bool không round-trip")
	}

	if err := c.Set(ctx, "n", 3.5, 0); err != nil {
		t.Fatalf("Set float: %v", err)
	}
	var n float64
	if err := c.Get(ctx, "n", &n); err != nil {
		t.Fatalf("Get float: %v", err)
	}
	if n != 3.5 {
		t.Errorf("n = %v", n)
	}
}

func TestGetSet_TimeRoundTrip(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	want := time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)
	if err := c.Set(ctx, "t", want, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got time.Time
	if err := c.Get(ctx, "t", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got = %v, muốn %v", got, want)
	}
}

func TestGet_Miss(t *testing.T) {
	c, _ := newClient(t)

	var got user
	err := c.Get(t.Context(), "khong-co", &got)
	if !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("err = %v, muốn ErrMiss", err)
	}
	// Mã lỗi riêng, không phải not_found: hai chuyện đó khác nhau.
	if !errs.Is(err, cache.CodeMiss) {
		t.Errorf("mã lỗi = %v, muốn %v", err, cache.CodeMiss)
	}
	if errs.Is(err, errs.CodeNotFound) {
		t.Error("cache miss bị coi là not_found — sẽ thành 404 trả cho client")
	}
}

func TestGet_DstKhongPhaiConTro(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.Set(ctx, "u", user{ID: "u-1"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got user
	if err := c.Get(ctx, "u", got); err == nil {
		t.Error("dst không phải con trỏ mà không báo lỗi")
	}
	if err := c.Get(ctx, "u", nil); err == nil {
		t.Error("dst nil mà không báo lỗi")
	}
	var p *user
	if err := c.Get(ctx, "u", p); err == nil {
		t.Error("dst là con trỏ nil mà không báo lỗi")
	}
}

// Thông báo lỗi không được vọng lại nội dung đang nằm trong cache: nó đi thẳng
// vào log.
func TestGet_LoiGiaiMaKhongLoDuLieu(t *testing.T) {
	c, mr := newClient(t)

	mr.Set("u", "so-dien-thoai-0912345678")

	var got user
	err := c.Get(t.Context(), "u", &got)
	if err == nil {
		t.Fatal("giá trị rác mà Get không báo lỗi")
	}
	if strings.Contains(err.Error(), "0912345678") {
		t.Errorf("thông báo lỗi chứa dữ liệu trong cache: %v", err)
	}
}

func TestSet_TTL(t *testing.T) {
	c, mr := newClient(t)
	ctx := t.Context()

	if err := c.Set(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	ttl, err := c.TTL(ctx, "k")
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Errorf("ttl = %v", ttl)
	}

	mr.FastForward(2 * time.Minute)
	var got string
	if err := c.Get(ctx, "k", &got); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("key đã hết hạn mà vẫn đọc được: err = %v", err)
	}
}

// Redis nhồi hai tình huống vào hai giá trị âm của cùng một lệnh. Trả nguyên
// giá trị âm ra ngoài là cách chắc chắn có người đem nó đi cộng vào một mốc thời
// gian.
func TestTTL_PhanBietKhongCoVaKhongHan(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if _, err := c.TTL(ctx, "khong-co"); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("key không tồn tại: err = %v, muốn ErrMiss", err)
	}

	if err := c.Set(ctx, "khong-han", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	ttl, err := c.TTL(ctx, "khong-han")
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl != 0 {
		t.Errorf("ttl = %v, muốn 0 cho key không có hạn", ttl)
	}
}

func TestSetNX(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	ok, err := c.SetNX(ctx, "k", "dau-tien", time.Minute)
	if err != nil {
		t.Fatalf("SetNX: %v", err)
	}
	if !ok {
		t.Fatal("lần đầu SetNX phải ghi được")
	}

	ok, err = c.SetNX(ctx, "k", "thu-hai", time.Minute)
	if err != nil {
		t.Fatalf("SetNX: %v", err)
	}
	if ok {
		t.Error("lần hai SetNX không được ghi")
	}

	var got string
	if err := c.Get(ctx, "k", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "dau-tien" {
		t.Errorf("got = %q, giá trị đã bị ghi đè", got)
	}
}

func TestDelVaExists(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	for _, k := range []string{"a", "b"} {
		if err := c.Set(ctx, k, "v", 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	n, err := c.Exists(ctx, "a", "b", "c")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if n != 2 {
		t.Errorf("Exists = %d, muốn 2", n)
	}

	// Xoá cả key không tồn tại: không phải lỗi.
	if err := c.Del(ctx, "a", "khong-co"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if n, _ := c.Exists(ctx, "a"); n != 0 {
		t.Error("Del không xoá được")
	}

	// Danh sách rỗng không được gửi lệnh sai cú pháp xuống Redis.
	if err := c.Del(ctx); err != nil {
		t.Errorf("Del() rỗng: %v", err)
	}
	if n, err := c.Exists(ctx); err != nil || n != 0 {
		t.Errorf("Exists() rỗng: %d, %v", n, err)
	}
}

func TestExpire(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.Set(ctx, "k", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Expire(ctx, "k", time.Minute); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if ttl, _ := c.TTL(ctx, "k"); ttl <= 0 {
		t.Errorf("ttl = %v sau Expire", ttl)
	}

	if err := c.Expire(ctx, "khong-co", time.Minute); !errors.Is(err, cache.ErrMiss) {
		t.Errorf("Expire trên key không tồn tại: err = %v, muốn ErrMiss", err)
	}
}

func TestIncr(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	n, err := c.Incr(ctx, "counter", 5)
	if err != nil {
		t.Fatalf("Incr: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, muốn 5 (key chưa có coi như 0)", n)
	}

	if n, _ = c.Incr(ctx, "counter", -2); n != 3 {
		t.Errorf("n = %d, muốn 3", n)
	}

	// Giá trị đếm phải đọc lại được: đó là lý do string đi thô.
	var got int
	if err := c.Get(ctx, "counter", &got); err != nil {
		t.Fatalf("Get counter: %v", err)
	}
	if got != 3 {
		t.Errorf("got = %d", got)
	}
}

func TestMGet(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	if err := c.Set(ctx, "u:1", user{ID: "1", Name: "An"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Set(ctx, "u:3", user{ID: "3", Name: "Cường"}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	var got []user
	if err := c.MGet(ctx, []string{"u:1", "u:2", "u:3"}, &got); err != nil {
		t.Fatalf("MGet: %v", err)
	}

	// Đúng độ dài và đúng thứ tự: đó là điều kiện để ghép lại được với danh sách
	// key ban đầu.
	if len(got) != 3 {
		t.Fatalf("len = %d, muốn 3", len(got))
	}
	if got[0].Name != "An" || got[2].Name != "Cường" {
		t.Errorf("got = %+v", got)
	}
	if got[1] != (user{}) {
		t.Errorf("key thiếu phải cho giá trị zero, nhận %+v", got[1])
	}
}

func TestMGet_KeyRong(t *testing.T) {
	c, _ := newClient(t)

	got := []user{{ID: "cu"}}
	if err := c.MGet(t.Context(), nil, &got); err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, muốn slice rỗng", got)
	}
}

func TestMGet_DstSai(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	var notSlice user
	if err := c.MGet(ctx, []string{"a"}, &notSlice); err == nil {
		t.Error("dst không phải slice mà không báo lỗi")
	}
	if err := c.MGet(ctx, []string{"a"}, []user{}); err == nil {
		t.Error("dst không phải con trỏ mà không báo lỗi")
	}
}

func TestScan(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()

	for _, k := range []string{"u:1", "u:2", "other"} {
		if err := c.Set(ctx, k, "v", 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	seen := map[string]bool{}
	var cursor uint64
	for {
		keys, next, err := c.Scan(ctx, "u:*", cursor, 10)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		for _, k := range keys {
			seen[k] = true
		}
		if next == 0 {
			break
		}
		cursor = next
	}

	if !seen["u:1"] || !seen["u:2"] {
		t.Errorf("Scan bỏ sót key: %v", seen)
	}
	if seen["other"] {
		t.Errorf("Scan trả về key ngoài pattern: %v", seen)
	}
}

// Ở cluster, SCAN chỉ quét một node. Trả kết quả thiếu trong im lặng tệ hơn
// nhiều so với trả lỗi.
func TestScan_ClusterBaoLoi(t *testing.T) {
	c, err := cache.New(cache.Config{Addrs: []string{"127.0.0.1:6379", "127.0.0.1:6380"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	_, _, err = c.Scan(t.Context(), "*", 0, 10)
	if err == nil {
		t.Fatal("Scan trên cluster mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "ForEachMaster") {
		t.Errorf("lỗi không chỉ ra cách làm đúng: %v", err)
	}
}

func TestDecode(t *testing.T) {
	var got user
	if err := cache.Decode([]byte(`{"id":"u-1","name":"An"}`), &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.ID != "u-1" || got.Name != "An" {
		t.Errorf("got = %+v", got)
	}

	var raw json.RawMessage
	if err := cache.Decode([]byte(`{"a":1}`), &raw); err != nil {
		t.Fatalf("Decode RawMessage: %v", err)
	}
	if string(raw) != `{"a":1}` {
		t.Errorf("raw = %s", raw)
	}
}
