package log_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/core/secret"
)

// ---------- Lớp 1: chặn theo kích thước ----------

func TestLop1_ElideTheoKichThuoc(t *testing.T) {
	const maxLen = 32
	cfg := log.MaskConfig{MaxLen: maxLen}

	t.Run("ngắn thì giữ nguyên", func(t *testing.T) {
		got := log.SafeMap(map[string]any{"note": "vừa đủ ngắn"}, cfg)
		if got["note"] != "vừa đủ ngắn" {
			t.Errorf("note = %#v, muốn giữ nguyên", got["note"])
		}
	})

	t.Run("đúng ngưỡng thì vẫn giữ nguyên", func(t *testing.T) {
		exact := strings.Repeat("a", maxLen)
		got := log.SafeMap(map[string]any{"note": exact}, cfg)
		if got["note"] != exact {
			t.Errorf("chuỗi dài đúng MaxLen phải được giữ, được %#v", got["note"])
		}
	})

	t.Run("vượt ngưỡng thì thành metadata", func(t *testing.T) {
		long := strings.Repeat("a", maxLen+1)
		got := log.SafeMap(map[string]any{"note": long}, cfg)

		e := elidedOf(t, got["note"])
		if n := elidedBytes(t, e); n != int64(maxLen+1) {
			t.Errorf("bytes = %d, muốn %d", n, maxLen+1)
		}
		sha := elidedSHA(t, e)
		if sha == "" {
			t.Error("sha256 rỗng, mặc định phải có hash")
		}
		if len(sha) != 8 {
			t.Errorf("sha256 = %q, muốn 8 hex đầu", sha)
		}
		if !strings.Contains(string(mustJSON(t, e)), `"_elided"`) {
			t.Error("JSON không có key _elided")
		}
	})
}

// Nhãn chỉ để người đọc log dễ hiểu, nhưng sai nhãn thì gây hiểu nhầm nội dung.
func TestLop1_NhanLoaiNoiDung(t *testing.T) {
	cfg := log.MaskConfig{MaxLen: 16}
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"base64 không padding", strings.Repeat("QUJD", 10), log.LabelBase64},
		{"base64 có padding", strings.Repeat("QUJD", 10) + "==", log.LabelBase64},
		{"data URI", "data:image/png;base64," + strings.Repeat("A", 40), log.LabelDataURI},
		{"data URI viết hoa", "DATA:image/png;base64," + strings.Repeat("A", 40), log.LabelDataURI},
		{"văn bản thường", strings.Repeat("xin chào ", 10), log.LabelText},
		{"văn bản có ký tự ngoài bảng base64", strings.Repeat("a-b_c!", 10), log.LabelText},
		{"chỉ có dấu bằng", strings.Repeat("=", 20), log.LabelText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := log.SafeMap(map[string]any{"v": tt.value}, cfg)
			if nhan := elidedOf(t, got["v"])["_elided"]; nhan != tt.want {
				t.Errorf("nhãn = %#v, muốn %q", nhan, tt.want)
			}
		})
	}
}

// Cùng nội dung phải ra cùng hash — đó là cả mục đích của việc kèm hash: đối chiếu
// được "client gửi trùng file hai lần".
func TestLop1_HashOnDinhVaPhanBiet(t *testing.T) {
	cfg := log.MaskConfig{MaxLen: 8}
	hashOf := func(s string) string {
		return elidedSHA(t, elidedOf(t, log.SafeMap(map[string]any{"v": s}, cfg)["v"]))
	}

	a := strings.Repeat("noi dung mot", 10)
	b := strings.Repeat("noi dung hai", 10)

	lan1, lan2 := hashOf(a), hashOf(a)
	if lan1 != lan2 {
		t.Errorf("cùng nội dung ra hai hash khác nhau: %q rồi %q", lan1, lan2)
	}
	if lan1 == hashOf(b) {
		t.Error("nội dung khác nhau ra cùng hash")
	}
}

func TestLop1_TatHash(t *testing.T) {
	cfg := log.MaskConfig{MaxLen: 8, DisableElideHash: true}
	e := elidedOf(t, log.SafeMap(map[string]any{"v": strings.Repeat("a", 100)}, cfg)["v"])
	if sha := elidedSHA(t, e); sha != "" {
		t.Errorf("sha256 = %q, muốn rỗng khi DisableElideHash", sha)
	}
	if n := elidedBytes(t, e); n != 100 {
		t.Errorf("bytes = %d, muốn vẫn báo kích thước", n)
	}
}

// MaxLen <= 0 phải là "dùng mặc định", không phải "tắt lưới an toàn".
func TestLop1_MaxLenKhongDuongDungMacDinh(t *testing.T) {
	for _, maxLen := range []int{0, -1} {
		cfg := log.MaskConfig{MaxLen: maxLen}
		short := strings.Repeat("a", log.DefaultMaxLen)
		long := strings.Repeat("a", log.DefaultMaxLen+1)

		got := log.SafeMap(map[string]any{"short": short, "long": long}, cfg)
		if got["short"] != short {
			t.Errorf("MaxLen=%d: chuỗi %d ký tự bị che oan", maxLen, len(short))
		}
		if m, ok := got["long"].(map[string]any); !ok || m["_elided"] == nil {
			t.Errorf("MaxLen=%d: chuỗi %d ký tự không bị elide — lưới an toàn đã tắt", maxLen, len(long))
		}
	}
}

// Cắt theo byte sẽ làm đứt ký tự UTF-8; tiếng Việt thì ký tự nào cũng nhiều byte.
func TestLop1_TiengVietTinhTheoRune(t *testing.T) {
	cfg := log.MaskConfig{MaxLen: 100}
	// 90 ký tự tiếng Việt = 180 byte: vượt MaxLen nếu tính byte, không vượt nếu
	// tính rune.
	s := strings.Repeat("ế", 90)

	got := log.SafeMap(map[string]any{"note": s}, cfg)
	if got["note"] != s {
		t.Errorf("chuỗi 90 rune bị che oan vì đếm theo byte: %#v", got["note"])
	}
}

// ---------- Lớp 2: tag log: trên struct ----------

type allRules struct {
	Plain    string `json:"plain"`
	Redact   string `json:"redact"    log:"redact"`
	Elide    string `json:"elide"     log:"elide"`
	Truncate string `json:"truncate"  log:"truncate=10"`
	Edges    string `json:"card_no"   log:"edges=6,4"`
	Hash     string `json:"hash"      log:"hash"`
	Omit     string `json:"omit"      log:"omit"`
	Skipped  string `json:"-"`
}

func TestLop2_MoiRule(t *testing.T) {
	in := allRules{
		Plain:    "để nguyên",
		Redact:   "mật khẩu thật",
		Elide:    strings.Repeat("QUJD", 10),
		Truncate: "một câu dài hơn mười ký tự rất nhiều",
		Edges:    "4111111111111111",
		Hash:     "giá trị cần đối chiếu",
		Omit:     "không được xuất hiện",
		Skipped:  "cũng không được xuất hiện",
	}
	got := safeGroup(t, in)

	if got["plain"] != "để nguyên" {
		t.Errorf("plain = %#v, muốn giữ nguyên", got["plain"])
	}
	if got["redact"] != "********" {
		t.Errorf("redact = %#v, muốn ********", got["redact"])
	}
	if e, ok := got["elide"].(map[string]any); !ok || e["_elided"] != "base64" {
		t.Errorf("elide = %#v, muốn metadata nhãn base64", got["elide"])
	}
	if s, _ := got["truncate"].(string); !strings.HasSuffix(s, "…") || len([]rune(s)) != 11 {
		t.Errorf("truncate = %#v, muốn 10 rune cộng dấu …", got["truncate"])
	}
	if got["card_no"] != "411111******1111" {
		t.Errorf("card_no = %#v, muốn 411111******1111", got["card_no"])
	}
	if s, _ := got["hash"].(string); len(s) != 64 {
		t.Errorf("hash = %#v, muốn 64 hex", got["hash"])
	}
	if _, ok := got["omit"]; ok {
		t.Error("field log:\"omit\" vẫn xuất hiện trong log")
	}
	if _, ok := got["Skipped"]; ok {
		t.Error("field json:\"-\" không khai tag log vẫn xuất hiện")
	}
}

func TestLop2_TruncateGiuNguyenKhiNganHon(t *testing.T) {
	type doc struct {
		Note string `json:"note" log:"truncate=100"`
	}
	got := safeGroup(t, doc{Note: "ngắn"})
	if got["note"] != "ngắn" {
		t.Errorf("note = %#v, chuỗi ngắn hơn N không được thêm dấu …", got["note"])
	}
}

// Nếu head+tail đã phủ hết giá trị thì edges không che được gì — phải che sạch
// thay vì để lộ nguyên giá trị chỉ vì nó ngắn hơn dự kiến.
func TestLop2_EdgesTrenGiaTriQuaNgan(t *testing.T) {
	type card struct {
		No string `json:"no" log:"edges=6,4"`
	}
	for _, v := range []string{"", "123", "1234567890"} {
		got := safeGroup(t, card{No: v})
		if got["no"] != "********" {
			t.Errorf("no = %#v với input %q, muốn che sạch", got["no"], v)
		}
	}
}

func TestLop2_EdgesMacDinhChiGiuBonKyTuCuoi(t *testing.T) {
	type acct struct {
		No string `json:"no" log:"edges"`
	}
	got := safeGroup(t, acct{No: "0123456789"})
	if s, _ := got["no"].(string); !strings.HasSuffix(s, "6789") || strings.Contains(s, "0123") {
		t.Errorf("no = %#v, muốn chỉ còn 4 ký tự cuối", got["no"])
	}
}

// Tag sai cú pháp phải nghiêng về che, và output ******** là dấu hiệu test nhận ra ngay.
func TestLop2_TagSaiCuPhapThiChe(t *testing.T) {
	type bad struct {
		A string `json:"a" log:"truncate=abc"`
		B string `json:"b" log:"edges=6"`
		C string `json:"c" log:"edges=x,y"`
		D string `json:"d" log:"khong_ton_tai"`
		E string `json:"e" log:"truncate=-5"`
	}
	got := safeGroup(t, bad{A: "aaaa", B: "bbbb", C: "cccc", D: "dddd", E: "eeee"})
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		if got[k] != "********" {
			t.Errorf("%s = %#v, tag sai cú pháp phải thành ********", k, got[k])
		}
	}
}

type inner struct {
	Token string `json:"token" log:"redact"`
	Note  string `json:"note"`
}

type outer struct {
	Name     string  `json:"name"`
	Inner    inner   `json:"inner"`
	InnerPtr *inner  `json:"inner_ptr"`
	NilPtr   *inner  `json:"nil_ptr"`
	List     []inner `json:"list"`
	NilList  []inner `json:"nil_list"`
	Count    int     `json:"count"`
	Ratio    float64 `json:"ratio"`
	Flag     bool    `json:"flag"`
	When     time.Time
}

func TestLop2_LongNhauVaCacKieuKhac(t *testing.T) {
	got := safeGroup(t, outer{
		Name:     "tên",
		Inner:    inner{Token: "bí mật 1", Note: "ghi chú 1"},
		InnerPtr: &inner{Token: "bí mật 2", Note: "ghi chú 2"},
		List: []inner{
			{Token: "bí mật 3", Note: "ghi chú 3"},
			{Token: "bí mật 4", Note: "ghi chú 4"},
		},
		Count: 42,
		Ratio: 1.5,
		Flag:  true,
		When:  time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC),
	})

	raw := string(mustJSON(t, got))
	for i := 1; i <= 4; i++ {
		if strings.Contains(raw, "bí mật") {
			t.Fatalf("token lọt ra log ở độ sâu nào đó: %s", raw)
		}
	}

	if in, ok := got["inner"].(map[string]any); !ok || in["token"] != "********" {
		t.Errorf("inner = %#v", got["inner"])
	}
	if in, ok := got["inner_ptr"].(map[string]any); !ok || in["token"] != "********" {
		t.Errorf("inner_ptr = %#v", got["inner_ptr"])
	}
	if got["nil_ptr"] != nil {
		t.Errorf("nil_ptr = %#v, muốn null", got["nil_ptr"])
	}
	if got["nil_list"] != nil {
		t.Errorf("nil_list = %#v, muốn null", got["nil_list"])
	}

	list, ok := got["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("list = %#v, muốn 2 phần tử", got["list"])
	}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok || m["token"] != "********" {
			t.Errorf("list[%d] = %#v, token phải bị che trong slice của struct", i, item)
		}
	}

	if got["count"] != float64(42) {
		t.Errorf("count = %#v, số phải giữ nguyên", got["count"])
	}
	if got["ratio"] != 1.5 || got["flag"] != true {
		t.Errorf("ratio = %#v, flag = %#v", got["ratio"], got["flag"])
	}
	// time.Time không có field export nào; walk vào sẽ ra object rỗng.
	if s, _ := got["When"].(string); !strings.HasPrefix(s, "2026-08-21") {
		t.Errorf("When = %#v, muốn dạng text của time.Time", got["When"])
	}
}

type embedBase struct {
	ID    string `json:"id"`
	Token string `json:"token" log:"redact"`
}

type embedChild struct {
	embedBase
	Name string `json:"name"`
}

// Trải phẳng field nhúng giống encoding/json, để dòng log cùng hình dạng với body.
func TestLop2_FieldNhung(t *testing.T) {
	got := safeGroup(t, embedChild{embedBase: embedBase{ID: "id-1", Token: "bí mật"}, Name: "tên"})

	if got["id"] != "id-1" || got["name"] != "tên" {
		t.Errorf("field nhúng không được trải phẳng: %#v", got)
	}
	if got["token"] != "********" {
		t.Errorf("token = %#v, tag trên field nhúng phải có tác dụng", got["token"])
	}
	if _, nested := got["embedBase"]; nested {
		t.Error("field nhúng bị lồng thành object con")
	}
}

// Lớp 1 vẫn áp lên kết quả của lớp 2: lưới an toàn có ngoại lệ thì không còn là lưới.
func TestLop2_Lop1VanApLenKetQuaCuaTag(t *testing.T) {
	type doc struct {
		Note string `json:"note" log:"truncate=1000"`
	}
	logger, buf := newTestLogger(t, log.Options{Mask: log.MaskConfig{MaxLen: 64}})
	logger.Info("m", "body", log.Safe(doc{Note: strings.Repeat("a", 500)}))

	body := jsonField(t, buf.String(), "body")
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body = %#v", body)
	}
	note, ok := m["note"].(map[string]any)
	if !ok || note["_elided"] == nil {
		t.Errorf("note = %#v, truncate=1000 với MaxLen=64 vẫn phải bị elide", m["note"])
	}
}

// Cache theo reflect.Type: hai type khác nhau có field cùng tên không được lẫn plan.
func TestLop2_CacheKhongNhiemGiuaCacType(t *testing.T) {
	type typeA struct {
		Value string `json:"value" log:"redact"`
	}
	type typeB struct {
		Value string `json:"value"`
	}

	// Lượt đầu nạp cache cho từng type, lượt sau đọc từ cache.
	for range 2 {
		a := safeGroup(t, typeA{Value: "phải che"})
		if a["value"] != "********" {
			t.Errorf("typeA.value = %#v, muốn ********", a["value"])
		}
		b := safeGroup(t, typeB{Value: "không che"})
		if b["value"] != "không che" {
			t.Errorf("typeB.value = %#v — plan của typeA đã nhiễm sang typeB", b["value"])
		}
	}
}

func TestLop2_SafeVoiGiaTriKhongPhaiStruct(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"nil", nil},
		{"chuỗi", "chuỗi thường"},
		{"số", 42},
		{"slice chuỗi", []string{"a", "b"}},
		{"con trỏ nil", (*inner)(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, buf := newTestLogger(t, log.Options{})
			logger.Info("m", "v", log.Safe(tt.in))
			if buf.Len() == 0 {
				t.Fatal("không ghi được dòng log nào")
			}
			if !json.Valid(buf.Bytes()) {
				t.Errorf("dòng log không phải JSON hợp lệ: %s", buf.String())
			}
		})
	}
}

// Cấu trúc trỏ vòng lại chính nó không được làm sập process.
func TestLop2_CauTrucVongLap(t *testing.T) {
	type node struct {
		Name string `json:"name"`
		Next *node  `json:"next"`
	}
	a := &node{Name: "a"}
	b := &node{Name: "b", Next: a}
	a.Next = b

	logger, buf := newTestLogger(t, log.Options{})
	logger.Info("m", "v", log.Safe(a))

	if !json.Valid(buf.Bytes()) {
		t.Errorf("dòng log không hợp lệ: %s", buf.String())
	}
}

// ---------- Lớp 3: theo tên field ----------

func TestLop3_KhopTenFieldMoiDoSau(t *testing.T) {
	payload := map[string]any{
		"password": "cấp 1",
		"level2": map[string]any{
			"token": "cấp 2",
			"level3": map[string]any{
				"otp": "cấp 3",
				"list": []any{
					map[string]any{"cvv": "trong slice"},
					map[string]any{"api_key": "trong slice 2"},
				},
			},
		},
		"users": []any{
			map[string]any{"name": "an", "access_token": "trong slice của map"},
		},
	}
	got := log.SafeMap(payload, log.MaskConfig{})

	raw := string(mustJSON(t, got))
	for _, leaked := range []string{"cấp 1", "cấp 2", "cấp 3", "trong slice", "trong slice 2", "trong slice của map"} {
		if strings.Contains(raw, leaked) {
			t.Errorf("giá trị %q lọt ra: %s", leaked, raw)
		}
	}
	if !strings.Contains(raw, "an") {
		t.Error("field không nhạy cảm bị che oan")
	}
}

func TestLop3_KhopKhongPhanBietHoaThuongVaGachNoi(t *testing.T) {
	payload := map[string]any{
		"Password":      "a",
		"ACCESS_TOKEN":  "b",
		"api-key":       "c",
		"Authorization": "d",
		"Api-Key":       "e",
	}
	got := log.SafeMap(payload, log.MaskConfig{})
	for k, v := range got {
		if v != "********" {
			t.Errorf("%s = %#v, muốn ********", k, v)
		}
	}
}

// Khai Fields riêng không được vô tình tắt danh sách mặc định — đó là cách
// password lọt vào log mà không ai nhận ra.
func TestLop3_TronVoiMacDinh(t *testing.T) {
	got := log.SafeMap(
		map[string]any{"password": "vẫn phải che", "ma_rieng": "cũng che"},
		log.MaskConfig{Fields: map[string]log.Rule{"ma_rieng": log.RuleRedact}},
	)
	if got["password"] != "********" {
		t.Errorf("password = %#v — khai Fields riêng đã tắt danh sách mặc định", got["password"])
	}
	if got["ma_rieng"] != "********" {
		t.Errorf("ma_rieng = %#v", got["ma_rieng"])
	}
}

func TestLop3_KhaiTayThangMacDinh(t *testing.T) {
	got := log.SafeMap(
		map[string]any{"password": "0123456789abcdef"},
		log.MaskConfig{Fields: map[string]log.Rule{"password": "edges=0,4"}},
	)
	if s, _ := got["password"].(string); !strings.HasSuffix(s, "cdef") {
		t.Errorf("password = %#v, luật khai tay phải thắng mặc định", got["password"])
	}
}

func TestLop3_MapNil(t *testing.T) {
	if got := log.SafeMap(nil, log.MaskConfig{}); got != nil {
		t.Errorf("SafeMap(nil) = %#v, muốn nil", got)
	}
}

func TestLop3_KhongSuaMapGoc(t *testing.T) {
	orig := map[string]any{"password": "giá trị gốc"}
	log.SafeMap(orig, log.MaskConfig{})
	if orig["password"] != "giá trị gốc" {
		t.Errorf("SafeMap đã sửa map gốc: %#v", orig)
	}
}

// Output phải tất định: thứ tự lặp map trong Go là ngẫu nhiên, không sắp xếp thì
// golden test vô dụng.
func TestLop3_ThuTuTatDinh(t *testing.T) {
	payload := map[string]any{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	first := ""
	for i := range 20 {
		logger, buf := newTestLogger(t, log.Options{})
		logger.Info("m", "body", log.Safe(payload))

		// Chỉ so phần body: field time đổi mỗi lần chạy.
		body := string(mustJSON(t, jsonField(t, buf.String(), "body")))
		if i == 0 {
			first = body
			continue
		}
		if body != first {
			t.Fatalf("thứ tự không tất định:\n%s\n%s", first, body)
		}
	}
}

// ---------- secret.Secret phải bị che ở cả ba lớp ----------

func TestSecret_CheOCaBaLop(t *testing.T) {
	const plaintext = "mật khẩu thật không được lộ"

	type cfgStruct struct {
		Host     string        `json:"host"`
		Password secret.Secret `json:"password"`
		// Không có tag log: nào — Secret phải tự che.
		Extra secret.Secret `json:"extra_credential"`
	}

	t.Run("lớp 2 - trong struct qua Safe", func(t *testing.T) {
		got := safeGroup(t, cfgStruct{Host: "db", Password: plaintext, Extra: plaintext})
		raw := string(mustJSON(t, got))
		if strings.Contains(raw, plaintext) {
			t.Fatalf("bí mật lọt qua lớp 2: %s", raw)
		}
		if !strings.Contains(raw, secret.Redacted) {
			t.Errorf("không thấy %s: %s", secret.Redacted, raw)
		}
	})

	t.Run("lớp 3 - trong map qua SafeMap", func(t *testing.T) {
		got := log.SafeMap(map[string]any{
			"ten_la":      secret.Secret(plaintext),
			"long":        map[string]any{"sau_hon": secret.Secret(plaintext)},
			"trong_slice": []any{secret.Secret(plaintext)},
		}, log.MaskConfig{})
		raw := string(mustJSON(t, got))
		if strings.Contains(raw, plaintext) {
			t.Fatalf("bí mật lọt qua lớp 3: %s", raw)
		}
	})

	// Bí mật dài phải bị che thành [REDACTED], không được biến thành metadata
	// elide: elide của một bí mật vừa vô nghĩa vừa để lộ kích thước bí mật.
	t.Run("lớp 1 - bí mật dài vẫn ra [REDACTED]", func(t *testing.T) {
		long := secret.Secret(strings.Repeat("bí mật ", 100))
		for _, maxLen := range []int{1, 8, 32, log.DefaultMaxLen} {
			got := log.SafeMap(map[string]any{"v": long}, log.MaskConfig{MaxLen: maxLen})
			if got["v"] != secret.Redacted {
				t.Errorf("MaxLen=%d: v = %#v, muốn %s", maxLen, got["v"], secret.Redacted)
			}
		}
	})

	t.Run("attribute trực tiếp", func(t *testing.T) {
		logger, buf := newTestLogger(t, log.Options{})
		logger.Info("m", "cred", secret.Secret(plaintext))
		if strings.Contains(buf.String(), plaintext) {
			t.Fatalf("bí mật lọt qua attribute: %s", buf.String())
		}
	})
}

// ---------- helper ----------

// elidedOf lấy metadata elide từ kết quả SafeMap.
//
// SafeMap trả về dữ liệu thuần (map, string, số) chứ không phải type của gokit,
// nên metadata elide ở đó là map[string]any với các key _elided/bytes/sha256 —
// cùng hình dạng JSON với khi đi qua slog.
func elidedOf(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok || m["_elided"] == nil {
		t.Fatalf("%#v không phải metadata elide", v)
	}
	return m
}

func elidedBytes(t *testing.T, m map[string]any) int64 {
	t.Helper()
	n, ok := m["bytes"].(int64)
	if !ok {
		t.Fatalf("bytes = %#v, muốn int64", m["bytes"])
	}
	return n
}

func elidedSHA(t *testing.T, m map[string]any) string {
	t.Helper()
	if m["sha256"] == nil {
		return ""
	}
	s, ok := m["sha256"].(string)
	if !ok {
		t.Fatalf("sha256 = %#v, muốn string", m["sha256"])
	}
	return s
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// ---------- các đường dùng còn lại ----------

// Safe phải che được cả khi không đi qua handler của gokit: người dùng có thể
// đính nó vào một *slog.Logger dựng sẵn ở chỗ khác.
func TestSafe_VoiHandlerSlogThuong(t *testing.T) {
	type req struct {
		User     string        `json:"user"`
		Password string        `json:"password" log:"redact"`
		Cred     secret.Secret `json:"cred"`
		Blob     string        `json:"blob"`
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("m", "body", log.Safe(req{
		User:     "an",
		Password: "mật khẩu",
		Cred:     "token thật",
		Blob:     strings.Repeat("z", log.DefaultMaxLen+1),
	}))

	out := buf.String()
	for _, leaked := range []string{"mật khẩu", "token thật"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%q lọt ra khi dùng handler slog thường: %s", leaked, out)
		}
	}
	if !strings.Contains(out, "_elided") {
		t.Errorf("lớp 1 không chạy với handler thường: %s", out)
	}
	if !strings.Contains(out, `"user":"an"`) {
		t.Errorf("field thường bị mất: %s", out)
	}
}

func TestSafe_LongNhauChinhNo(t *testing.T) {
	type req struct {
		Password string `json:"password" log:"redact"`
	}
	got := safeGroup(t, log.Safe(req{Password: "mật khẩu"}))
	if got["password"] != "********" {
		t.Errorf("password = %#v, Safe bọc Safe phải cho cùng kết quả", got["password"])
	}
}

// Luật elide và hash phải đọc byte thô: đi qua fmt sẽ biến []byte thành
// "[122 122 ...]" làm cả kích thước lẫn hash đều sai.
func TestLop2_RuleTrenKieuKhongPhaiString(t *testing.T) {
	type payload struct {
		Blob    []byte `json:"blob" log:"elide"`
		Amount  int    `json:"amount" log:"hash"`
		Enabled bool   `json:"enabled" log:"redact"`
		Score   int    `json:"score" log:"truncate=2"`
	}
	raw := []byte(strings.Repeat("z", 300))
	got := safeGroup(t, payload{Blob: raw, Amount: 1234567, Enabled: true, Score: 98765})

	e, ok := got["blob"].(map[string]any)
	if !ok {
		t.Fatalf("blob = %#v", got["blob"])
	}
	if n, _ := e["bytes"].(float64); int(n) != len(raw) {
		t.Errorf("blob.bytes = %#v, muốn %d — elide đã không đọc byte thô", e["bytes"], len(raw))
	}
	if s, _ := got["amount"].(string); len(s) != 64 {
		t.Errorf("amount = %#v, muốn hash 64 hex", got["amount"])
	}
	if got["enabled"] != "********" {
		t.Errorf("enabled = %#v", got["enabled"])
	}
	if got["score"] != "98…" {
		t.Errorf("score = %#v, muốn 2 ký tự đầu", got["score"])
	}
}

// []byte ngắn không bị elide, và []byte không được walk thành slice của số.
func TestLop1_ByteSliceNgan(t *testing.T) {
	got := log.SafeMap(map[string]any{"blob": []byte("ngắn")}, log.MaskConfig{})
	if _, isSlice := got["blob"].([]any); isSlice {
		t.Errorf("blob = %#v, []byte không được trải thành slice số", got["blob"])
	}
}

func TestLop3_MapKeyKhongPhaiString(t *testing.T) {
	payload := map[string]any{
		"theo_so": map[int]string{2: "hai", 1: "một"},
	}
	got := log.SafeMap(payload, log.MaskConfig{})
	inner, ok := got["theo_so"].(map[string]any)
	if !ok {
		t.Fatalf("theo_so = %#v", got["theo_so"])
	}
	if inner["1"] != "một" || inner["2"] != "hai" {
		t.Errorf("map key không phải string không hiển thị đúng: %#v", inner)
	}
}

func TestLop3_MapCuaMapLongNhau(t *testing.T) {
	got := log.SafeMap(map[string]any{
		"a": map[string]any{"b": map[string]any{"c": map[string]any{"token": "sâu 4 tầng"}}},
	}, log.MaskConfig{})

	if strings.Contains(string(mustJSON(t, got)), "sâu 4 tầng") {
		t.Errorf("token ở tầng 4 không bị che: %s", mustJSON(t, got))
	}
}

func TestTruncateVeKhong(t *testing.T) {
	type doc struct {
		Note string `json:"note" log:"truncate=0"`
	}
	got := safeGroup(t, doc{Note: "nội dung"})
	if got["note"] != "…" {
		t.Errorf("note = %#v, truncate=0 phải bỏ hết nội dung", got["note"])
	}
}

// Marker _dropped phải nói đúng giới hạn, kể cả khi giới hạn không chia hết ra KB.
func TestMaxLineBytes_DinhDangGioiHan(t *testing.T) {
	tests := []struct {
		limit int
		want  string
	}{
		{2048, "2KB"},
		{32 << 10, "32KB"},
		{1 << 20, "1MB"},
		{5000, "5000 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			logger, buf := newTestLogger(t, log.Options{
				// MaxLen phải lớn hơn body, nếu không lớp 1 rút gọn trước và
				// trần dòng không có việc gì để làm.
				Mask: log.MaskConfig{MaxLen: tt.limit * 4, MaxLineBytes: tt.limit},
			})
			logger.Info("m", "body", strings.Repeat("x", tt.limit*2))

			body, ok := jsonField(t, buf.String(), "body").(map[string]any)
			if !ok {
				t.Fatalf("body = %#v, muốn marker", jsonField(t, buf.String(), "body"))
			}
			reason, _ := body["_dropped"].(string)
			if !strings.Contains(reason, tt.want) {
				t.Errorf("_dropped = %q, muốn chứa %q", reason, tt.want)
			}
		})
	}
}
