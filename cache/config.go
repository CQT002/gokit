package cache

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	"github.com/cqt002/gokit/core/secret"
	"github.com/cqt002/gokit/core/tlsx"
)

// Giá trị mặc định của Config.
const (
	DefaultPoolSize      = 10
	DefaultSlowThreshold = 100 * time.Millisecond
)

// Config cấu hình Client.
type Config struct {
	// Addrs là danh sách địa chỉ "host:port". Bắt buộc.
	//
	// Một địa chỉ nghĩa là standalone, nhiều địa chỉ nghĩa là cluster. Không có
	// cờ riêng để chọn chế độ: số địa chỉ đã nói lên điều đó, và một cờ tách
	// rời sẽ có lúc lệch với danh sách.
	Addrs []string `yaml:"addrs" env:"REDIS_ADDRS"`

	// Username cho Redis 6 ACL. Rỗng thì dùng user default.
	Username string `yaml:"username" env:"REDIS_USERNAME"`

	// Password là mật khẩu. Kiểu secret.Secret nên nó không lọt ra log khi ai
	// đó in cả struct Config.
	Password secret.Secret `yaml:"password" env:"REDIS_PASSWORD"`

	// DB là số hiệu database. Chỉ dùng được ở chế độ standalone — Redis Cluster
	// chỉ có database 0, nên khai khác 0 kèm nhiều địa chỉ là lỗi.
	DB int `yaml:"db" env:"REDIS_DB"`

	// TLS bật kết nối mã hoá khi có khai cert hoặc CA. Không khai gì thì kết
	// nối chạy plaintext.
	TLS tlsx.Options `yaml:"tls"`

	// PoolSize là số connection tối đa **cho mỗi node**. 0 → DefaultPoolSize.
	//
	// "Mỗi node" là chỗ dễ tính sai ở chế độ cluster: PoolSize 50 với 6 node
	// nghĩa là tối đa 300 connection từ một instance service.
	PoolSize int `yaml:"pool_size" env:"REDIS_POOL_SIZE"`

	// SlowThreshold là ngưỡng coi một lệnh Redis là chậm. 0 →
	// DefaultSlowThreshold.
	SlowThreshold time.Duration `yaml:"slow_threshold" env:"REDIS_SLOW_THRESHOLD"`

	// Logger ghi log lệnh lỗi và lệnh chậm. nil thì dùng slog.Default().
	//
	// Không dùng redis.SetLogger của go-redis: đó là biến toàn cục, nên hai
	// client trong cùng process không thể ghi log về hai chỗ khác nhau.
	Logger *slog.Logger `yaml:"-"`
}

// validate kiểm tra các field bắt buộc và các tổ hợp không dùng được.
func (c Config) validate() error {
	if len(c.Addrs) == 0 {
		return fmt.Errorf("cache: Config thiếu Addrs")
	}
	for _, a := range c.Addrs {
		if a == "" {
			return fmt.Errorf("cache: Addrs có phần tử rỗng")
		}
	}
	if c.DB != 0 && len(c.Addrs) > 1 {
		return fmt.Errorf("cache: DB = %d nhưng có %d địa chỉ — Redis Cluster chỉ có database 0",
			c.DB, len(c.Addrs))
	}
	if c.PoolSize < 0 {
		return fmt.Errorf("cache: PoolSize không được âm")
	}
	return nil
}

// tlsConfig dựng *tls.Config, hoặc nil khi Config không khai gì về TLS.
func (c Config) tlsConfig() (*tls.Config, error) {
	if !hasTLS(c.TLS) {
		return nil, nil
	}
	cfg, err := tlsx.ClientConfig(c.TLS)
	if err != nil {
		return nil, fmt.Errorf("cache: cấu hình TLS: %w", err)
	}
	return cfg, nil
}

// hasTLS cho biết Options có khai gì để bật TLS không.
//
// InsecureSkipVerify tính là có: một config chỉ đặt cờ đó nghĩa là người dùng
// đang chờ TLS với cert tự ký, không phải plaintext.
func hasTLS(o tlsx.Options) bool {
	return len(o.CertPEM) > 0 || o.CertFile != "" || o.CertB64 != "" ||
		len(o.RootCAPEM) > 0 || o.RootCAFile != "" || o.RootCAB64 != "" ||
		o.InsecureSkipVerify
}
