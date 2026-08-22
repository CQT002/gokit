package kafka

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/cqt002/gokit/core/secret"
	"github.com/cqt002/gokit/core/tlsx"
)

// SASLMechanism là cơ chế xác thực SASL.
type SASLMechanism string

// Các cơ chế SASL được hỗ trợ.
//
// Không có GSSAPI/Kerberos: nó cần thư viện ngoài và cấu hình keytab ở tầng hệ
// điều hành, tức là một lớp phức tạp mà chưa ai cần tới. Cần thì dựng
// sasl.Mechanism riêng và dùng [ProducerConfig.ClientOpts].
const (
	SASLPlain    SASLMechanism = "plain"
	SASLScram256 SASLMechanism = "scram-sha-256"
	SASLScram512 SASLMechanism = "scram-sha-512"
)

// SASLConfig là thông tin đăng nhập SASL.
type SASLConfig struct {
	// Mechanism là cơ chế xác thực. Bắt buộc.
	Mechanism SASLMechanism `yaml:"mechanism" env:"KAFKA_SASL_MECHANISM"`

	// Username là tên đăng nhập. Bắt buộc.
	Username string `yaml:"username" env:"KAFKA_SASL_USERNAME"`

	// Password là mật khẩu. Kiểu secret.Secret nên nó không lọt ra log khi ai
	// đó in cả struct config.
	Password secret.Secret `yaml:"password" env:"KAFKA_SASL_PASSWORD"`
}

// mechanism dựng sasl.Mechanism của franz-go.
func (s *SASLConfig) mechanism() (sasl.Mechanism, error) {
	if s == nil {
		return nil, nil
	}
	if s.Username == "" {
		return nil, fmt.Errorf("kafka: SASLConfig thiếu Username")
	}

	switch SASLMechanism(strings.ToLower(string(s.Mechanism))) {
	case SASLPlain:
		return plain.Auth{User: s.Username, Pass: s.Password.Reveal()}.AsMechanism(), nil
	case SASLScram256:
		return scram.Auth{User: s.Username, Pass: s.Password.Reveal()}.AsSha256Mechanism(), nil
	case SASLScram512:
		return scram.Auth{User: s.Username, Pass: s.Password.Reveal()}.AsSha512Mechanism(), nil
	case "":
		return nil, fmt.Errorf("kafka: SASLConfig thiếu Mechanism")
	default:
		return nil, fmt.Errorf("kafka: cơ chế SASL %q không được hỗ trợ", s.Mechanism)
	}
}

// Acks là số bản sao phải xác nhận trước khi coi một lần ghi là thành công.
type Acks string

// Các mức xác nhận.
const (
	// AcksAll đợi mọi bản sao trong ISR xác nhận. Mặc định, và là mức duy nhất
	// không mất dữ liệu khi một broker chết.
	AcksAll Acks = "all"

	// AcksLeader chỉ đợi leader. Nhanh hơn, nhưng message ghi xong mà leader
	// chết trước khi bản sao kịp theo kịp thì mất hẳn.
	AcksLeader Acks = "leader"

	// AcksNone không đợi gì. Nhanh nhất và **không có đảm bảo nào** — client
	// không biết message có tới nơi hay không. Chỉ dùng cho dữ liệu vô hại khi
	// mất, ví dụ metric lấy mẫu.
	AcksNone Acks = "none"
)

// kgoAcks đổi Acks thành tuỳ chọn của franz-go.
func (a Acks) kgoAcks() (kgo.Acks, error) {
	switch a {
	case AcksAll, "":
		return kgo.AllISRAcks(), nil
	case AcksLeader:
		return kgo.LeaderAck(), nil
	case AcksNone:
		return kgo.NoAck(), nil
	default:
		return kgo.Acks{}, fmt.Errorf("kafka: RequiredAcks %q không hợp lệ (all|leader|none)", a)
	}
}

// compressionOpt đổi tên thuật toán nén thành tuỳ chọn của franz-go.
//
// Rỗng nghĩa là dùng mặc định của franz-go: thử snappy rồi rơi về không nén.
// Nén ở producer đáng bật gần như luôn — nó đổi CPU lấy băng thông mạng và dung
// lượng đĩa của broker, và với dữ liệu JSON thì tỉ lệ thường là 5–10 lần.
func compressionOpt(name string) (kgo.Opt, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return nil, nil
	case "none":
		return kgo.ProducerBatchCompression(kgo.NoCompression()), nil
	case "gzip":
		return kgo.ProducerBatchCompression(kgo.GzipCompression()), nil
	case "snappy":
		return kgo.ProducerBatchCompression(kgo.SnappyCompression()), nil
	case "lz4":
		return kgo.ProducerBatchCompression(kgo.Lz4Compression()), nil
	case "zstd":
		return kgo.ProducerBatchCompression(kgo.ZstdCompression()), nil
	default:
		return nil, fmt.Errorf("kafka: Compression %q không hợp lệ (none|gzip|snappy|lz4|zstd)", name)
	}
}

// baseOpts dựng phần tuỳ chọn chung của producer và consumer.
func baseOpts(brokers []string, clientID string, saslCfg *SASLConfig, tlsCfg tlsx.Options, log *slog.Logger) ([]kgo.Opt, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("kafka: thiếu Brokers")
	}
	for _, b := range brokers {
		if b == "" {
			return nil, fmt.Errorf("kafka: Brokers có phần tử rỗng")
		}
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.WithLogger(newKgoLogger(log)),
	}
	if clientID != "" {
		// ClientID hiện trong log và metric của broker. Đặt nó bằng tên service
		// là cách rẻ nhất để trả lời "ai đang đọc topic này".
		opts = append(opts, kgo.ClientID(clientID))
	}

	if hasTLS(tlsCfg) {
		c, err := tlsx.ClientConfig(tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("kafka: cấu hình TLS: %w", err)
		}
		opts = append(opts, kgo.DialTLSConfig(c))
	}

	mech, err := saslCfg.mechanism()
	if err != nil {
		return nil, err
	}
	if mech != nil {
		if !hasTLS(tlsCfg) && SASLMechanism(strings.ToLower(string(saslCfg.Mechanism))) == SASLPlain {
			// SASL/PLAIN gửi mật khẩu **nguyên văn** trên dây. Không có TLS thì
			// bất kỳ ai trên đường truyền cũng đọc được — và đây là cấu hình
			// rất dễ vô tình để lọt ra production.
			return nil, fmt.Errorf("kafka: SASL/PLAIN không có TLS sẽ gửi mật khẩu nguyên văn — bật TLS hoặc đổi sang %s", SASLScram512)
		}
		opts = append(opts, kgo.SASL(mech))
	}

	return opts, nil
}

// hasTLS cho biết Options có khai gì để bật TLS không.
func hasTLS(o tlsx.Options) bool {
	return len(o.CertPEM) > 0 || o.CertFile != "" || o.CertB64 != "" ||
		len(o.RootCAPEM) > 0 || o.RootCAFile != "" || o.RootCAB64 != "" ||
		o.InsecureSkipVerify
}
