package crypto_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/crypto"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, crypto.KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("sinh khoá: %v", err)
	}
	return k
}

func mustCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher(mustKey(t))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestVongTronMaHoaGiaiMa(t *testing.T) {
	c := mustCipher(t)

	tests := []struct {
		name  string
		plain []byte
	}{
		{"chuỗi ngắn", []byte("0912345678")},
		{"tiếng Việt", []byte("Nguyễn Văn Ánh, số 1 Lê Duẩn")},
		{"rỗng", []byte{}},
		{"nil", nil},
		{"một byte", []byte{0}},
		{"nhị phân", []byte{0, 1, 2, 255, 254, 0, 0}},
		{"dài", bytes.Repeat([]byte("x"), 100_000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := c.Encrypt(tt.plain)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			got, err := c.Decrypt(blob)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			// bytes.Equal coi nil và slice rỗng là bằng nhau, nên nil/rỗng không
			// cần nhánh riêng.
			if !bytes.Equal(got, tt.plain) {
				t.Errorf("giải mã ra %q, muốn %q", got, tt.plain)
			}
			// Chỉ kiểm tra với dữ liệu đủ dài. Với một byte 0x00, xác suất nó
			// trùng ngẫu nhiên một byte nào đó trong nonce hoặc tag là khoảng
			// 15% — khẳng định này sẽ flaky mà không nói lên điều gì.
			if len(tt.plain) >= 8 && bytes.Contains(blob, tt.plain) {
				t.Error("dữ liệu gốc xuất hiện nguyên vẹn trong blob")
			}
		})
	}
}

// Nonce phải mới mỗi lần: dùng lại nonce với cùng khoá trong GCM làm mất hoàn
// toàn tính bí mật và cho phép giả mạo dữ liệu.
func TestNonceKhongTaiSuDung(t *testing.T) {
	c := mustCipher(t)
	plain := []byte("cùng một nội dung")

	seen := make(map[string]struct{}, 200)
	for i := range 200 {
		blob, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		if _, dup := seen[string(blob)]; dup {
			t.Fatalf("blob trùng ở lần %d — nonce đã bị tái sử dụng", i)
		}
		seen[string(blob)] = struct{}{}
	}
}

// Đây là yêu cầu chính của việc đổi khoá: dữ liệu đã ghi bằng khoá cũ phải đọc
// được sau khi đã chuyển sang khoá mới.
func TestKeyRotation_GiaiMaBlobCuaKhoaCu(t *testing.T) {
	keyCu, keyMoi := mustKey(t), mustKey(t)

	cipherCu, err := crypto.NewCipherWithKeys(crypto.Key{ID: "2025", Key: keyCu})
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}
	blobCu, err := cipherCu.Encrypt([]byte("dữ liệu ghi từ năm ngoái"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Đổi khoá: khoá mới thành khoá chính, khoá cũ giữ lại để đọc dữ liệu cũ.
	sauDoi, err := crypto.NewCipherWithKeys(
		crypto.Key{ID: "2026", Key: keyMoi},
		crypto.Key{ID: "2025", Key: keyCu},
	)
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}

	got, err := sauDoi.Decrypt(blobCu)
	if err != nil {
		t.Fatalf("không đọc được blob của khoá cũ: %v", err)
	}
	if string(got) != "dữ liệu ghi từ năm ngoái" {
		t.Errorf("giải mã ra %q", got)
	}

	// Dữ liệu mới phải được ghi bằng khoá mới.
	blobMoi, err := sauDoi.Encrypt([]byte("dữ liệu mới"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if sauDoi.PrimaryKeyID() != "2026" {
		t.Errorf("PrimaryKeyID = %q, muốn 2026", sauDoi.PrimaryKeyID())
	}
	if !bytes.Contains(blobMoi[:8], []byte("2026")) {
		t.Errorf("blob mới không mang ID khoá mới: %x", blobMoi[:8])
	}

	// Và cipher chỉ có khoá cũ thì không đọc được dữ liệu mới.
	if _, err := cipherCu.Decrypt(blobMoi); !errors.Is(err, crypto.ErrUnknownKeyID) {
		t.Errorf("cipher cũ đọc blob mới ra lỗi %v, muốn ErrUnknownKeyID", err)
	}
}

func TestKeyIDs(t *testing.T) {
	k1, k2, k3 := mustKey(t), mustKey(t), mustKey(t)
	c, err := crypto.NewCipherWithKeys(
		crypto.Key{ID: "c", Key: k1},
		crypto.Key{ID: "b", Key: k2},
		crypto.Key{ID: "a", Key: k3},
	)
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}
	ids := c.KeyIDs()
	if len(ids) != 3 || ids[0] != "c" {
		t.Errorf("KeyIDs = %v, muốn khoá chính đứng đầu", ids)
	}

	// Sửa slice trả về không được ảnh hưởng cipher.
	ids[0] = "bị sửa"
	if c.KeyIDs()[0] != "c" {
		t.Error("KeyIDs trả về slice dùng chung với nội bộ cipher")
	}
}

func TestNewCipher_KhoaSai(t *testing.T) {
	for _, n := range []int{0, 1, 16, 24, 31, 33, 64} {
		if _, err := crypto.NewCipher(make([]byte, n)); !errors.Is(err, crypto.ErrKeySize) {
			t.Errorf("khoá %d byte cho lỗi %v, muốn ErrKeySize", n, err)
		}
	}
	if _, err := crypto.NewCipher(nil); !errors.Is(err, crypto.ErrKeySize) {
		t.Errorf("khoá nil cho lỗi %v, muốn ErrKeySize", err)
	}
}

func TestNewCipherWithKeys_ThamSoSai(t *testing.T) {
	k := mustKey(t)
	tests := []struct {
		name     string
		primary  crypto.Key
		previous []crypto.Key
	}{
		{"ID rỗng", crypto.Key{ID: "", Key: k}, nil},
		{"ID quá dài", crypto.Key{ID: strings.Repeat("x", 256), Key: k}, nil},
		{"ID trùng", crypto.Key{ID: "a", Key: k}, []crypto.Key{{ID: "a", Key: mustKey(t)}}},
		{"khoá cũ sai độ dài", crypto.Key{ID: "a", Key: k}, []crypto.Key{{ID: "b", Key: []byte("ngắn")}}},
		{"khoá cũ ID rỗng", crypto.Key{ID: "a", Key: k}, []crypto.Key{{ID: "", Key: mustKey(t)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := crypto.NewCipherWithKeys(tt.primary, tt.previous...); err == nil {
				t.Error("muốn lỗi, không có lỗi")
			}
		})
	}
}

// ID khoá nằm trong dữ liệu xác thực kèm theo của GCM, nên sửa nó là tag không
// còn khớp — không ai lừa được hệ thống giải mã bằng một khoá khác.
func TestSuaIDKhoaTrongBlobBiPhatHien(t *testing.T) {
	k1, k2 := mustKey(t), mustKey(t)
	c, err := crypto.NewCipherWithKeys(crypto.Key{ID: "1", Key: k1}, crypto.Key{ID: "2", Key: k2})
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}

	blob, err := c.Encrypt([]byte("dữ liệu"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// byte 0 là phiên bản, byte 1 là độ dài ID, byte 2 là ID "1".
	if blob[2] != '1' {
		t.Fatalf("tiền đề sai: byte ID = %q", blob[2])
	}
	blob[2] = '2' // đổi sang khoá khác, một khoá cipher này có thật

	_, err = c.Decrypt(blob)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("Decrypt = %v, muốn ErrDecrypt — ID khoá không được xác thực", err)
	}
}

func TestSuaCiphertextBiPhatHien(t *testing.T) {
	c := mustCipher(t)
	blob, err := c.Encrypt([]byte("số dư: 1000"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Đổi từng byte một, ở mọi vị trí: header, nonce, ciphertext, tag.
	for i := range blob {
		hong := bytes.Clone(blob)
		hong[i] ^= 0xff

		if _, err := c.Decrypt(hong); err == nil {
			t.Errorf("sửa byte %d/%d không bị phát hiện", i, len(blob))
		}
	}
}

func TestDecrypt_BlobSaiDinhDang(t *testing.T) {
	c := mustCipher(t)
	tests := []struct {
		name string
		blob []byte
	}{
		{"nil", nil},
		{"rỗng", []byte{}},
		{"một byte", []byte{1}},
		{"phiên bản lạ", []byte{99, 1, '0', 1, 2, 3}},
		{"độ dài ID bằng 0", []byte{1, 0, 1, 2, 3}},
		{"ID bị cắt", []byte{1, 10, '0'}},
		{"thiếu nonce", append([]byte{1, 1, '0'}, 1, 2, 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Decrypt(tt.blob)
			if err == nil {
				t.Fatal("muốn lỗi, không có lỗi")
			}
			if !errors.Is(err, crypto.ErrInvalidBlob) && !errors.Is(err, crypto.ErrUnknownKeyID) {
				t.Errorf("lỗi %v, muốn ErrInvalidBlob hoặc ErrUnknownKeyID", err)
			}
		})
	}
}

// Sai khoá phải ra ErrDecrypt, không phải giải ra dữ liệu rác.
func TestDecrypt_SaiKhoa(t *testing.T) {
	c1, err := crypto.NewCipherWithKeys(crypto.Key{ID: "same", Key: mustKey(t)})
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}
	c2, err := crypto.NewCipherWithKeys(crypto.Key{ID: "same", Key: mustKey(t)})
	if err != nil {
		t.Fatalf("NewCipherWithKeys: %v", err)
	}

	blob, err := c1.Encrypt([]byte("bí mật"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c2.Decrypt(blob); !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("Decrypt với khoá khác = %v, muốn ErrDecrypt", err)
	}
}

// Thông báo lỗi không được kể chi tiết của GCM: nó không giúp debug mà lại nói
// cho người thử tấn công biết họ sai ở đâu.
func TestLoiGiaiMaKhongLoChiTiet(t *testing.T) {
	c := mustCipher(t)
	blob, err := c.Encrypt([]byte("bí mật"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	blob[len(blob)-1] ^= 0xff

	_, err = c.Decrypt(blob)
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if strings.Contains(err.Error(), "message authentication failed") {
		t.Errorf("lỗi lộ chi tiết nội bộ của GCM: %v", err)
	}
}

func TestNewCipher_DungDefaultKeyID(t *testing.T) {
	c := mustCipher(t)
	if got := c.PrimaryKeyID(); got != crypto.DefaultKeyID {
		t.Errorf("PrimaryKeyID = %q, muốn %q", got, crypto.DefaultKeyID)
	}
}

// Blob của cùng một cipher phải đọc được bởi một cipher khác dựng từ cùng khoá:
// nếu không thì restart process là mất dữ liệu.
func TestBlobDocDuocQuaNhieuInstance(t *testing.T) {
	key := mustKey(t)
	c1, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	blob, err := c1.Encrypt([]byte("bền vững"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	c2, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	got, err := c2.Decrypt(blob)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "bền vững" {
		t.Errorf("giải mã ra %q", got)
	}
}

// Cipher dùng chung giữa nhiều goroutine là cách dùng bình thường (một cipher cho
// cả process). Test này chỉ có nghĩa với -race.
func TestCipher_SongSong(t *testing.T) {
	c := mustCipher(t)
	done := make(chan struct{})

	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 50 {
				blob, err := c.Encrypt([]byte("song song"))
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := c.Decrypt(blob); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for range 4 {
		<-done
	}
}
