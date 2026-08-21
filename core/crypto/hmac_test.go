package crypto_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/crypto"
)

func TestSignHMAC(t *testing.T) {
	key := []byte("khoá webhook")
	payload := []byte(`{"event":"payment.completed"}`)

	sig := crypto.SignHMAC(key, payload)

	if len(sig) != 64 {
		t.Errorf("độ dài = %d, muốn 64 hex của sha256", len(sig))
	}
	if sig != strings.ToLower(sig) {
		t.Errorf("sig = %q, muốn hex viết thường", sig)
	}
	if _, err := hex.DecodeString(sig); err != nil {
		t.Errorf("sig không phải hex: %v", err)
	}

	// Kiểm chứng độc lập bằng stdlib, không chỉ so với chính nó.
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if want := hex.EncodeToString(mac.Sum(nil)); sig != want {
		t.Errorf("sig = %q, muốn %q", sig, want)
	}
}

func TestSignHMAC_OnDinhVaPhanBiet(t *testing.T) {
	key := []byte("khoá")

	if a, b := crypto.SignHMAC(key, []byte("x")), crypto.SignHMAC(key, []byte("x")); a != b {
		t.Error("cùng khoá cùng payload ra hai signature khác nhau")
	}
	if a, b := crypto.SignHMAC(key, []byte("x")), crypto.SignHMAC(key, []byte("y")); a == b {
		t.Error("payload khác nhau ra cùng signature")
	}
	if a, b := crypto.SignHMAC([]byte("k1"), []byte("x")), crypto.SignHMAC([]byte("k2"), []byte("x")); a == b {
		t.Error("khoá khác nhau ra cùng signature")
	}
}

func TestVerifyHMAC(t *testing.T) {
	key := []byte("khoá webhook")
	payload := []byte(`{"event":"payment.completed"}`)
	sig := crypto.SignHMAC(key, payload)

	if !crypto.VerifyHMAC(key, payload, sig) {
		t.Error("signature đúng bị từ chối")
	}

	// Hex viết hoa vẫn phải hợp lệ: so sánh diễn ra trên byte thô sau khi giải hex.
	if !crypto.VerifyHMAC(key, payload, strings.ToUpper(sig)) {
		t.Error("signature viết hoa bị từ chối")
	}
}

func TestVerifyHMAC_TuChoi(t *testing.T) {
	key := []byte("khoá webhook")
	payload := []byte("payload")
	sig := crypto.SignHMAC(key, payload)

	tests := []struct {
		name    string
		key     []byte
		payload []byte
		sig     string
	}{
		{"sai khoá", []byte("khoá khác"), payload, sig},
		{"payload bị sửa", key, []byte("payload!"), sig},
		{"sig rỗng", key, payload, ""},
		{"sig không phải hex", key, payload, "khong-phai-hex-zzzz"},
		{"sig độ dài lẻ", key, payload, sig[:63]},
		{"sig ngắn hơn", key, payload, sig[:32]},
		{"sig dài hơn", key, payload, sig + "00"},
		{"sig lệch một ký tự cuối", key, payload, flipLast(sig)},
		{"khoá rỗng", nil, payload, sig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if crypto.VerifyHMAC(tt.key, tt.payload, tt.sig) {
				t.Error("VerifyHMAC = true, muốn false")
			}
		})
	}
}

func flipLast(sig string) string {
	last := sig[len(sig)-1]
	if last == '0' {
		return sig[:len(sig)-1] + "1"
	}
	return sig[:len(sig)-1] + "0"
}

// Signature chỉ khớp phần đầu phải bị từ chối. Đây là điều kiện mà so sánh chuỗi
// kiểu prefix hoặc EqualFold nửa vời sẽ làm sai.
func TestVerifyHMAC_KhopMotPhanKhongDuoc(t *testing.T) {
	key := []byte("khoá")
	payload := []byte("payload")
	sig := crypto.SignHMAC(key, payload)

	for n := 1; n < len(sig); n++ {
		if crypto.VerifyHMAC(key, payload, sig[:n]) {
			t.Fatalf("chấp nhận signature chỉ có %d/%d ký tự đầu", n, len(sig))
		}
	}
}

// Payload rỗng vẫn ký và verify được — webhook GET không có body vẫn cần ký.
func TestHMAC_PayloadRong(t *testing.T) {
	key := []byte("khoá")
	sig := crypto.SignHMAC(key, nil)
	if !crypto.VerifyHMAC(key, nil, sig) {
		t.Error("không verify được payload nil")
	}
	if !crypto.VerifyHMAC(key, []byte{}, sig) {
		t.Error("payload nil và payload rỗng phải cho cùng signature")
	}
}
