package crypto_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/crypto"
)

const pii = "0912345678"

// useCipher đặt cipher mặc định cho một test và trả lại giá trị cũ khi xong.
//
// defaultCipher là state toàn cục nên test nào sửa nó phải tự dọn, nếu không thứ
// tự chạy test sẽ quyết định kết quả.
func useCipher(t *testing.T, c *crypto.Cipher) {
	t.Helper()
	truoc := crypto.DefaultCipher()
	crypto.SetDefaultCipher(c)
	t.Cleanup(func() { crypto.SetDefaultCipher(truoc) })
}

func TestEncrypted_VongTronGhiDoc(t *testing.T) {
	useCipher(t, mustCipher(t))

	v, err := crypto.Encrypted(pii).Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	stored, ok := v.(string)
	if !ok {
		t.Fatalf("Value trả kiểu %T, muốn string", v)
	}
	if strings.Contains(stored, pii) {
		t.Fatal("dữ liệu gốc xuất hiện nguyên vẹn trong giá trị lưu xuống DB")
	}
	if _, err := base64.StdEncoding.DecodeString(stored); err != nil {
		t.Errorf("giá trị lưu không phải base64: %v", err)
	}

	var got crypto.Encrypted
	if err := got.Scan(stored); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.Reveal() != pii {
		t.Errorf("Scan ra %q, muốn %q", got.Reveal(), pii)
	}
}

// Driver DB trả về []byte hay string tuỳ driver và tuỳ kiểu cột, nên phải nhận cả hai.
func TestEncrypted_ScanNhanCaStringVaBytes(t *testing.T) {
	useCipher(t, mustCipher(t))

	v, err := crypto.Encrypted(pii).Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	stored := v.(string)

	for name, raw := range map[string]any{
		"string": stored,
		"bytes":  []byte(stored),
	} {
		t.Run(name, func(t *testing.T) {
			var got crypto.Encrypted
			if err := got.Scan(raw); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if got.Reveal() != pii {
				t.Errorf("Scan ra %q", got.Reveal())
			}
		})
	}
}

// Cột PII rỗng đúng nghĩa là "không có dữ liệu", và NULL là cách SQL nói điều đó.
func TestEncrypted_RongThanhNULL(t *testing.T) {
	useCipher(t, mustCipher(t))

	v, err := crypto.Encrypted("").Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if v != nil {
		t.Errorf("Value = %#v, muốn nil (NULL)", v)
	}

	for name, raw := range map[string]any{"nil": nil, "chuỗi rỗng": ""} {
		t.Run(name, func(t *testing.T) {
			got := crypto.Encrypted("giá trị cũ")
			if err := got.Scan(raw); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if got != "" {
				t.Errorf("Scan ra %q, muốn rỗng", got.Reveal())
			}
		})
	}
}

func TestEncrypted_ScanLoi(t *testing.T) {
	useCipher(t, mustCipher(t))

	tests := []struct {
		name string
		raw  any
	}{
		{"kiểu không hỗ trợ", 42},
		{"không phải base64", "!!!khong-phai-base64!!!"},
		{"base64 nhưng không phải blob", base64.StdEncoding.EncodeToString([]byte("rác"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got crypto.Encrypted
			if err := got.Scan(tt.raw); err == nil {
				t.Error("muốn lỗi, không có lỗi")
			}
		})
	}
}

// Chưa đặt cipher là lỗi cấu hình, và nó phải lộ ra dưới dạng lỗi rõ ràng chứ
// không phải panic hay dữ liệu lưu ở dạng rõ.
func TestEncrypted_ChuaDatCipher(t *testing.T) {
	useCipher(t, nil)

	if _, err := crypto.Encrypted(pii).Value(); !errors.Is(err, crypto.ErrNoCipher) {
		t.Errorf("Value = %v, muốn ErrNoCipher", err)
	}

	var got crypto.Encrypted
	if err := got.Scan("YWJj"); !errors.Is(err, crypto.ErrNoCipher) {
		t.Errorf("Scan = %v, muốn ErrNoCipher", err)
	}

	// Chuỗi rỗng không cần cipher: nó thành NULL mà không mã hoá gì.
	if v, err := crypto.Encrypted("").Value(); err != nil || v != nil {
		t.Errorf("Value của chuỗi rỗng = (%#v, %v), muốn (nil, nil)", v, err)
	}
}

// Field kiểu này chứa PII, và log là nơi PII lọt ra nhiều nhất.
func TestEncrypted_MoiDuongRaDeuChe(t *testing.T) {
	type customer struct {
		ID    string            `json:"id"`
		Phone crypto.Encrypted  `json:"phone"`
		Notes *crypto.Encrypted `json:"notes"`
	}
	notes := crypto.Encrypted("ghi chú riêng tư")
	c := customer{ID: "kh-1", Phone: pii, Notes: &notes}

	e := crypto.Encrypted(pii)
	cases := map[string]string{
		"String":   e.String(),
		"GoString": e.GoString(),
		"fmt %v":   fmt.Sprintf("%v", e),
		// Cố tình đi qua fmt thay vì gọi String() trực tiếp: đang kiểm tra chính
		// đường đi qua verb %s.
		"fmt %s":         fmt.Sprintf("%s", e), //nolint:staticcheck // cố tình đi qua fmt
		"fmt %+v struct": fmt.Sprintf("%+v", c),
	}
	for name, got := range cases {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(got, pii) {
				t.Fatalf("PII lọt ra: %s", got)
			}
			if !strings.Contains(got, crypto.EncryptedText) {
				t.Errorf("không thấy %s trong %q", crypto.EncryptedText, got)
			}
		})
	}

	t.Run("json.Marshal", func(t *testing.T) {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if strings.Contains(string(b), pii) {
			t.Fatalf("PII lọt ra JSON: %s", b)
		}
		if strings.Count(string(b), crypto.EncryptedText) != 2 {
			t.Errorf("số lần che = %d, muốn 2: %s", strings.Count(string(b), crypto.EncryptedText), b)
		}
	})

	t.Run("slog", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("customer", "c", c, "phone", e)
		if strings.Contains(buf.String(), pii) {
			t.Fatalf("PII lọt ra log: %s", buf.String())
		}
	})
}

// Chiều đọc vào phải giữ dữ liệu thật, nếu không thì không nhận được request nào.
func TestEncrypted_UnmarshalJSONGiuGiaTriThat(t *testing.T) {
	var v struct {
		Phone crypto.Encrypted `json:"phone"`
	}
	if err := json.Unmarshal([]byte(`{"phone":"`+pii+`"}`), &v); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if v.Phone.Reveal() != pii {
		t.Errorf("Reveal = %q, muốn %q", v.Phone.Reveal(), pii)
	}
}

// Đổi khoá cũng phải áp cho Encrypted: dữ liệu đã ghi bằng khoá cũ vẫn đọc được
// sau khi nạp cipher mới.
func TestEncrypted_QuaDoiKhoa(t *testing.T) {
	keyCu, keyMoi := mustKey(t), mustKey(t)

	cipherCu, err := crypto.NewCipherWithKeys(crypto.Key{ID: "cu", Key: keyCu})
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}
	useCipher(t, cipherCu)

	v, err := crypto.Encrypted(pii).Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	stored := v.(string)

	sauDoi, err := crypto.NewCipherWithKeys(
		crypto.Key{ID: "moi", Key: keyMoi},
		crypto.Key{ID: "cu", Key: keyCu},
	)
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}
	crypto.SetDefaultCipher(sauDoi)

	var got crypto.Encrypted
	if err := got.Scan(stored); err != nil {
		t.Fatalf("không đọc lại được dữ liệu ghi bằng khoá cũ: %v", err)
	}
	if got.Reveal() != pii {
		t.Errorf("Scan ra %q", got.Reveal())
	}
}
