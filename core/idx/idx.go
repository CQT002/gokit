// Package idx sinh định danh: UUID v4/v7, trace ID và span ID theo chuẩn W3C,
// và chuỗi ngẫu nhiên dùng chung.
//
// Mọi hàm ở đây lấy entropy từ crypto/rand, không bao giờ từ math/rand. ID sinh
// từ math/rand là đoán được, và một request ID đoán được đủ để người ngoài dò
// log của request khác.
package idx

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/google/uuid"
)

// Bảng ký tự cho RandomString và RandomDigits. Độ dài của chúng (62 và 10)
// quyết định ngưỡng loại bỏ byte ở randomFromAlphabet.
const (
	alphanumChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	digitChars    = "0123456789"
)

// NewUUIDv4 trả về UUID phiên bản 4 (ngẫu nhiên toàn phần) dạng chuỗi có dấu gạch.
func NewUUIDv4() string {
	return uuid.Must(uuid.NewRandom()).String()
}

// NewUUIDv7 trả về UUID phiên bản 7 — 48 bit đầu là timestamp Unix milli, nên
// chuỗi sinh ra sắp xếp được theo thời gian.
//
// Đây là lựa chọn ưu tiên cho khoá chính: index B-tree chỉ ghi vào phần cuối cây
// thay vì rải khắp như v4, và hai ID sinh liên tiếp luôn tăng dần.
func NewUUIDv7() string {
	return uuid.Must(uuid.NewV7()).String()
}

// NewTraceID trả về trace ID chuẩn W3C: 32 ký tự hex viết thường (16 byte).
func NewTraceID() string {
	return randomHex(16)
}

// NewSpanID trả về span ID chuẩn W3C: 16 ký tự hex viết thường (8 byte).
func NewSpanID() string {
	return randomHex(8)
}

// RandomString trả về chuỗi n ký tự lấy từ [0-9A-Za-z], phân phối đều.
// n <= 0 trả về chuỗi rỗng.
func RandomString(n int) string {
	return randomFromAlphabet(n, alphanumChars)
}

// RandomDigits trả về chuỗi n chữ số [0-9], phân phối đều. Dùng cho OTP, mã
// tham chiếu hiển thị cho người đọc. n <= 0 trả về chuỗi rỗng.
func RandomDigits(n int) string {
	return randomFromAlphabet(n, digitChars)
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	mustRead(b)
	return hex.EncodeToString(b)
}

// randomFromAlphabet lấy mẫu có loại bỏ (rejection sampling) chứ không dùng
// b % len(alphabet) trực tiếp: 256 không chia hết cho 62, nên phép chia lấy dư
// trần sẽ làm 8 ký tự đầu bảng xuất hiện nhiều hơn phần còn lại.
func randomFromAlphabet(n int, alphabet string) string {
	if n <= 0 {
		return ""
	}
	size := len(alphabet)
	limit := 256 - (256 % size) // 248 với 62 ký tự, 250 với 10 chữ số

	out := make([]byte, 0, n)
	// Đọc dư ~25% để phần lớn trường hợp chỉ cần một lần gọi crypto/rand:
	// tỉ lệ byte bị loại là (256-limit)/256, tối đa 8/256 với hai bảng ở trên.
	buf := make([]byte, n+n/4+8)
	for len(out) < n {
		mustRead(buf)
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, alphabet[int(b)%size])
			if len(out) == n {
				break
			}
		}
	}
	return string(out)
}

// mustRead panic khi không lấy được entropy. Từ Go 1.24 crypto/rand.Read không
// còn trả lỗi (nó tự panic nếu OS từ chối cấp entropy) — nhánh này chỉ còn là
// lưới an toàn, và panic đúng hơn là trả về ID đoán được.
func mustRead(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("idx: không đọc được crypto/rand: " + err.Error())
	}
}
