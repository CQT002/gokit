package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"

	"github.com/cqt002/gokit/core/retry"
	"github.com/cqt002/gokit/core/tlsx"
	"github.com/cqt002/gokit/core/tracectx"
)

// Handler xử lý một message.
//
// Hợp đồng của giá trị trả về quyết định toàn bộ hành vi của consumer:
//
//   - nil nghĩa là **đã xử lý xong**, kể cả khi kết luận là message không dùng
//     được. Offset được commit và consumer đi tiếp.
//   - error nghĩa là **muốn thử lại**. Consumer thử lại theo
//     [ConsumerConfig.RetryBackoff]; hết lượt thì gửi vào DLQ, hoặc dừng hẳn nếu
//     không khai DLQ.
//
// Nghĩa là handler tự quyết định đâu là lỗi vĩnh viễn. Một message sai định dạng
// sẽ không bao giờ đúng lên được, nên trả error cho nó chỉ tốn ba lần thử rồi
// vào DLQ; nếu chỗ đó đằng nào cũng bỏ thì trả nil kèm một dòng log là xong.
//
// ctx đã mang SpanContext lấy từ header của message, nên log trong handler tự có
// trace ID nối với producer.
//
// Panic trong handler được bắt lại và biến thành error, nên một message hỏng
// không làm chết cả service.
type Handler func(ctx context.Context, msg Message) error

// Giá trị mặc định của ConsumerConfig.
const (
	// DefaultMaxPollRecords là số message lấy về mỗi vòng lặp.
	//
	// Con số này cũng là trần cho thời gian một lần rebalance bị chặn: consumer
	// xử lý xong cả lô rồi mới cho phép rebalance. Lô càng lớn thì thông lượng
	// càng tốt nhưng rebalance càng lâu.
	DefaultMaxPollRecords = 500

	// DefaultConcurrency là số partition được xử lý song song.
	DefaultConcurrency = 1

	// maxDLQErrorLen là độ dài tối đa của thông báo lỗi ghi vào header DLQ.
	//
	// Có trần vì thông báo lỗi do handler sinh và có thể rất dài — một lỗi
	// validate liệt kê trăm field sẽ làm bản ghi DLQ phình lên, mà phần đáng đọc
	// nằm ở đầu.
	maxDLQErrorLen = 1024
)

// Tên header mà consumer gắn vào bản ghi khi đẩy vào DLQ.
//
// Có tiền tố chung để lọc nhanh, và đủ thông tin để tìm lại bản ghi gốc: biết
// topic, partition và offset là đủ để đọc đúng message đó bằng kafka-console-consumer.
const (
	HeaderDLQError     = "dlq-error"
	HeaderDLQTopic     = "dlq-origin-topic"
	HeaderDLQPartition = "dlq-origin-partition"
	HeaderDLQOffset    = "dlq-origin-offset"
	HeaderDLQGroup     = "dlq-origin-group"
	HeaderDLQTime      = "dlq-failed-at"
)

// ConsumerConfig cấu hình Consumer.
type ConsumerConfig struct {
	// Brokers là danh sách "host:port" để quay số lần đầu. Bắt buộc.
	Brokers []string `yaml:"brokers" env:"KAFKA_BROKERS"`

	// Group là consumer group. Bắt buộc.
	//
	// Kafka chia partition cho các thành viên trong cùng group, nên đây là thứ
	// quyết định "mỗi message được xử lý một lần bởi một instance". Hai service
	// khác nhau muốn cùng đọc một topic thì phải dùng hai group khác nhau.
	Group string `yaml:"group" env:"KAFKA_GROUP"`

	// Topics là danh sách topic cần đọc. Bắt buộc.
	Topics []string `yaml:"topics" env:"KAFKA_TOPICS"`

	// ClientID hiện trong log và metric của broker. Nên đặt bằng tên service.
	ClientID string `yaml:"client_id" env:"KAFKA_CLIENT_ID"`

	// SASL là thông tin xác thực. nil nghĩa là không xác thực.
	SASL *SASLConfig `yaml:"sasl"`

	// TLS bật kết nối mã hoá khi có khai cert hoặc CA.
	TLS tlsx.Options `yaml:"tls"`

	// FromOldest quyết định đọc từ đâu khi group **chưa có offset đã lưu**.
	//
	// true là đọc từ đầu topic, false là chỉ đọc message mới. Chỉ có tác dụng ở
	// lần chạy đầu của một group mới: sau đó offset đã lưu luôn thắng.
	FromOldest bool `yaml:"from_oldest" env:"KAFKA_FROM_OLDEST"`

	// Concurrency là số **partition** được xử lý song song. <= 0 → 1.
	//
	// Song song theo partition, tuần tự trong một partition. Đây là mức song
	// song duy nhất giữ được đảm bảo thứ tự của Kafka: một worker pool bốc
	// message từ chung một hàng đợi sẽ xử lý hai sự kiện của cùng một đơn hàng
	// sai thứ tự, và lỗi đó chỉ hiện ra dưới tải.
	//
	// Đặt lớn hơn 1 thì handler phải an toàn khi gọi từ nhiều goroutine.
	Concurrency int `yaml:"concurrency" env:"KAFKA_CONCURRENCY"`

	// MaxPollRecords là số message lấy về mỗi vòng. <= 0 → DefaultMaxPollRecords.
	MaxPollRecords int `yaml:"max_poll_records" env:"KAFKA_MAX_POLL_RECORDS"`

	// MaxRetries là số lần **thử lại** handler, không tính lần đầu.
	//
	//	> 0  → tổng cộng MaxRetries + 1 lần gọi
	//	= 0  → dùng RetryBackoff.MaxAttempts (giá trị zero của nó là 3 lần gọi)
	//	< 0  → không thử lại
	MaxRetries int `yaml:"max_retries" env:"KAFKA_MAX_RETRIES"`

	// RetryBackoff là cách chờ giữa các lần thử. Giá trị zero dùng được.
	RetryBackoff retry.Policy `yaml:"-"`

	// DLQTopic nhận message đã hết lượt thử lại. Rỗng nghĩa là không có DLQ.
	//
	// **Không khai DLQTopic thì một message hỏng làm Run trả lỗi và service
	// dừng.** Đó là chủ ý: hai lựa chọn còn lại đều tệ hơn — bỏ qua trong im
	// lặng là mất dữ liệu, còn thử lại mãi là chặn cả partition mà không ai
	// biết. Muốn "bỏ qua" thì để handler trả nil, ở đó mới có đủ ngữ cảnh để
	// quyết định.
	//
	// Topic này phải tồn tại sẵn (hoặc broker cho phép tự tạo topic).
	DLQTopic string `yaml:"dlq_topic" env:"KAFKA_DLQ_TOPIC"`

	// Logger ghi log vòng đời và lỗi xử lý. nil thì dùng slog.Default().
	Logger *slog.Logger `yaml:"-"`

	// Metrics là registry để đăng ký metric. nil nghĩa là không đo.
	Metrics *prometheus.Registry `yaml:"-"`

	// ClientOpts là các tuỳ chọn franz-go thô, thêm vào sau cùng.
	ClientOpts []kgo.Opt `yaml:"-"`
}

// Consumer đọc message từ Kafka và đưa cho handler.
type Consumer struct {
	cl      *kgo.Client
	log     *slog.Logger
	metrics *consumerMetrics

	group       string
	dlqTopic    string
	concurrency int
	maxPoll     int
	policy      retry.Policy

	// running chặn việc gọi Run hai lần trên cùng một Consumer: hai vòng lặp
	// cùng poll một client sẽ chia nhau message theo cách không ai kiểm soát
	// được, và commit của bên này ghi đè tiến độ của bên kia.
	running atomic.Bool
}

// NewConsumer dựng Consumer từ cấu hình.
//
// Chưa kết nối và chưa đọc gì: việc đó bắt đầu ở [Consumer.Run].
func NewConsumer(cfg ConsumerConfig) (*Consumer, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	if cfg.Group == "" {
		return nil, errors.New("kafka: ConsumerConfig thiếu Group")
	}
	if len(cfg.Topics) == 0 {
		return nil, errors.New("kafka: ConsumerConfig thiếu Topics")
	}
	for _, t := range cfg.Topics {
		if t == "" {
			return nil, errors.New("kafka: Topics có phần tử rỗng")
		}
	}

	opts, err := baseOpts(cfg.Brokers, cfg.ClientID, cfg.SASL, cfg.TLS, log)
	if err != nil {
		return nil, err
	}

	offset := kgo.NewOffset().AtEnd()
	if cfg.FromOldest {
		offset = kgo.NewOffset().AtStart()
	}

	opts = append(opts,
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.ConsumeResetOffset(offset),

		// Tự commit, không để franz-go commit theo chu kỳ: commit tự động sẽ ghi
		// nhận cả những message chưa xử lý xong, và lúc restart chúng biến mất.
		kgo.DisableAutoCommit(),

		// Chặn rebalance trong lúc đang xử lý một lô. Không có nó thì partition
		// có thể bị thu hồi giữa chừng, và lần commit sau đó ghi offset cho
		// partition mà instance này không còn sở hữu — instance mới sẽ nhảy qua
		// những message chưa ai xử lý.
		kgo.BlockRebalanceOnPoll(),
	)

	if hook := clientMetricsOpt(cfg.Metrics, "consumer"); hook != nil {
		opts = append(opts, hook)
	}
	opts = append(opts, cfg.ClientOpts...)

	metrics, err := newConsumerMetrics(cfg.Metrics, cfg.Group)
	if err != nil {
		return nil, err
	}

	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: dựng consumer: %w", err)
	}

	return &Consumer{
		cl:          cl,
		log:         log.With(slog.String("group", cfg.Group)),
		metrics:     metrics,
		group:       cfg.Group,
		dlqTopic:    cfg.DLQTopic,
		concurrency: max(cfg.Concurrency, DefaultConcurrency),
		maxPoll:     maxPollOr(cfg.MaxPollRecords),
		policy:      retryPolicy(cfg.MaxRetries, cfg.RetryBackoff),
	}, nil
}

// maxPollOr trả về số message mỗi vòng, hoặc mặc định.
func maxPollOr(n int) int {
	if n <= 0 {
		return DefaultMaxPollRecords
	}
	return n
}

// retryPolicy gộp MaxRetries và RetryBackoff thành một Policy.
//
// Hai field cùng nói về số lần thử là thứ dễ mâu thuẫn, nên quy tắc phải viết ra
// một chỗ: MaxRetries thắng khi khác 0, còn lại giữ nguyên Policy.
func retryPolicy(maxRetries int, p retry.Policy) retry.Policy {
	switch {
	case maxRetries > 0:
		p.MaxAttempts = maxRetries + 1
	case maxRetries < 0:
		p.MaxAttempts = 1
	}
	return p
}

// Run đọc message và gọi h cho tới khi ctx bị cancel hoặc gặp lỗi không xử lý được.
//
// Trả nil khi dừng vì ctx bị cancel hoặc vì [Consumer.Close], khớp với
// httpx.App.Run nên ghép thẳng vào App được:
//
//	app.Add("kafka", func(ctx context.Context) error { return consumer.Run(ctx, handle) },
//	    func(context.Context) error { consumer.Close(); return nil })
//
// Đảm bảo: **at-least-once**. Offset chỉ được commit sau khi mọi message trong
// lô đã xử lý xong, nên một lần restart giữa chừng làm cả lô được gửi lại.
// Handler phải chịu được việc nhận lại cùng một message — thường là kiểm tra một
// khoá idempotency trước khi ghi.
//
// Gọi Run hai lần trên cùng một Consumer là lỗi.
func (c *Consumer) Run(ctx context.Context, h Handler) error {
	if h == nil {
		return errors.New("kafka: Run cần handler")
	}
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("kafka: Run đã đang chạy trên Consumer này")
	}
	defer c.running.Store(false)

	c.log.InfoContext(ctx, "consumer bắt đầu chạy",
		slog.Int("concurrency", c.concurrency),
		slog.String("dlq_topic", c.dlqTopic))

	for {
		fetches := c.cl.PollRecords(ctx, c.maxPoll)

		// Close được gọi từ goroutine khác.
		if fetches.IsClientClosed() {
			c.log.InfoContext(ctx, "consumer dừng vì client đã đóng")
			return nil
		}

		err := c.consumeBatch(ctx, fetches, h)

		// Gỡ chặn rebalance sau **mọi** đường ra của một lần poll, kể cả khi lô
		// vừa rồi lỗi hoặc ctx đã cancel. Cặp với BlockRebalanceOnPoll ở trên.
		//
		// Bỏ sót một lần gọi ở đây là treo hẳn: Close phải rời group, rời group
		// là một lần rebalance, và rebalance đang bị chặn bởi chính lần poll mà
		// ta thoát ra sớm. Triệu chứng là Close không bao giờ trả về — chỉ hiện
		// ra lúc shutdown, tức là chỗ ít ai test nhất.
		c.cl.AllowRebalance()

		// ctx cancel là dừng sạch, không phải lỗi — kể cả khi lô đang dở dang.
		// Phần chưa commit sẽ được gửi lại, đúng như đảm bảo at-least-once.
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// consumeBatch xử lý một lô đã lấy về: ghi log lỗi tầng fetch, chạy handler,
// rồi commit.
func (c *Consumer) consumeBatch(ctx context.Context, fetches kgo.Fetches, h Handler) error {
	if err := c.logFetchErrors(ctx, fetches); err != nil {
		return err
	}
	return c.processFetches(ctx, fetches, h)
}

// logFetchErrors ghi log các lỗi ở tầng lấy dữ liệu.
//
// Không dừng consumer: phần lớn lỗi ở đây (mất leader, metadata cũ) được
// franz-go tự thử lại, và dừng service vì một nhịp mạng là phản ứng quá tay.
// Lỗi do context thì trả ra ngoài để vòng lặp kết thúc.
func (c *Consumer) logFetchErrors(ctx context.Context, fetches kgo.Fetches) error {
	for _, fe := range fetches.Errors() {
		if errors.Is(fe.Err, context.Canceled) || errors.Is(fe.Err, context.DeadlineExceeded) {
			return fe.Err
		}
		c.log.ErrorContext(ctx, "lỗi khi lấy message",
			slog.String("topic", fe.Topic),
			slog.Int("partition", int(fe.Partition)),
			slog.String("error", fe.Err.Error()))
	}
	return nil
}

// processFetches xử lý một lô message rồi commit.
//
// Song song theo partition, tuần tự trong partition — xem
// [ConsumerConfig.Concurrency].
func (c *Consumer) processFetches(ctx context.Context, fetches kgo.Fetches, h Handler) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(c.concurrency)

	fetches.EachPartition(func(p kgo.FetchTopicPartition) {
		recs := p.Records
		if len(recs) == 0 {
			return
		}
		g.Go(func() error { return c.processPartition(gctx, recs, h) })
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// Commit một lần cho cả lô, và chỉ khi mọi partition đã xong. Commit từng
	// phần rồi lỗi ở phần sau vẫn đúng về mặt at-least-once, nhưng gộp lại thì
	// số lần round-trip tới coordinator ít hơn hẳn.
	//
	// Dùng ctx gốc: nếu ctx đã cancel thì lần commit này trượt, và cả lô sẽ
	// được gửi lại — đúng như đảm bảo at-least-once đã ghi trong godoc.
	if err := c.cl.CommitRecords(ctx, fetches.Records()...); err != nil {
		return fmt.Errorf("kafka: commit offset: %w", err)
	}
	return nil
}

// processPartition xử lý tuần tự các message của một partition.
func (c *Consumer) processPartition(ctx context.Context, recs []*kgo.Record, h Handler) error {
	for _, rec := range recs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.handleOne(ctx, fromRecord(rec), h); err != nil {
			return err
		}
	}
	return nil
}

// handleOne gọi handler cho một message, thử lại và đẩy vào DLQ khi cần.
func (c *Consumer) handleOne(ctx context.Context, msg Message, h Handler) error {
	msgCtx := extractTrace(ctx, msg)
	log := c.log.With(
		slog.String("topic", msg.Topic),
		slog.Int("partition", int(msg.Partition)),
		slog.Int64("offset", msg.Offset),
		slog.String("trace_id", tracectx.TraceIDFrom(msgCtx)))

	start := time.Now()
	attempts := 0

	err := retry.Do(msgCtx, c.policy, func(ctx context.Context) error {
		attempts++
		if attempts > 1 {
			log.WarnContext(ctx, "thử lại message", slog.Int("attempt", attempts))
		}
		return callHandler(ctx, h, msg)
	})

	took := time.Since(start).Seconds()
	retries := attempts - 1

	if err == nil {
		c.metrics.observe(msg.Topic, resultOK, took, retries)
		return nil
	}

	// ctx bị cancel giữa chừng không phải lỗi của message: nó sẽ được gửi lại.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if c.dlqTopic == "" {
		c.metrics.observe(msg.Topic, resultError, took, retries)
		return fmt.Errorf("kafka: xử lý message %s/%d/%d thất bại sau %d lần thử (chưa khai DLQTopic): %w",
			msg.Topic, msg.Partition, msg.Offset, attempts, err)
	}

	if dlqErr := c.toDLQ(msgCtx, msg, err); dlqErr != nil {
		// Không commit khi không đẩy được vào DLQ: commit lúc này là mất hẳn
		// message. Dừng lại để nó được gửi lại ở lần chạy sau.
		c.metrics.observe(msg.Topic, resultError, took, retries)
		return fmt.Errorf("kafka: đẩy message %s/%d/%d vào DLQ %q thất bại: %w",
			msg.Topic, msg.Partition, msg.Offset, c.dlqTopic, dlqErr)
	}

	c.metrics.observe(msg.Topic, resultDLQ, took, retries)
	log.ErrorContext(msgCtx, "message vào DLQ sau khi hết lượt thử lại",
		slog.Int("attempts", attempts),
		slog.String("dlq_topic", c.dlqTopic),
		slog.String("error", err.Error()))
	return nil
}

// callHandler gọi handler và biến panic thành error.
//
// Panic trong handler mà không bắt thì cả process chết, và mọi partition khác
// mà instance này đang giữ ngừng theo. Biến nó thành error thì một message hỏng
// chỉ đi vào DLQ như mọi message hỏng khác.
func callHandler(ctx context.Context, h Handler, msg Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("kafka: handler panic: %v", r)
		}
	}()
	return h(ctx, msg)
}

// toDLQ gửi message vào DLQ kèm thông tin nguồn gốc và nguyên nhân.
//
// Gửi **đồng bộ**: phải chắc chắn DLQ đã nhận trước khi offset được commit, nếu
// không thì một lỗi ở đây làm message biến mất hoàn toàn.
func (c *Consumer) toDLQ(ctx context.Context, msg Message, cause error) error {
	headers := make(map[string]string, len(msg.Headers)+6)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headers[HeaderDLQError] = truncate(cause.Error(), maxDLQErrorLen)
	headers[HeaderDLQTopic] = msg.Topic
	headers[HeaderDLQPartition] = strconv.Itoa(int(msg.Partition))
	headers[HeaderDLQOffset] = strconv.FormatInt(msg.Offset, 10)
	headers[HeaderDLQGroup] = c.group
	headers[HeaderDLQTime] = time.Now().UTC().Format(time.RFC3339Nano)

	// Giữ nguyên Key: bản ghi DLQ vào cùng partition với bản gốc, nên thứ tự
	// giữa các message hỏng của cùng một thực thể vẫn đúng khi phát lại.
	rec := Message{
		Topic:   c.dlqTopic,
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	}.toRecord()

	return c.cl.ProduceSync(ctx, rec).FirstErr()
}

// truncate cắt chuỗi về tối đa n byte, cắt theo ranh giới byte và thêm dấu.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Ping kiểm tra kết nối tới ít nhất một broker.
func (c *Consumer) Ping(ctx context.Context) error {
	if err := c.cl.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: không kết nối được tới broker: %w", err)
	}
	return nil
}

// Close rời group và đóng mọi kết nối.
//
// Làm cho [Consumer.Run] đang chạy trả về nil. Rời group tường minh thay vì để
// hết session timeout: nhờ đó những instance còn lại nhận lại partition ngay,
// thay vì chờ vài chục giây không ai xử lý.
func (c *Consumer) Close() { c.cl.Close() }

// Client trả về client franz-go bên dưới.
func (c *Consumer) Client() *kgo.Client { return c.cl }
