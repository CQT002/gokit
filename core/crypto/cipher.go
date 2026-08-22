// Package crypto gói lại những thao tác mật mã mà mỗi service tuyệt đối không
// nên tự viết: mã hoá field PII trong DB, HMAC cho webhook, và hash mật khẩu.
//
// Đây là loại code mà sai một tham số là mở một lỗ bảo mật, và cái sai đó không
// làm test fail — hệ thống vẫn chạy, vẫn mã hoá, chỉ là mã hoá theo cách phá được.
// Vì vậy package này không nhận tham số nào ảnh hưởng tới độ an toàn: thuật toán,
// kích thước nonce, tham số argon2 đều cố định trong code, không cấu hình được.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize là độ dài khoá bắt buộc, tính theo byte: AES-256.
const KeySize = 32

// DefaultKeyID là ID gán cho khoá khi dùng NewCipher.
//
// Blob nào cũng mang ID của khoá đã mã hoá nó, kể cả khi chỉ có một khoá — nhờ vậy
// ngày cần đổi khoá thì dữ liệu cũ vẫn giải mã được mà không phải đoán.
const DefaultKeyID = "0"

// maxKeyIDLen là giới hạn của ID khoá: độ dài ID được ghi vào blob bằng một byte.
const maxKeyIDLen = 255

// blobVersion là phiên bản định dạng blob, nằm ở byte đầu tiên. Có sẵn từ đầu để
// ngày đổi định dạng thì blob cũ vẫn đọc được.
const blobVersion = 1

// Các lỗi của package.
var (
	// ErrKeySize là lỗi khi khoá không đúng KeySize byte.
	ErrKeySize = errors.New("crypto: khoá phải đúng 32 byte")
	// ErrUnknownKeyID là lỗi khi blob được mã hoá bằng một khoá không có trong Cipher.
	ErrUnknownKeyID = errors.New("crypto: blob dùng khoá không có trong cipher")
	// ErrInvalidBlob là lỗi khi blob sai định dạng hoặc bị cắt ngắn.
	ErrInvalidBlob = errors.New("crypto: blob sai định dạng")
	// ErrDecrypt là lỗi khi giải mã thất bại: sai khoá, hoặc dữ liệu đã bị sửa.
	ErrDecrypt = errors.New("crypto: giải mã thất bại")
)

// Key là một khoá mã hoá kèm ID của nó.
type Key struct {
	// ID nhận diện khoá, được ghi vào blob. Không rỗng, tối đa 255 byte.
	//
	// Nên đặt theo thứ tự tăng dần hoặc theo thời điểm tạo ("2026-08", "v3") để
	// đọc dữ liệu là biết được blob đó thuộc thế hệ khoá nào.
	ID string
	// Key là khoá AES-256, đúng 32 byte.
	Key []byte
}

// Cipher mã hoá và giải mã bằng AES-256-GCM.
//
// Hỗ trợ đổi khoá: mã hoá luôn dùng khoá chính, còn giải mã tra khoá theo ID nhúng
// trong blob. Nhờ vậy đổi khoá là việc thêm khoá mới làm khoá chính và giữ khoá cũ
// trong danh sách — không cần dừng hệ thống, không cần mã hoá lại toàn bộ dữ liệu
// trước khi đổi.
type Cipher struct {
	primary  string
	byKeyID  map[string]cipher.AEAD
	keyOrder []string
}

// NewCipher tạo Cipher với một khoá duy nhất, ID là DefaultKeyID.
func NewCipher(key []byte) (*Cipher, error) {
	return NewCipherWithKeys(Key{ID: DefaultKeyID, Key: key})
}

// NewCipherWithKeys tạo Cipher với khoá chính và các khoá cũ.
//
// primary dùng để mã hoá. previous chỉ dùng để giải mã dữ liệu đã ghi bằng khoá
// cũ — đây là cơ chế đổi khoá: thêm khoá mới vào vị trí primary, đẩy khoá đang
// dùng xuống previous, và dữ liệu cũ vẫn đọc được.
func NewCipherWithKeys(primary Key, previous ...Key) (*Cipher, error) {
	c := &Cipher{
		primary: primary.ID,
		byKeyID: make(map[string]cipher.AEAD, 1+len(previous)),
	}
	for _, k := range append([]Key{primary}, previous...) {
		if err := c.add(k); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Cipher) add(k Key) error {
	switch {
	case k.ID == "":
		return errors.New("crypto: ID khoá không được rỗng")
	case len(k.ID) > maxKeyIDLen:
		return fmt.Errorf("crypto: ID khoá %q dài %d byte, tối đa %d", k.ID, len(k.ID), maxKeyIDLen)
	case len(k.Key) != KeySize:
		return fmt.Errorf("%w: khoá %q dài %d byte", ErrKeySize, k.ID, len(k.Key))
	}
	if _, dup := c.byKeyID[k.ID]; dup {
		return fmt.Errorf("crypto: ID khoá %q bị trùng", k.ID)
	}

	block, err := aes.NewCipher(k.Key)
	if err != nil {
		return fmt.Errorf("crypto: khởi tạo AES cho khoá %q: %w", k.ID, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("crypto: khởi tạo GCM cho khoá %q: %w", k.ID, err)
	}

	c.byKeyID[k.ID] = aead
	c.keyOrder = append(c.keyOrder, k.ID)
	return nil
}

// PrimaryKeyID trả về ID của khoá đang dùng để mã hoá.
func (c *Cipher) PrimaryKeyID() string { return c.primary }

// KeyIDs trả về ID của mọi khoá, khoá chính đứng đầu. Dùng để kiểm tra cấu hình
// lúc khởi động.
func (c *Cipher) KeyIDs() []string {
	out := make([]string, len(c.keyOrder))
	copy(out, c.keyOrder)
	return out
}

// Encrypt mã hoá plain bằng khoá chính.
//
// Định dạng blob:
//
//	byte 0        phiên bản định dạng
//	byte 1        độ dài ID khoá
//	byte 2..n     ID khoá
//	n+1..n+12     nonce
//	còn lại       ciphertext kèm tag xác thực
//
// Phần header (phiên bản, độ dài ID, ID) được đưa vào GCM làm dữ liệu xác thực
// kèm theo. Nhờ vậy không ai sửa được ID khoá trong blob để lừa hệ thống giải mã
// bằng một khoá khác: sửa header là tag không còn khớp.
//
// Nonce lấy từ crypto/rand cho mỗi lần gọi, không bao giờ tái sử dụng. Dùng lại
// một nonce với cùng khoá trong GCM làm mất hoàn toàn tính bí mật và cho phép
// người ngoài giả mạo dữ liệu.
func (c *Cipher) Encrypt(plain []byte) ([]byte, error) {
	aead, ok := c.byKeyID[c.primary]
	if !ok {
		return nil, errors.New("crypto: cipher chưa có khoá chính")
	}

	header := make([]byte, 0, 2+len(c.primary))
	header = append(header, blobVersion, byte(len(c.primary)))
	header = append(header, c.primary...)

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: không đọc được crypto/rand: %w", err)
	}

	// Seal ghi tiếp vào cuối slice truyền vào, nên header và nonce nằm sẵn ở đầu
	// blob mà không phải copy thêm lần nữa.
	blob := make([]byte, 0, len(header)+len(nonce)+len(plain)+aead.Overhead())
	blob = append(blob, header...)
	blob = append(blob, nonce...)
	return aead.Seal(blob, nonce, plain, header), nil
}

// Decrypt giải mã blob, tra khoá theo ID nhúng trong blob.
//
// Lỗi phân biệt được ba trường hợp, vì cách xử lý của chúng khác nhau hoàn toàn:
// ErrInvalidBlob là dữ liệu không phải blob của package này, ErrUnknownKeyID là
// thiếu khoá trong cấu hình (thêm khoá cũ vào là đọc được), ErrDecrypt là sai khoá
// hoặc dữ liệu đã bị sửa.
func (c *Cipher) Decrypt(blob []byte) ([]byte, error) {
	if len(blob) < 2 {
		return nil, fmt.Errorf("%w: chỉ có %d byte", ErrInvalidBlob, len(blob))
	}
	if blob[0] != blobVersion {
		return nil, fmt.Errorf("%w: phiên bản %d không hỗ trợ", ErrInvalidBlob, blob[0])
	}

	idLen := int(blob[1])
	headerLen := 2 + idLen
	if idLen == 0 || len(blob) < headerLen {
		return nil, fmt.Errorf("%w: độ dài ID khoá %d không hợp lệ", ErrInvalidBlob, idLen)
	}

	header := blob[:headerLen]
	keyID := string(blob[2:headerLen])

	aead, ok := c.byKeyID[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKeyID, keyID)
	}

	nonceSize := aead.NonceSize()
	if len(blob) < headerLen+nonceSize+aead.Overhead() {
		return nil, fmt.Errorf("%w: blob bị cắt ngắn", ErrInvalidBlob)
	}
	nonce := blob[headerLen : headerLen+nonceSize]
	ciphertext := blob[headerLen+nonceSize:]

	plain, err := aead.Open(nil, nonce, ciphertext, header)
	if err != nil {
		// Không trả về chi tiết của err: thông báo lỗi chi tiết của GCM không giúp
		// gì cho việc debug mà lại nói cho người thử tấn công biết họ sai ở đâu.
		return nil, fmt.Errorf("%w (khoá %q)", ErrDecrypt, keyID)
	}
	return plain, nil
}
