package crypto

import (
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
)

// EncryptedText là chuỗi thay thế cho giá trị Encrypted khi in ra log.
const EncryptedText = "[ENCRYPTED]"

// ErrNoCipher là lỗi khi dùng Encrypted mà chưa gọi SetDefaultCipher.
var ErrNoCipher = errors.New("crypto: chưa đặt cipher mặc định, gọi crypto.SetDefaultCipher lúc khởi động")

// defaultCipher là cipher mà Encrypted dùng.
//
// Đây là state toàn cục thay đổi được — thứ mà gokit tránh ở mọi nơi khác. Không
// có đường nào khác: GORM gọi Value() và Scan() trên field của model, không truyền
// context hay dependency nào vào, nên cipher phải đến từ một chỗ mà method tìm
// được. Bù lại nó chỉ dành cho lúc khởi động, và dùng atomic.Pointer nên đọc ghi
// song song vẫn an toàn.
var defaultCipher atomic.Pointer[Cipher]

// SetDefaultCipher đặt cipher cho type Encrypted.
//
// Gọi một lần lúc khởi động, trước khi mở kết nối DB. Đổi cipher giữa lúc đang
// chạy là hợp lệ về mặt kỹ thuật — dùng để nạp khoá mới — nhưng cipher mới phải
// còn giữ các khoá cũ, nếu không dữ liệu đã ghi sẽ không đọc lại được.
func SetDefaultCipher(c *Cipher) { defaultCipher.Store(c) }

// DefaultCipher trả về cipher đang dùng cho Encrypted, nil nếu chưa đặt.
func DefaultCipher() *Cipher { return defaultCipher.Load() }

// Encrypted là chuỗi tự mã hoá khi ghi xuống DB và tự giải mã khi đọc lên.
//
// Dùng trực tiếp làm kiểu của field trong model GORM:
//
//	type Customer struct {
//	    ID    string
//	    Phone crypto.Encrypted // cột trong DB chứa blob base64
//	}
//
// Giá trị trong bộ nhớ là **dạng rõ**; chỉ dạng lưu trong DB là đã mã hoá. Tên
// type nói về cột dữ liệu, không nói về biến.
//
// Cột trong DB nên là text hoặc varchar đủ dài: blob được encode base64 nên dài
// hơn dữ liệu gốc khoảng 4/3, cộng thêm khoảng 40 byte cho header, nonce và tag.
type Encrypted string

// Value cài đặt driver.Valuer: mã hoá rồi trả về blob dạng base64.
//
// Chuỗi rỗng thành NULL thay vì một blob mã hoá của chuỗi rỗng — cột PII rỗng
// đúng nghĩa là "không có dữ liệu", và NULL là cách SQL nói điều đó.
func (e Encrypted) Value() (driver.Value, error) {
	if e == "" {
		return nil, nil
	}
	c := DefaultCipher()
	if c == nil {
		return nil, ErrNoCipher
	}

	blob, err := c.Encrypt([]byte(e))
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.EncodeToString(blob), nil
}

// Scan cài đặt sql.Scanner: giải base64 rồi giải mã.
func (e *Encrypted) Scan(v any) error {
	if v == nil {
		*e = ""
		return nil
	}

	var raw string
	switch t := v.(type) {
	case string:
		raw = t
	case []byte:
		raw = string(t)
	default:
		return fmt.Errorf("crypto: không đọc được Encrypted từ kiểu %T", v)
	}
	if raw == "" {
		*e = ""
		return nil
	}

	c := DefaultCipher()
	if c == nil {
		return ErrNoCipher
	}

	blob, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("%w: giá trị trong cột không phải base64", ErrInvalidBlob)
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return err
	}
	*e = Encrypted(plain)
	return nil
}

// LogValue cài đặt slog.LogValuer, luôn trả về EncryptedText.
//
// Field kiểu này chứa PII, và log là nơi PII lọt ra nhiều nhất. Che ở đây nghĩa là
// log cả model cũng không lộ gì, không cần ai nhớ đính tag.
func (e Encrypted) LogValue() slog.Value { return slog.StringValue(EncryptedText) }

// String cài đặt fmt.Stringer để %v và %s không in ra PII.
//
// Muốn giá trị thật thì dùng Reveal hoặc string(e) — cả hai đều là thao tác
// tường minh, grep được khi audit.
func (e Encrypted) String() string { return EncryptedText }

// GoString cài đặt fmt.GoStringer cho verb %#v.
func (e Encrypted) GoString() string { return EncryptedText }

// MarshalJSON trả về EncryptedText, để một model lỡ đem marshal thẳng ra response
// cũng không lộ PII.
//
// Không có UnmarshalJSON tương ứng: chiều đọc vào dùng cài đặt mặc định của kiểu
// string nên nhận dữ liệu thật bình thường.
func (e Encrypted) MarshalJSON() ([]byte, error) {
	return []byte(`"` + EncryptedText + `"`), nil
}

// Reveal trả về giá trị dạng rõ. Mọi chỗ gọi hàm này là chỗ cần xem lại khi audit.
func (e Encrypted) Reveal() string { return string(e) }
