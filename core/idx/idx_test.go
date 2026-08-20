package idx_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/idx"
)

var (
	reUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reHex  = regexp.MustCompile(`^[0-9a-f]+$`)
)

func TestNewUUIDv4(t *testing.T) {
	id := idx.NewUUIDv4()
	if !reUUID.MatchString(id) {
		t.Fatalf("dạng UUID sai: %q", id)
	}
	// Nibble phiên bản nằm ở ký tự thứ 15 (sau 2 dấu gạch).
	if got := id[14]; got != '4' {
		t.Errorf("phiên bản = %c, muốn 4 (%q)", got, id)
	}
	// Variant RFC 4122: 2 bit cao của byte thứ 9 là 10 → ký tự thuộc [89ab].
	if got := id[19]; !strings.ContainsRune("89ab", rune(got)) {
		t.Errorf("variant = %c, muốn một trong 89ab (%q)", got, id)
	}
}

func TestNewUUIDv7(t *testing.T) {
	id := idx.NewUUIDv7()
	if !reUUID.MatchString(id) {
		t.Fatalf("dạng UUID sai: %q", id)
	}
	if got := id[14]; got != '7' {
		t.Errorf("phiên bản = %c, muốn 7 (%q)", got, id)
	}
}

// Lý do duy nhất chọn v7 làm khoá chính là thứ tự theo thời gian — nếu tính chất
// này mất thì lựa chọn đó không còn cơ sở, nên phải có test canh.
func TestNewUUIDv7_TangDan(t *testing.T) {
	const n = 2000
	prev := idx.NewUUIDv7()
	for i := 1; i < n; i++ {
		cur := idx.NewUUIDv7()
		if cur <= prev {
			t.Fatalf("v7 không tăng dần ở lần %d: %q <= %q", i, cur, prev)
		}
		prev = cur
	}
}

func TestTraceVaSpanID(t *testing.T) {
	tests := []struct {
		name string
		gen  func() string
		want int
	}{
		{"trace", idx.NewTraceID, 32},
		{"span", idx.NewSpanID, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.gen()
			if len(id) != tt.want {
				t.Errorf("độ dài = %d, muốn %d (%q)", len(id), tt.want, id)
			}
			if !reHex.MatchString(id) {
				t.Errorf("không phải hex viết thường: %q", id)
			}
			if strings.Trim(id, "0") == "" {
				t.Errorf("ID toàn số 0 là giá trị không hợp lệ theo W3C: %q", id)
			}
		})
	}
}

func TestKhongTrungLap(t *testing.T) {
	gens := map[string]func() string{
		"uuidv4":  idx.NewUUIDv4,
		"uuidv7":  idx.NewUUIDv7,
		"traceID": idx.NewTraceID,
		"spanID":  idx.NewSpanID,
		"random":  func() string { return idx.RandomString(16) },
	}
	for name, gen := range gens {
		t.Run(name, func(t *testing.T) {
			const n = 5000
			seen := make(map[string]struct{}, n)
			for range n {
				id := gen()
				if _, dup := seen[id]; dup {
					t.Fatalf("trùng sau %d lần sinh: %q", len(seen), id)
				}
				seen[id] = struct{}{}
			}
		})
	}
}

func TestRandomString_DoDaiVaBangKyTu(t *testing.T) {
	const allowed = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	for _, n := range []int{1, 2, 16, 64, 1000} {
		s := idx.RandomString(n)
		if len(s) != n {
			t.Errorf("RandomString(%d) dài %d", n, len(s))
		}
		for _, r := range s {
			if !strings.ContainsRune(allowed, r) {
				t.Errorf("RandomString(%d) chứa ký tự ngoài bảng: %q", n, r)
			}
		}
	}
}

func TestRandomDigits(t *testing.T) {
	s := idx.RandomDigits(6)
	if len(s) != 6 {
		t.Fatalf("độ dài = %d, muốn 6", len(s))
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Errorf("chứa ký tự không phải chữ số: %q", r)
		}
	}
}

func TestRandom_NKhongDuong(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if got := idx.RandomString(n); got != "" {
			t.Errorf("RandomString(%d) = %q, muốn rỗng", n, got)
		}
		if got := idx.RandomDigits(n); got != "" {
			t.Errorf("RandomDigits(%d) = %q, muốn rỗng", n, got)
		}
	}
}

// Canh lỗi off-by-one ở ngưỡng loại bỏ byte: nếu ngưỡng tính sai, ký tự cuối
// bảng sẽ không bao giờ xuất hiện, còn vài ký tự đầu bảng sẽ xuất hiện gấp đôi.
func TestRandomString_PhuHetBangKyTu(t *testing.T) {
	const allowed = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	count := make(map[rune]int, len(allowed))
	for _, r := range idx.RandomString(62 * 400) {
		count[r]++
	}
	for _, r := range allowed {
		if count[r] == 0 {
			t.Errorf("ký tự %q không xuất hiện lần nào", r)
		}
	}
	if len(count) != len(allowed) {
		t.Errorf("số ký tự khác nhau = %d, muốn %d", len(count), len(allowed))
	}
	// Với 400 lần xuất hiện kỳ vọng mỗi ký tự, lệch quá 3 lần là dấu hiệu bias
	// thật chứ không phải nhiễu thống kê.
	for r, c := range count {
		if c < 400/3 || c > 400*3 {
			t.Errorf("ký tự %q xuất hiện %d lần, kỳ vọng khoảng 400", r, c)
		}
	}
}
