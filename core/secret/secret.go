// Package secret cung cấp type Secret cho giá trị không được phép xuất hiện
// trong log, dump hay response.
//
// Vấn đề nó giải quyết: một dòng fmt.Printf("%+v", cfg) hoặc slog.Info("config",
// "cfg", cfg) là đủ để đẩy mật khẩu DB vào log tập trung, nơi nhiều người đọc
// được hơn hẳn số người được phép biết mật khẩu đó. Khai báo field là Secret thì
// mọi đường ra text/JSON/log đều tự trả về "[REDACTED]"; muốn giá trị thật phải
// gọi Reveal() — một lệnh grep là ra hết chỗ nào đang chạm vào bí mật.
package secret

import (
	"crypto/subtle"
	"log/slog"
)

// Redacted là chuỗi thay thế cho mọi giá trị Secret khi in ra.
const Redacted = "[REDACTED]"

// Secret là chuỗi bí mật (mật khẩu, API key, token, khoá HMAC).
//
// Vì kiểu nền là string, nó nhận giá trị trực tiếp từ YAML và biến môi trường
// mà không cần code chuyển đổi. Chỉ chiều đi ra bị chặn, chiều đi vào không.
type Secret string

// String cài đặt fmt.Stringer. Bao trùm %v, %s, %q và cả %+v khi Secret nằm
// trong struct.
func (s Secret) String() string { return Redacted }

// GoString cài đặt fmt.GoStringer, cho verb %#v.
func (s Secret) GoString() string { return Redacted }

// MarshalJSON trả về chuỗi JSON "[REDACTED]".
//
// Không có UnmarshalJSON tương ứng: chiều đọc vào dùng cài đặt mặc định của
// kiểu string, nên load config từ JSON vẫn ra giá trị thật. Hệ quả là Secret
// không round-trip qua JSON được — đó là chủ ý.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// MarshalText cài đặt encoding.TextMarshaler.
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// MarshalYAML che giá trị khi dump config bằng yaml.v3.
func (s Secret) MarshalYAML() (any, error) { return Redacted, nil }

// LogValue cài đặt slog.LogValuer, nên slog che giá trị mà không cần package log
// của gokit biết gì về type này.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// IsZero cho biết bí mật có rỗng không, dùng để validate config mà không phải
// lôi giá trị ra. yaml.v3 và json omitzero cũng dùng method này.
func (s Secret) IsZero() bool { return s == "" }

// Equal so sánh hai bí mật bằng thời gian không đổi.
//
// So sánh bằng == hoặc Reveal() == other làm thời gian thực thi phụ thuộc vào
// số byte khớp đầu chuỗi, tức là để hở kênh phụ cho phép dò dần token qua nhiều
// lần thử. Độ dài chuỗi vẫn lộ — subtle.ConstantTimeCompare không che được điều
// đó, nên đừng dùng Equal cho bí mật có độ dài mang thông tin.
func (s Secret) Equal(other Secret) bool {
	return subtle.ConstantTimeCompare([]byte(s), []byte(other)) == 1
}

// Reveal trả về giá trị thật. Mọi chỗ gọi hàm này đều là chỗ cần xem lại khi
// audit: đó là ranh giới bí mật rời khỏi vùng được bảo vệ.
func (s Secret) Reveal() string { return string(s) }
