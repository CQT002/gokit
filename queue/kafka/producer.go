package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/cqt002/gokit/core/tlsx"
)

// ProducerConfig cấu hình Producer.
type ProducerConfig struct {
	// Brokers là danh sách "host:port" để quay số lần đầu. Bắt buộc.
	//
	// Không cần liệt kê hết cluster: client lấy danh sách đầy đủ từ metadata.
	// Nhưng nên có ít nhất hai, để một broker chết không làm service không khởi
	// động được.
	Brokers []string `yaml:"brokers" env:"KAFKA_BROKERS"`

	// ClientID hiện trong log và metric của broker. Nên đặt bằng tên service.
	ClientID string `yaml:"client_id" env:"KAFKA_CLIENT_ID"`

	// SASL là thông tin xác thực. nil nghĩa là không xác thực.
	SASL *SASLConfig `yaml:"sasl"`

	// TLS bật kết nối mã hoá khi có khai cert hoặc CA.
	TLS tlsx.Options `yaml:"tls"`

	// Compression là thuật toán nén: none, gzip, snappy, lz4, zstd.
	// Rỗng thì dùng mặc định của franz-go.
	Compression string `yaml:"compression" env:"KAFKA_COMPRESSION"`

	// RequiredAcks là mức xác nhận. Rỗng → AcksAll.
	RequiredAcks Acks `yaml:"required_acks" env:"KAFKA_REQUIRED_ACKS"`

	// MaxRetries là số lần thử lại một bản ghi khi gặp lỗi tạm thời.
	// <= 0 thì dùng mặc định của franz-go.
	MaxRetries int `yaml:"max_retries" env:"KAFKA_PRODUCER_MAX_RETRIES"`

	// Async chuyển Send sang chế độ không chờ.
	//
	// Đọc kỹ godoc của [Producer.Send] trước khi bật: ở chế độ này Send trả nil
	// ngay cả khi việc gửi sau đó thất bại.
	Async bool `yaml:"async" env:"KAFKA_PRODUCER_ASYNC"`

	// PropagateTrace tự chèn header `traceparent` vào mỗi message.
	PropagateTrace bool `yaml:"propagate_trace" env:"KAFKA_PROPAGATE_TRACE"`

	// Logger ghi log vòng đời client và lỗi gửi. nil thì dùng slog.Default().
	Logger *slog.Logger `yaml:"-"`

	// Metrics là registry để đăng ký metric. nil nghĩa là không đo.
	Metrics *prometheus.Registry `yaml:"-"`

	// ClientOpts là các tuỳ chọn franz-go thô, thêm vào sau cùng.
	//
	// Cửa thoát có chủ ý: franz-go có hàng trăm tuỳ chọn, và bọc lại từng cái
	// sẽ biến config này thành một bản sao kém hơn của nó. Tuỳ chọn ở đây ghi
	// đè phần package này đã đặt.
	ClientOpts []kgo.Opt `yaml:"-"`
}

// Producer gửi message lên Kafka.
//
// An toàn khi dùng từ nhiều goroutine.
type Producer struct {
	cl      *kgo.Client
	log     *slog.Logger
	metrics *producerMetrics

	async          bool
	propagateTrace bool
}

// NewProducer dựng Producer từ cấu hình.
//
// Hàm này **không** kết nối: franz-go quay số lười, ở lần gửi đầu tiên. Sai địa
// chỉ broker lộ ra ở lần Send đầu chứ không phải lúc khởi động. Gọi
// [Producer.Ping] nếu muốn biết ngay.
func NewProducer(cfg ProducerConfig) (*Producer, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	opts, err := baseOpts(cfg.Brokers, cfg.ClientID, cfg.SASL, cfg.TLS, log)
	if err != nil {
		return nil, err
	}

	acks, err := cfg.RequiredAcks.kgoAcks()
	if err != nil {
		return nil, err
	}
	opts = append(opts, kgo.RequiredAcks(acks))

	// franz-go bật ghi idempotent mặc định, và cơ chế đó **đòi** acks=all. Hạ
	// acks mà quên tắt idempotent thì client trả lỗi cấu hình lúc khởi tạo —
	// một lỗi rất khó hiểu nếu không biết ràng buộc này.
	if cfg.RequiredAcks != AcksAll && cfg.RequiredAcks != "" {
		opts = append(opts, kgo.DisableIdempotentWrite())
	}

	comp, err := compressionOpt(cfg.Compression)
	if err != nil {
		return nil, err
	}
	if comp != nil {
		opts = append(opts, comp)
	}

	if cfg.MaxRetries > 0 {
		opts = append(opts, kgo.RecordRetries(cfg.MaxRetries))
	}
	if hook := clientMetricsOpt(cfg.Metrics, "producer"); hook != nil {
		opts = append(opts, hook)
	}
	opts = append(opts, cfg.ClientOpts...)

	metrics, err := newProducerMetrics(cfg.Metrics)
	if err != nil {
		return nil, err
	}

	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: dựng producer: %w", err)
	}

	return &Producer{
		cl:             cl,
		log:            log,
		metrics:        metrics,
		async:          cfg.Async,
		propagateTrace: cfg.PropagateTrace,
	}, nil
}

// Send gửi các message.
//
// Chế độ đồng bộ (mặc định): chờ broker xác nhận và trả về lỗi đầu tiên. Message
// nào lỗi thì lỗi đó bọc cả topic của nó.
//
// Chế độ bất đồng bộ ([ProducerConfig.Async]): trả về nil ngay sau khi xếp
// message vào bộ đệm, **kể cả khi việc gửi sau đó thất bại**. Lỗi lúc đó chỉ
// xuất hiện trong log và metric. Chỉ dùng cho dữ liệu chấp nhận được khi mất —
// và nhớ gọi [Producer.Flush] trước khi service dừng, nếu không message còn
// trong bộ đệm sẽ mất.
//
// Không có message nào thì Send trả nil và không làm gì.
func (p *Producer) Send(ctx context.Context, msgs ...Message) error {
	if len(msgs) == 0 {
		return nil
	}

	recs := make([]*kgo.Record, len(msgs))
	for i, m := range msgs {
		if m.Topic == "" {
			return fmt.Errorf("kafka: message thứ %d không có Topic", i)
		}
		if p.propagateTrace {
			m = injectTrace(ctx, m)
		}
		recs[i] = m.toRecord()
	}

	if p.async {
		for _, rec := range recs {
			p.cl.Produce(ctx, rec, p.onProduced)
		}
		return nil
	}

	results := p.cl.ProduceSync(ctx, recs...)
	var errs []error
	for _, r := range results {
		topic := ""
		if r.Record != nil {
			topic = r.Record.Topic
		}
		if r.Err != nil {
			p.metrics.observe(topic, resultError)
			errs = append(errs, fmt.Errorf("kafka: gửi vào topic %q: %w", topic, r.Err))
			continue
		}
		p.metrics.observe(topic, resultOK)
	}
	return errors.Join(errs...)
}

// onProduced là callback của chế độ bất đồng bộ.
//
// Đây là **chỗ duy nhất** lỗi gửi ở chế độ async hiện ra, nên nó phải ghi log:
// không có dòng log này thì message mất đi trong im lặng hoàn toàn.
func (p *Producer) onProduced(rec *kgo.Record, err error) {
	topic := ""
	if rec != nil {
		topic = rec.Topic
	}

	if err != nil {
		p.metrics.observe(topic, resultError)
		p.log.Error("gửi message Kafka thất bại",
			slog.String("topic", topic),
			slog.String("error", err.Error()))
		return
	}
	p.metrics.observe(topic, resultOK)
}

// SendJSON mã hoá v thành JSON rồi gửi.
//
// Tự đặt header `content-type: application/json`, để phía nhận biết cách giải mã
// mà không phải thoả thuận ngầm.
func (p *Producer) SendJSON(ctx context.Context, topic, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("kafka: mã hoá JSON cho topic %q: %w", topic, err)
	}
	return p.Send(ctx, Message{
		Topic:   topic,
		Key:     key,
		Value:   b,
		Headers: map[string]string{"content-type": "application/json"},
	})
}

// Flush chờ mọi message đang trong bộ đệm được gửi xong.
//
// Bắt buộc gọi trước khi dừng service ở chế độ bất đồng bộ. [Producer.Close]
// cũng flush, nên chỉ cần Flush riêng khi muốn một điểm chốt giữa chừng — ví dụ
// sau khi ghi xong một lô dữ liệu và trước khi cập nhật trạng thái đã xử lý.
func (p *Producer) Flush(ctx context.Context) error {
	if err := p.cl.Flush(ctx); err != nil {
		return fmt.Errorf("kafka: flush producer: %w", err)
	}
	return nil
}

// Ping kiểm tra kết nối tới ít nhất một broker.
//
// Gọi lúc khởi động nếu muốn sai địa chỉ hay sai thông tin xác thực lộ ra ngay,
// thay vì ở lần gửi đầu tiên — cùng vai trò với lần ping trong db.Open.
func (p *Producer) Ping(ctx context.Context) error {
	if err := p.cl.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: không kết nối được tới broker: %w", err)
	}
	return nil
}

// Close flush bộ đệm rồi đóng mọi kết nối.
//
// Không trả lỗi vì franz-go không trả: Close chờ flush xong rồi mới đóng. Cần
// biết flush có thành công không thì gọi [Producer.Flush] trước.
func (p *Producer) Close() { p.cl.Close() }

// Client trả về client franz-go bên dưới.
//
// Cửa thoát cho những thứ package này không bọc: transaction, produce có
// partitioner riêng, kadm. Dùng nó không làm hỏng gì — chỉ là phần đó tự lo lấy
// trace và metric.
func (p *Producer) Client() *kgo.Client { return p.cl }
