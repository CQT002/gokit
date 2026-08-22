package tlsx_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/tlsx"
)

// selfSigned sinh một certificate tự ký dùng được cả làm cert của server, cert
// của client, và CA để verify chính nó.
//
// Sinh tại chỗ thay vì để file fixture trong repo: fixture cert luôn hết hạn vào
// một ngày nào đó và test bắt đầu fail vì lý do không liên quan gì tới code.
func selfSigned(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("sinh khoá: %v", err)
	}

	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gokit-test"},
		DNSNames:              []string{"gokit-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("tạo certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal khoá: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func writeTemp(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("ghi %s: %v", name, err)
	}
	return path
}

// Ba dạng nguồn phải cho ra cùng một kết quả — đó là cả lý do gộp chúng vào một
// struct thay vì có ba hàm khác nhau.
func TestServerConfig_BaDangNguonNhuNhau(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	certFile := writeTemp(t, "cert.pem", certPEM)
	keyFile := writeTemp(t, "key.pem", keyPEM)

	tests := []struct {
		name string
		opts tlsx.Options
	}{
		{"bytes", tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM}},
		{"file", tlsx.Options{CertFile: certFile, KeyFile: keyFile}},
		{"base64", tlsx.Options{
			CertB64: base64.StdEncoding.EncodeToString(certPEM),
			KeyB64:  base64.StdEncoding.EncodeToString(keyPEM),
		}},
		{"base64 không padding", tlsx.Options{
			CertB64: base64.RawStdEncoding.EncodeToString(certPEM),
			KeyB64:  base64.RawStdEncoding.EncodeToString(keyPEM),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := tlsx.ServerConfig(tt.opts)
			if err != nil {
				t.Fatalf("ServerConfig: %v", err)
			}
			if len(cfg.Certificates) != 1 {
				t.Errorf("số certificate = %d, muốn 1", len(cfg.Certificates))
			}
			if cfg.MinVersion != tls.VersionTLS12 {
				t.Errorf("MinVersion = %x, muốn TLS 1.2", cfg.MinVersion)
			}
			if cfg.ClientAuth != tls.NoClientCert {
				t.Errorf("ClientAuth = %v, không khai CA thì không được đòi cert client", cfg.ClientAuth)
			}
		})
	}
}

func TestServerConfig_ThieuCertHoacKey(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	tests := []struct {
		name string
		opts tlsx.Options
	}{
		{"không khai gì", tlsx.Options{}},
		{"chỉ có cert", tlsx.Options{CertPEM: certPEM}},
		{"chỉ có key", tlsx.Options{KeyPEM: keyPEM}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tlsx.ServerConfig(tt.opts); err == nil {
				t.Error("muốn lỗi, không có lỗi")
			}
		})
	}
}

// Khai CA cho server nghĩa là muốn xác thực client; một CA được khai mà không
// dùng để verify là cấu hình bảo mật giả.
func TestServerConfig_CoCAThiBatmTLS(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	cfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: certPEM})
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs = nil")
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, muốn RequireAndVerifyClientCert", cfg.ClientAuth)
	}
}

func TestClientConfig(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	t.Run("không cert vẫn dùng được", func(t *testing.T) {
		cfg, err := tlsx.ClientConfig(tlsx.Options{ServerName: "api.example.com"})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if len(cfg.Certificates) != 0 {
			t.Errorf("số certificate = %d, muốn 0", len(cfg.Certificates))
		}
		if cfg.RootCAs != nil {
			t.Error("RootCAs phải là nil để dùng CA pool của hệ thống")
		}
		if cfg.ServerName != "api.example.com" {
			t.Errorf("ServerName = %q", cfg.ServerName)
		}
	})

	t.Run("có cert cho mTLS", func(t *testing.T) {
		cfg, err := tlsx.ClientConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: certPEM})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if len(cfg.Certificates) != 1 {
			t.Errorf("số certificate = %d, muốn 1", len(cfg.Certificates))
		}
		if cfg.RootCAs == nil {
			t.Error("RootCAs = nil")
		}
	})

	t.Run("InsecureSkipVerify được truyền qua", func(t *testing.T) {
		cfg, err := tlsx.ClientConfig(tlsx.Options{InsecureSkipVerify: true})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if !cfg.InsecureSkipVerify {
			t.Error("InsecureSkipVerify = false")
		}
	})
}

func TestMinVersion(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	cfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatalf("ServerConfig: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %x, muốn TLS 1.3", cfg.MinVersion)
	}

	clientCfg, err := tlsx.ClientConfig(tlsx.Options{})
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if clientCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion mặc định = %x, muốn TLS 1.2", clientCfg.MinVersion)
	}
}

// Khai hai nguồn cho cùng một thứ là lỗi, không phải thứ tự ưu tiên: khi cert
// trên đĩa khác cert trong env, đoán xem cái nào thắng là tự tạo sự cố.
func TestNguonXungDot(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	certFile := writeTemp(t, "cert.pem", certPEM)
	b64 := base64.StdEncoding.EncodeToString(certPEM)

	tests := []struct {
		name string
		opts tlsx.Options
	}{
		{"cert: bytes và file", tlsx.Options{CertPEM: certPEM, CertFile: certFile, KeyPEM: keyPEM}},
		{"cert: bytes và base64", tlsx.Options{CertPEM: certPEM, CertB64: b64, KeyPEM: keyPEM}},
		{"cert: file và base64", tlsx.Options{CertFile: certFile, CertB64: b64, KeyPEM: keyPEM}},
		{"cert: cả ba", tlsx.Options{CertPEM: certPEM, CertFile: certFile, CertB64: b64, KeyPEM: keyPEM}},
		{"key: bytes và base64", tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, KeyB64: b64}},
		{"CA: bytes và file", tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: certPEM, RootCAFile: certFile}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tlsx.ServerConfig(tt.opts)
			if err == nil {
				t.Fatal("muốn lỗi, không có lỗi")
			}
			if !errors.Is(err, tlsx.ErrConflictingSources) {
				t.Errorf("lỗi %v không bọc ErrConflictingSources", err)
			}
		})
	}
}

func TestNguonKhongDocDuoc(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	tests := []struct {
		name string
		opts tlsx.Options
	}{
		{"file không tồn tại", tlsx.Options{CertFile: "/khong/ton/tai.pem", KeyPEM: keyPEM}},
		{"file rỗng", tlsx.Options{CertFile: writeTemp(t, "rong.pem", nil), KeyPEM: keyPEM}},
		{"base64 rác", tlsx.Options{CertB64: "!!!khong-phai-base64!!!", KeyPEM: keyPEM}},
		{"PEM không hợp lệ", tlsx.Options{CertPEM: []byte("day khong phai PEM"), KeyPEM: keyPEM}},
		{"cert và key không khớp nhau", tlsx.Options{CertPEM: certPEM, KeyPEM: mustOtherKey(t)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tlsx.ServerConfig(tt.opts); err == nil {
				t.Error("muốn lỗi, không có lỗi")
			}
		})
	}
}

func mustOtherKey(t *testing.T) []byte {
	t.Helper()
	_, keyPEM := selfSigned(t)
	return keyPEM
}

// Im lặng bỏ qua CA không parse được sẽ để lại pool rỗng, và mọi kết nối fail với
// thông báo không liên quan gì tới nguyên nhân thật.
func TestCAKhongParseDuoc(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	opts := tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: []byte("khong phai certificate")}

	if _, err := tlsx.ServerConfig(opts); err == nil {
		t.Error("ServerConfig không báo lỗi với CA rác")
	}
	if _, err := tlsx.ClientConfig(opts); err == nil {
		t.Error("ClientConfig không báo lỗi với CA rác")
	}
}

// Test mạnh nhất: hai config do package này dựng ra phải bắt tay được với nhau.
// Mọi khẳng định về field ở trên đều có thể đúng mà handshake vẫn fail.
func TestHandshake(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	t.Run("TLS một chiều", func(t *testing.T) {
		serverCfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM})
		if err != nil {
			t.Fatalf("ServerConfig: %v", err)
		}
		clientCfg, err := tlsx.ClientConfig(tlsx.Options{RootCAPEM: certPEM, ServerName: "gokit-test"})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if err := handshake(t, serverCfg, clientCfg); err != nil {
			t.Errorf("handshake: %v", err)
		}
	})

	t.Run("mTLS", func(t *testing.T) {
		serverCfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: certPEM})
		if err != nil {
			t.Fatalf("ServerConfig: %v", err)
		}
		clientCfg, err := tlsx.ClientConfig(tlsx.Options{
			CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: certPEM, ServerName: "gokit-test",
		})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if err := handshake(t, serverCfg, clientCfg); err != nil {
			t.Errorf("handshake mTLS: %v", err)
		}
	})

	t.Run("mTLS từ chối client không có cert", func(t *testing.T) {
		serverCfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM, RootCAPEM: certPEM})
		if err != nil {
			t.Fatalf("ServerConfig: %v", err)
		}
		clientCfg, err := tlsx.ClientConfig(tlsx.Options{RootCAPEM: certPEM, ServerName: "gokit-test"})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if err := handshake(t, serverCfg, clientCfg); err == nil {
			t.Error("server nhận client không có certificate dù đã bật mTLS")
		}
	})

	t.Run("client từ chối cert không tin cậy", func(t *testing.T) {
		serverCfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM})
		if err != nil {
			t.Fatalf("ServerConfig: %v", err)
		}
		// Không khai RootCA: cert tự ký không nằm trong CA pool hệ thống.
		clientCfg, err := tlsx.ClientConfig(tlsx.Options{ServerName: "gokit-test"})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if err := handshake(t, serverCfg, clientCfg); err == nil {
			t.Error("client nhận certificate tự ký không tin cậy")
		}
	})

	t.Run("InsecureSkipVerify bỏ qua verify", func(t *testing.T) {
		serverCfg, err := tlsx.ServerConfig(tlsx.Options{CertPEM: certPEM, KeyPEM: keyPEM})
		if err != nil {
			t.Fatalf("ServerConfig: %v", err)
		}
		clientCfg, err := tlsx.ClientConfig(tlsx.Options{InsecureSkipVerify: true})
		if err != nil {
			t.Fatalf("ClientConfig: %v", err)
		}
		if err := handshake(t, serverCfg, clientCfg); err != nil {
			t.Errorf("handshake: %v", err)
		}
	})
}

// handshake bắt tay TLS qua một socket TCP loopback và trả lỗi của phía client,
// hoặc lỗi của phía server nếu client không thấy vấn đề gì.
//
// Dùng TCP thật chứ không phải net.Pipe: net.Pipe không có buffer nên mỗi Write
// chặn tới khi phía kia Read. Khi một phía từ chối handshake giữa đường, phía còn
// lại treo trong Write và chỉ thoát khi hết deadline — test vẫn đúng nhưng mỗi ca
// lỗi tốn trọn vẹn deadline.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) error {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		// Đóng khi xong: nhờ vậy handshake thất bại phía server làm read của
		// client bung ra ngay, không phải đợi deadline.
		defer func() { _ = conn.Close() }()

		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		done <- tls.Server(conn, serverCfg).Handshake()
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	clientErr := tls.Client(raw, clientCfg).Handshake()

	// Vẫn phải đọc kết quả phía server để goroutine không rò. Phía nào phát hiện
	// vấn đề trước là không xác định, nên chỉ dùng lỗi của server khi client
	// không báo gì.
	select {
	case serverErr := <-done:
		if clientErr == nil {
			return serverErr
		}
	case <-time.After(10 * time.Second):
		return errors.New("server không hoàn tất handshake sau 10 giây")
	}
	return clientErr
}
