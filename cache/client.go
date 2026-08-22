package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/redis/go-redis/v9"

	"golang.org/x/sync/singleflight"
)

// Client là client Redis dùng chung của service.
//
// Nó cài đặt [KV], [Hash], [PubSub], [Stream], [Pipeline] và [Loader]. Chỗ nào
// cần đủ mọi thứ thì nhận *Client; chỗ nào chỉ đọc/ghi key thì nhận [KV] và
// test được bằng một mock năm dòng.
type Client struct {
	rdb redis.UniversalClient
	log *slog.Logger

	// flight gom các lần load trùng key. Đặt trên Client, không phải biến
	// package: hai Client trỏ tới hai Redis khác nhau mà dùng chung một nhóm
	// thì một key trùng tên ở hai hệ thống sẽ bị gộp thành một lần load.
	flight Flight
}

// New dựng Client từ cấu hình.
//
// Hàm này **không** kết nối: go-redis mở connection lười, ở lệnh đầu tiên. Gọi
// [Client.Ping] lúc khởi động nếu muốn sai cấu hình lộ ra ngay thay vì ở request
// đầu tiên.
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	tlsCfg, err := cfg.tlsConfig()
	if err != nil {
		return nil, err
	}

	poolSize := cfg.PoolSize
	if poolSize == 0 {
		poolSize = DefaultPoolSize
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	slow := cfg.SlowThreshold
	if slow <= 0 {
		slow = DefaultSlowThreshold
	}

	// NewUniversalClient chọn *redis.Client khi có một địa chỉ và
	// *redis.ClusterClient khi có nhiều — đúng ranh giới đã khai trong godoc
	// của Config.Addrs.
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:     cfg.Addrs,
		Username:  cfg.Username,
		Password:  cfg.Password.Reveal(),
		DB:        cfg.DB,
		PoolSize:  poolSize,
		TLSConfig: tlsCfg,
	})
	rdb.AddHook(&loggingHook{log: log, slow: slow})

	return &Client{rdb: rdb, log: log}, nil
}

// NewWithRedis bọc một redis.UniversalClient đã có.
//
// Dùng khi client Redis do chỗ khác dựng — test với server giả, hoặc app đã có
// sẵn client và chỉ muốn thêm phần tiện ích của package này.
func NewWithRedis(rdb redis.UniversalClient, log *slog.Logger) (*Client, error) {
	if rdb == nil {
		return nil, errors.New("cache: NewWithRedis cần một redis.UniversalClient")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{rdb: rdb, log: log}, nil
}

// Redis trả về client go-redis bên dưới.
//
// Đây là cửa thoát có chủ ý: package này chỉ bọc những thao tác dùng nhiều, và
// mọi thứ còn lại của Redis — Lua script, SETRANGE, GEO, module — vẫn tới được
// mà không cần package này thêm một method cho mỗi lệnh.
func (c *Client) Redis() redis.UniversalClient { return c.rdb }

// Flight trả về nhóm chống stampede của client. Xem [GetOrLoad].
func (c *Client) Flight() *Flight { return &c.flight }

// Logger trả về logger của client.
//
// Có để các package con và [GetOrLoad] ghi log về đúng chỗ mà app đã cấu hình,
// thay vì rơi về slog.Default().
func (c *Client) Logger() *slog.Logger { return c.log }

// Ping kiểm tra kết nối.
//
// Gọi nó lúc khởi động: sai địa chỉ hay sai mật khẩu lộ ra ngay thay vì thành
// lỗi 500 ở request đầu tiên.
func (c *Client) Ping(ctx context.Context) error {
	return wrap("ping", c.rdb.Ping(ctx).Err())
}

// Close đóng mọi connection.
func (c *Client) Close() error {
	if err := c.rdb.Close(); err != nil {
		return fmt.Errorf("cache: đóng client: %w", err)
	}
	return nil
}

// Flight gom các lần load trùng key trong cùng process.
//
// Zero value dùng được. Xem [GetOrLoad] để biết vì sao nó là một type riêng
// chứ không phải biến toàn cục.
type Flight struct {
	g singleflight.Group
}

// loggingHook ghi log lệnh lỗi và lệnh chậm.
//
// Cài redis.Hook chứ không bọc từng method: hook thấy **mọi** lệnh, kể cả lệnh
// đi qua Redis() và lệnh trong pipeline, nên không có đường nào lọt qua mà
// không được đo.
type loggingHook struct {
	log  *slog.Logger
	slow time.Duration
}

// DialHook cài đặt redis.Hook. Ghi log khi quay số thất bại.
func (h *loggingHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := next(ctx, network, addr)
		if err != nil {
			h.log.ErrorContext(ctx, "redis dial thất bại",
				slog.String("addr", addr), slog.String("error", err.Error()))
		}
		return conn, err
	}
}

// ProcessHook cài đặt redis.Hook cho lệnh đơn.
func (h *loggingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		h.observe(ctx, cmd.Name(), isHandshake(cmd.Name()), time.Since(start), err)
		return err
	}
}

// ProcessPipelineHook cài đặt redis.Hook cho pipeline.
func (h *loggingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)

		// Một lô toàn lệnh handshake là lô do go-redis tự gửi lúc mở
		// connection, không phải pipeline của app.
		allHandshake := true
		for _, cmd := range cmds {
			if !isHandshake(cmd.Name()) {
				allHandshake = false
				break
			}
		}

		h.observe(ctx, fmt.Sprintf("pipeline(%d)", len(cmds)), allHandshake, time.Since(start), err)
		return err
	}
}

// isHandshake cho biết lệnh có phải là lệnh go-redis tự gửi lúc mở connection.
//
// go-redis gửi CLIENT SETINFO (cần Redis 7.2) và CLIENT MAINT_NOTIFICATIONS
// (cần Redis Enterprise) khi mở mỗi connection, rồi **bỏ qua lỗi và chạy tiếp**
// nếu server không hỗ trợ. Trên Redis 6.x — vẫn còn rất nhiều — nghĩa là mỗi
// connection mới sinh ra một lỗi mà chẳng có gì sai. Ghi nó ở mức ERROR làm mọi
// alert theo error rate mất giá trị, nên nhóm này không được ghi log.
//
// Chỉ CLIENT nằm trong danh sách: AUTH hay SELECT thất bại là sự cố thật, và
// những lỗi đó phải hiện ra.
func isHandshake(name string) bool { return name == "client" }

// observe ghi một dòng log cho lệnh vừa chạy, nếu có gì đáng ghi.
//
// Chỉ ghi tên lệnh, **không** ghi đối số: đối số của Redis là key và giá trị,
// tức là dữ liệu người dùng. Cùng lý do như Config.LogSQLParams ở module db.
func (h *loggingHook) observe(ctx context.Context, name string, handshake bool, took time.Duration, err error) {
	switch {
	case err != nil && !errors.Is(err, redis.Nil):
		if handshake {
			return
		}
		h.log.ErrorContext(ctx, "redis lỗi",
			slog.String("cmd", name),
			slog.Float64("elapsed_ms", float64(took.Nanoseconds())/1e6),
			slog.String("error", err.Error()))

	case took > h.slow:
		h.log.WarnContext(ctx, "redis chậm",
			slog.String("cmd", name),
			slog.Float64("elapsed_ms", float64(took.Nanoseconds())/1e6),
			slog.Bool("slow", true),
			slog.Duration("threshold", h.slow))
	}
}
