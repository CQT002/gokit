// Package tlsx dựng *tls.Config từ certificate ở ba dạng nguồn khác nhau.
//
// Ba dạng nguồn tồn tại vì thực tế triển khai có ba kiểu: file trên đĩa khi chạy
// local hoặc mount volume, bytes khi cert lấy từ vault lúc runtime, và base64
// khi cert đi qua biến môi trường — dạng mà Kubernetes Secret hay dùng và cũng là
// dạng duy nhất nhét được PEM nhiều dòng vào một env var.
//
// Mỗi loại vật liệu (cert, key, CA) chỉ được khai bằng đúng một dạng nguồn. Khai
// hai dạng cho cùng một thứ là lỗi, không phải thứ tự ưu tiên: khi cert trên đĩa
// khác cert trong env, đoán xem cái nào thắng là cách tự tạo ra sự cố production
// không ai debug được.
package tlsx

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// ErrConflictingSources là lỗi khi một loại vật liệu được khai bằng nhiều dạng
// nguồn cùng lúc.
var ErrConflictingSources = errors.New("tlsx: một loại vật liệu được khai bằng nhiều nguồn")

// Options gom mọi cách khai certificate.
//
// Với mỗi loại vật liệu, khai đúng một trong ba dạng: *PEM (bytes), *File (đường
// dẫn), hoặc *B64 (base64 của PEM).
type Options struct {
	// CertPEM, KeyPEM, RootCAPEM là nội dung PEM dạng bytes.
	CertPEM, KeyPEM, RootCAPEM []byte

	// CertFile, KeyFile, RootCAFile là đường dẫn tới file PEM.
	CertFile, KeyFile, RootCAFile string

	// CertB64, KeyB64, RootCAB64 là PEM đã encode base64, cho trường hợp cert đi
	// qua biến môi trường. Nhận cả dạng có padding và không padding.
	CertB64, KeyB64, RootCAB64 string

	// ServerName là tên host dùng để verify certificate của server. Chỉ có tác
	// dụng với ClientConfig.
	//
	// Cần khai khi kết nối bằng IP hoặc qua tunnel, tức là khi tên host dùng để
	// quay số khác tên trong certificate.
	ServerName string

	// InsecureSkipVerify tắt việc verify certificate của server. Chỉ có tác dụng
	// với ClientConfig.
	//
	// Bật cái này là bỏ toàn bộ giá trị bảo mật của TLS: kết nối vẫn được mã hoá
	// nhưng không còn biết đang nói chuyện với ai, nên hết chống được
	// man-in-the-middle. Chỉ dùng khi test với cert tự ký, và đừng để nó đi ra
	// production kèm giá trị true trong file config.
	InsecureSkipVerify bool

	// MinVersion là phiên bản TLS thấp nhất được chấp nhận. 0 nghĩa là TLS 1.2.
	//
	// Không hạ xuống dưới TLS 1.2: TLS 1.0 và 1.1 đã bị các tiêu chuẩn thanh toán
	// loại bỏ và có lỗ hổng đã công bố.
	MinVersion uint16
}

// ServerConfig dựng tls.Config cho server.
//
// Bắt buộc có cert và key. Nếu khai thêm RootCA thì bật mTLS: server sẽ đòi và
// verify certificate của client bằng CA đó.
func ServerConfig(o Options) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: minVersion(o)}

	cert, err := keyPair(o)
	if err != nil {
		return nil, err
	}
	if cert == nil {
		return nil, errors.New("tlsx: ServerConfig cần cert và key")
	}
	cfg.Certificates = []tls.Certificate{*cert}

	pool, err := caPool(o)
	if err != nil {
		return nil, err
	}
	if pool != nil {
		// Có CA nghĩa là người dùng muốn xác thực client. Đòi và verify luôn —
		// một CA được khai mà không dùng để verify là cấu hình bảo mật giả.
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// ClientConfig dựng tls.Config cho client.
//
// Cert và key là tuỳ chọn, chỉ cần khi server đòi mTLS. RootCA để verify server;
// không khai thì dùng CA pool của hệ thống.
func ClientConfig(o Options) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: minVersion(o),
		ServerName: o.ServerName,
		//nolint:gosec // giá trị do chỗ gọi khai, và godoc của field đã nói rõ hệ quả
		InsecureSkipVerify: o.InsecureSkipVerify,
	}

	cert, err := keyPair(o)
	if err != nil {
		return nil, err
	}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}

	pool, err := caPool(o)
	if err != nil {
		return nil, err
	}
	cfg.RootCAs = pool // nil nghĩa là dùng CA pool của hệ thống
	return cfg, nil
}

func minVersion(o Options) uint16 {
	if o.MinVersion == 0 {
		return tls.VersionTLS12
	}
	return o.MinVersion
}

// keyPair đọc cert và key. Trả về nil, nil nếu không khai gì.
func keyPair(o Options) (*tls.Certificate, error) {
	certPEM, err := material("cert", o.CertPEM, o.CertFile, o.CertB64)
	if err != nil {
		return nil, err
	}
	keyPEM, err := material("key", o.KeyPEM, o.KeyFile, o.KeyB64)
	if err != nil {
		return nil, err
	}

	switch {
	case certPEM == nil && keyPEM == nil:
		return nil, nil
	case certPEM == nil:
		return nil, errors.New("tlsx: có key nhưng thiếu cert")
	case keyPEM == nil:
		return nil, errors.New("tlsx: có cert nhưng thiếu key")
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("tlsx: cặp cert/key không dùng được: %w", err)
	}
	return &cert, nil
}

// caPool đọc root CA. Trả về nil nếu không khai gì.
func caPool(o Options) (*x509.CertPool, error) {
	pem, err := material("root CA", o.RootCAPEM, o.RootCAFile, o.RootCAB64)
	if err != nil {
		return nil, err
	}
	if pem == nil {
		return nil, nil
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		// AppendCertsFromPEM chỉ trả false khi không parse được certificate nào.
		// Im lặng bỏ qua sẽ để lại một pool rỗng, và mọi kết nối fail với thông
		// báo hoàn toàn không liên quan tới nguyên nhân thật.
		return nil, errors.New("tlsx: root CA không chứa certificate nào parse được")
	}
	return pool, nil
}

// material lấy nội dung PEM từ đúng một trong ba dạng nguồn.
func material(name string, pem []byte, file, b64 string) ([]byte, error) {
	var count int
	if len(pem) > 0 {
		count++
	}
	if file != "" {
		count++
	}
	if b64 != "" {
		count++
	}

	switch {
	case count == 0:
		return nil, nil
	case count > 1:
		return nil, fmt.Errorf("%w: %s khai %d nguồn", ErrConflictingSources, name, count)
	}

	switch {
	case len(pem) > 0:
		return pem, nil

	case file != "":
		// Đường dẫn cert do app khai trong config, không phải từ input người dùng.
		//nolint:gosec // G304: đường dẫn certificate do app cung cấp
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("tlsx: đọc %s từ %q: %w", name, file, err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("tlsx: file %s %q rỗng", name, file)
		}
		return b, nil

	default:
		b, err := decodeB64(b64)
		if err != nil {
			return nil, fmt.Errorf("tlsx: giải base64 cho %s: %w", name, err)
		}
		return b, nil
	}
}

// decodeB64 nhận cả base64 có padding và không padding — biến môi trường đi qua
// nhiều tầng công cụ và không phải tầng nào cũng giữ dấu `=`.
func decodeB64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}
