package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/cqt002/gokit/core/secret"
)

// Tham số argon2id, theo một trong các cấu hình OWASP khuyến nghị hiện hành
// (m=19456 KiB, t=2, p=1).
//
// Cố tình không cho cấu hình: tham số quá thấp làm hash bị brute force, và người
// khai thường không có cơ sở nào để chọn khác. Đổi thì đổi ở đây, và nhờ tham số
// được ghi vào chuỗi hash nên hash cũ vẫn verify được sau khi đổi.
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32
)

// argonVersion là phiên bản argon2 trong chuỗi hash (0x13 = 19).
const argonVersion = argon2.Version

// Các lỗi của phần mật khẩu.
var (
	// ErrEmptyPassword là lỗi khi hash một mật khẩu rỗng.
	ErrEmptyPassword = errors.New("crypto: mật khẩu rỗng")
	// ErrInvalidHash là lỗi khi chuỗi hash sai định dạng.
	ErrInvalidHash = errors.New("crypto: chuỗi hash sai định dạng")
)

// HashPassword hash mật khẩu bằng argon2id.
//
// Kết quả theo định dạng PHC, mang theo cả tham số và salt:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt base64>$<hash base64>
//
// Ghi tham số vào chuỗi hash là điều kiện để đổi tham số về sau: hash cũ verify
// bằng tham số của chính nó, hash mới dùng tham số mới, không cần bắt người dùng
// đổi mật khẩu.
//
// Mật khẩu rỗng bị từ chối. Hash một chuỗi rỗng rồi để VerifyPassword("") trả về
// true là con đường vào hệ thống mà không cần mật khẩu, và nó thường xuất hiện do
// một nhánh code quên validate chứ không do ai cố ý.
func HashPassword(pw secret.Secret) (string, error) {
	if pw.IsZero() {
		return "", ErrEmptyPassword
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: không đọc được crypto/rand: %w", err)
	}

	sum := argon2.IDKey([]byte(pw.Reveal()), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argonVersion, argonMemoryKiB, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(sum)), nil
}

// VerifyPassword kiểm tra pw có khớp hash không.
//
// Tính lại bằng đúng tham số ghi trong hash, rồi so sánh bằng thời gian không đổi.
// Chuỗi hash sai định dạng trả về false chứ không lỗi: chỗ gọi là đường đăng nhập,
// và ở đó "không khớp" là câu trả lời duy nhất cần đưa ra.
func VerifyPassword(pw secret.Secret, hash string) bool {
	p, salt, want, err := parsePHC(hash)
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(pw.Reveal()), salt, p.time, p.memoryKiB, p.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NeedsRehash cho biết hash có đang dùng tham số yếu hơn mức hiện hành không.
//
// Gọi sau khi VerifyPassword thành công: lúc đó đang có mật khẩu dạng rõ trong tay,
// và đó là thời điểm duy nhất hash lại được mà không phải nhờ người dùng làm gì.
// Chuỗi hash sai định dạng cũng trả về true — cần thay bằng hash đúng định dạng.
func NeedsRehash(hash string) bool {
	p, _, want, err := parsePHC(hash)
	if err != nil {
		return true
	}
	return p.memoryKiB < argonMemoryKiB ||
		p.time < argonTime ||
		len(want) < argonKeyLen
}

// argonParams là tham số đọc ra từ chuỗi hash.
type argonParams struct {
	memoryKiB uint32
	time      uint32
	threads   uint8
}

// parsePHC tách chuỗi hash định dạng PHC thành tham số, salt và hash.
func parsePHC(hash string) (p argonParams, salt, sum []byte, err error) {
	// Dạng "$argon2id$v=19$m=...,t=...,p=...$salt$hash" tách ra 6 phần, phần đầu rỗng.
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" {
		return p, nil, nil, fmt.Errorf("%w: có %d phần", ErrInvalidHash, len(parts))
	}
	if parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("%w: thuật toán %q không hỗ trợ", ErrInvalidHash, parts[1])
	}

	versionStr, ok := strings.CutPrefix(parts[2], "v=")
	if !ok {
		return p, nil, nil, fmt.Errorf("%w: phần phiên bản %q", ErrInvalidHash, parts[2])
	}
	version, convErr := strconv.Atoi(versionStr)
	if convErr != nil {
		return p, nil, nil, fmt.Errorf("%w: phần phiên bản %q", ErrInvalidHash, parts[2])
	}
	if version != argonVersion {
		return p, nil, nil, fmt.Errorf("%w: phiên bản argon2 %d không hỗ trợ", ErrInvalidHash, version)
	}

	if p, err = parseArgonParams(parts[3]); err != nil {
		return p, nil, nil, err
	}

	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(parts[4]); err != nil {
		return p, nil, nil, fmt.Errorf("%w: salt không phải base64", ErrInvalidHash)
	}
	if sum, err = b64.DecodeString(parts[5]); err != nil {
		return p, nil, nil, fmt.Errorf("%w: hash không phải base64", ErrInvalidHash)
	}
	if len(salt) == 0 || len(sum) == 0 {
		return p, nil, nil, fmt.Errorf("%w: salt hoặc hash rỗng", ErrInvalidHash)
	}
	return p, salt, sum, nil
}

// parseArgonParams đọc "m=19456,t=2,p=1".
func parseArgonParams(s string) (argonParams, error) {
	var p argonParams
	for _, kv := range strings.Split(s, ",") {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return p, fmt.Errorf("%w: tham số %q", ErrInvalidHash, kv)
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil || n == 0 {
			return p, fmt.Errorf("%w: giá trị tham số %q", ErrInvalidHash, kv)
		}

		switch name {
		case "m":
			p.memoryKiB = uint32(n)
		case "t":
			p.time = uint32(n)
		case "p":
			if n > 255 {
				return p, fmt.Errorf("%w: p=%d quá lớn", ErrInvalidHash, n)
			}
			p.threads = uint8(n)
		default:
			return p, fmt.Errorf("%w: tham số lạ %q", ErrInvalidHash, name)
		}
	}
	if p.memoryKiB == 0 || p.time == 0 || p.threads == 0 {
		return p, fmt.Errorf("%w: thiếu tham số m, t hoặc p", ErrInvalidHash)
	}
	return p, nil
}
