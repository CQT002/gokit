package crypto_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/cqt002/gokit/core/crypto"
	"github.com/cqt002/gokit/core/secret"
)

const matKhau = secret.Secret("M4tKh@u-R4tD@i-2026")

func TestHashPassword_DinhDangPHC(t *testing.T) {
	hash, err := crypto.HashPassword(matKhau)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" {
		t.Fatalf("hash = %q, muốn 6 phần theo định dạng PHC", hash)
	}
	if parts[1] != "argon2id" {
		t.Errorf("thuật toán = %q, muốn argon2id", parts[1])
	}
	if parts[2] != "v=19" {
		t.Errorf("phiên bản = %q, muốn v=19", parts[2])
	}
	// Tham số phải nằm trong chuỗi hash — đó là điều kiện để đổi tham số về sau
	// mà hash cũ vẫn verify được.
	for _, want := range []string{"m=", "t=", "p="} {
		if !strings.Contains(parts[3], want) {
			t.Errorf("phần tham số %q thiếu %q", parts[3], want)
		}
	}
	if strings.Contains(hash, matKhau.Reveal()) {
		t.Fatal("mật khẩu xuất hiện nguyên vẹn trong hash")
	}
}

// Salt phải mới mỗi lần: cùng mật khẩu ra cùng hash nghĩa là hai người dùng cùng
// mật khẩu nhìn thấy được điều đó qua bảng dữ liệu, và rainbow table dùng lại được.
func TestHashPassword_SaltKhacNhauMoiLan(t *testing.T) {
	// 8 lần là đủ để bắt salt không đổi. argon2 cố tình tốn 19MiB và vài chục ms
	// mỗi lần gọi, nên vòng lặp dài chỉ làm CI chậm chứ không tăng khả năng phát hiện.
	seen := make(map[string]struct{}, 8)
	for i := range 8 {
		hash, err := crypto.HashPassword(matKhau)
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		if _, dup := seen[hash]; dup {
			t.Fatalf("hash trùng ở lần %d — salt không đổi", i)
		}
		seen[hash] = struct{}{}

		if !crypto.VerifyPassword(matKhau, hash) {
			t.Fatal("hash vừa tạo không verify được")
		}
	}
}

func TestVerifyPassword(t *testing.T) {
	hash, err := crypto.HashPassword(matKhau)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !crypto.VerifyPassword(matKhau, hash) {
		t.Error("mật khẩu đúng bị từ chối")
	}

	tests := []struct {
		name string
		pw   secret.Secret
	}{
		{"sai hoàn toàn", "khác hẳn"},
		{"lệch một ký tự", secret.Secret(matKhau.Reveal() + "x")},
		{"thiếu ký tự cuối", secret.Secret(matKhau.Reveal()[:len(matKhau.Reveal())-1])},
		{"khác hoa thường", secret.Secret(strings.ToLower(matKhau.Reveal()))},
		{"rỗng", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if crypto.VerifyPassword(tt.pw, hash) {
				t.Error("VerifyPassword = true, muốn false")
			}
		})
	}
}

// Mật khẩu rỗng bị từ chối ngay từ lúc hash: nếu hash được, VerifyPassword("")
// sẽ trả true và đó là đường vào hệ thống không cần mật khẩu.
func TestHashPassword_MatKhauRong(t *testing.T) {
	_, err := crypto.HashPassword("")
	if !errors.Is(err, crypto.ErrEmptyPassword) {
		t.Errorf("HashPassword(\"\") = %v, muốn ErrEmptyPassword", err)
	}
}

// Hash sai định dạng trả false chứ không panic: chỗ gọi là đường đăng nhập, và
// một cột DB bị hỏng không được làm sập service.
func TestVerifyPassword_HashSaiDinhDang(t *testing.T) {
	valid, err := crypto.HashPassword(matKhau)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(valid, "$")

	tests := []struct {
		name string
		hash string
	}{
		{"rỗng", ""},
		{"chuỗi thường", "khong-phai-hash"},
		{"thiếu phần", "$argon2id$v=19$m=19456,t=2,p=1"},
		{"thừa phần", valid + "$thua"},
		{"thuật toán khác", strings.Replace(valid, "argon2id", "argon2i", 1)},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"phiên bản lạ", strings.Replace(valid, "v=19", "v=16", 1)},
		{"phiên bản không phải số", strings.Replace(valid, "v=19", "v=abc", 1)},
		{"tham số lạ", strings.Replace(valid, "m=", "z=", 1)},
		{"tham số không phải số", strings.Replace(valid, "t=2", "t=abc", 1)},
		{"tham số bằng 0", strings.Replace(valid, "t=2", "t=0", 1)},
		{"thiếu tham số p", "$argon2id$v=19$m=19456,t=2$" + parts[4] + "$" + parts[5]},
		{"p quá lớn", strings.Replace(valid, "p=1", "p=999", 1)},
		{"salt không phải base64", "$argon2id$v=19$m=19456,t=2,p=1$!!!$" + parts[5]},
		{"hash không phải base64", "$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$!!!"},
		{"salt rỗng", "$argon2id$v=19$m=19456,t=2,p=1$$" + parts[5]},
		{"hash rỗng", "$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if crypto.VerifyPassword(matKhau, tt.hash) {
				t.Error("VerifyPassword = true với hash sai định dạng")
			}
		})
	}
}

// Verify phải dùng tham số ghi trong hash, không phải tham số hiện hành — đó là
// điều kiện để đổi tham số mà không bắt người dùng đặt lại mật khẩu.
func TestVerifyPassword_DungThamSoTrongHash(t *testing.T) {
	// Hash tạo với tham số yếu hơn mức hiện hành (m=8192, t=1), vẫn phải verify được.
	yeu := "$argon2id$v=19$m=8192,t=1,p=1$" + saltVaHashYeu(t)
	if !crypto.VerifyPassword(matKhau, yeu) {
		t.Error("không verify được hash tạo bằng tham số cũ")
	}
}

// saltVaHashYeu tạo phần salt$hash bằng tham số yếu, gọi thẳng argon2 của
// x/crypto để không phụ thuộc vào nội bộ package đang test.
func saltVaHashYeu(t *testing.T) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	sum := argon2.IDKey([]byte(matKhau.Reveal()), salt, 1, 8192, 1, 32)
	b64 := base64.RawStdEncoding
	return b64.EncodeToString(salt) + "$" + b64.EncodeToString(sum)
}

func TestNeedsRehash(t *testing.T) {
	hienHanh, err := crypto.HashPassword(matKhau)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if crypto.NeedsRehash(hienHanh) {
		t.Error("hash vừa tạo bằng tham số hiện hành lại bị coi là cần hash lại")
	}

	tests := []struct {
		name string
		hash string
		want bool
	}{
		{"memory yếu hơn", strings.Replace(hienHanh, "m=19456", "m=8192", 1), true},
		{"time ít hơn", strings.Replace(hienHanh, "t=2", "t=1", 1), true},
		{"memory cao hơn", strings.Replace(hienHanh, "m=19456", "m=65536", 1), false},
		{"sai định dạng", "khong-phai-hash", true},
		{"rỗng", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := crypto.NeedsRehash(tt.hash); got != tt.want {
				t.Errorf("NeedsRehash = %v, muốn %v", got, tt.want)
			}
		})
	}
}
