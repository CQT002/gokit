package kafka_test

import (
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/secret"
	"github.com/cqt002/gokit/core/tlsx"
	"github.com/cqt002/gokit/queue/kafka"
)

func TestNewProducer_ThieuBrokers(t *testing.T) {
	_, err := kafka.NewProducer(kafka.ProducerConfig{})
	if err == nil {
		t.Fatal("Config rỗng mà NewProducer không báo lỗi")
	}
	if !strings.Contains(err.Error(), "Brokers") {
		t.Errorf("lỗi không nhắc Brokers: %v", err)
	}
}

func TestNewProducer_BrokerRong(t *testing.T) {
	_, err := kafka.NewProducer(kafka.ProducerConfig{Brokers: []string{"a:9092", ""}})
	if err == nil {
		t.Fatal("Brokers có phần tử rỗng mà không báo lỗi")
	}
}

func TestNewProducer_AcksLa(t *testing.T) {
	_, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers:      []string{"a:9092"},
		RequiredAcks: "mot-nua",
	})
	if err == nil {
		t.Fatal("Acks không hợp lệ mà không báo lỗi")
	}
}

// franz-go bật ghi idempotent mặc định và cơ chế đó đòi acks=all. Hạ acks mà
// quên tắt idempotent thì client trả lỗi cấu hình rất khó hiểu.
func TestNewProducer_AcksThapVanDungDuoc(t *testing.T) {
	for _, acks := range []kafka.Acks{kafka.AcksNone, kafka.AcksLeader, kafka.AcksAll, ""} {
		p, err := kafka.NewProducer(kafka.ProducerConfig{
			Brokers:      []string{"127.0.0.1:9092"},
			RequiredAcks: acks,
			Logger:       quietLogger(),
		})
		if err != nil {
			t.Errorf("acks %q: %v", acks, err)
			continue
		}
		p.Close()
	}
}

func TestNewProducer_Compression(t *testing.T) {
	for _, name := range []string{"", "none", "gzip", "snappy", "lz4", "zstd", "GZIP"} {
		p, err := kafka.NewProducer(kafka.ProducerConfig{
			Brokers:     []string{"127.0.0.1:9092"},
			Compression: name,
			Logger:      quietLogger(),
		})
		if err != nil {
			t.Errorf("compression %q: %v", name, err)
			continue
		}
		p.Close()
	}

	_, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers:     []string{"127.0.0.1:9092"},
		Compression: "brotli",
	})
	if err == nil {
		t.Error("thuật toán nén không hỗ trợ mà không báo lỗi")
	}
}

func TestSASL_CacCoChe(t *testing.T) {
	for _, mech := range []kafka.SASLMechanism{kafka.SASLPlain, kafka.SASLScram256, kafka.SASLScram512} {
		p, err := kafka.NewProducer(kafka.ProducerConfig{
			Brokers: []string{"127.0.0.1:9092"},
			SASL: &kafka.SASLConfig{
				Mechanism: mech,
				Username:  "u",
				Password:  secret.Secret("p"),
			},
			// PLAIN đòi TLS, nên bật cho cả ba để test đúng một thứ.
			TLS:    tlsx.Options{InsecureSkipVerify: true},
			Logger: quietLogger(),
		})
		if err != nil {
			t.Errorf("cơ chế %q: %v", mech, err)
			continue
		}
		p.Close()
	}
}

func TestSASL_ThieuThongTin(t *testing.T) {
	tests := map[string]*kafka.SASLConfig{
		"thiếu Mechanism": {Username: "u"},
		"thiếu Username":  {Mechanism: kafka.SASLScram512},
		"cơ chế lạ":       {Mechanism: "gssapi", Username: "u"},
	}
	for name, sasl := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := kafka.NewProducer(kafka.ProducerConfig{
				Brokers: []string{"127.0.0.1:9092"},
				SASL:    sasl,
			})
			if err == nil {
				t.Fatal("cấu hình SASL sai mà không báo lỗi")
			}
		})
	}
}

// SASL/PLAIN gửi mật khẩu nguyên văn trên dây. Không có TLS thì bất kỳ ai trên
// đường truyền cũng đọc được — và đây là cấu hình rất dễ vô tình để lọt ra
// production.
func TestSASL_PlainKhongTLSBiChan(t *testing.T) {
	_, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers: []string{"127.0.0.1:9092"},
		SASL: &kafka.SASLConfig{
			Mechanism: kafka.SASLPlain,
			Username:  "u",
			Password:  secret.Secret("p"),
		},
	})
	if err == nil {
		t.Fatal("SASL/PLAIN không TLS mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "nguyên văn") {
		t.Errorf("lỗi không giải thích nguy cơ: %v", err)
	}

	// SCRAM không TLS thì được: nó không gửi mật khẩu nguyên văn.
	p, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers: []string{"127.0.0.1:9092"},
		SASL: &kafka.SASLConfig{
			Mechanism: kafka.SASLScram512,
			Username:  "u",
			Password:  secret.Secret("p"),
		},
		Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("SCRAM không TLS bị chặn nhầm: %v", err)
	}
	p.Close()
}

func TestNewProducer_TLSSai(t *testing.T) {
	_, err := kafka.NewProducer(kafka.ProducerConfig{
		Brokers: []string{"127.0.0.1:9092"},
		TLS:     tlsx.Options{CertPEM: []byte("không phải PEM"), KeyPEM: []byte("cũng không")},
	})
	if err == nil {
		t.Fatal("cert rác mà không báo lỗi")
	}
}

func TestNewConsumer_ThieuCauHinh(t *testing.T) {
	tests := map[string]kafka.ConsumerConfig{
		"thiếu Group":  {Brokers: []string{"a:9092"}, Topics: []string{"t"}},
		"thiếu Topics": {Brokers: []string{"a:9092"}, Group: "g"},
		"topic rỗng":   {Brokers: []string{"a:9092"}, Group: "g", Topics: []string{""}},
		"thiếu Broker": {Group: "g", Topics: []string{"t"}},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := kafka.NewConsumer(cfg); err == nil {
				t.Fatal("cấu hình thiếu mà NewConsumer không báo lỗi")
			}
		})
	}
}

// Password kiểu secret.Secret nên nó không lọt ra khi in cả struct config.
func TestSASLConfig_KhongLoMatKhau(t *testing.T) {
	cfg := kafka.SASLConfig{
		Mechanism: kafka.SASLScram512,
		Username:  "u",
		Password:  secret.Secret("mat-khau-that"),
	}
	if got := strings.Contains(sprintf(cfg), "mat-khau-that"); got {
		t.Errorf("mật khẩu lọt ra khi in config: %s", sprintf(cfg))
	}
}
