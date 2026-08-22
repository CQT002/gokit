package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SignHMAC trả về HMAC-SHA256 của payload dưới dạng hex viết thường.
//
// Dùng để ký request gửi đối tác và để tự kiểm chứng webhook mình phát ra.
func SignHMAC(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload) // hash.Hash.Write không bao giờ trả lỗi
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyHMAC kiểm tra sig có phải HMAC-SHA256 hợp lệ của payload không.
//
// Bắt buộc dùng hmac.Equal, không dùng == hay strings.EqualFold: so sánh chuỗi
// thường thoát ra ngay tại byte đầu tiên lệch nhau, nên thời gian thực thi tiết lộ
// số byte đầu đã đoán đúng. Với một endpoint cho phép thử lại, đó là đủ để dò ra
// toàn bộ signature mà không cần biết khoá.
//
// So sánh trên byte thô sau khi giải hex, không so trên chuỗi hex: nhờ vậy chữ hoa
// chữ thường trong sig không làm sai kết quả.
func VerifyHMAC(key, payload []byte, sig string) bool {
	want := hmac.New(sha256.New, key)
	want.Write(payload)

	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want.Sum(nil))
}
