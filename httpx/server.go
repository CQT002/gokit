package httpx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cqt002/gokit/core/tlsx"
)

// Giá trị mặc định của ServerConfig.
const (
	DefaultReadTimeout       = 15 * time.Second
	DefaultWriteTimeout      = 30 * time.Second
	DefaultIdleTimeout       = 60 * time.Second
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultShutdownTimeout   = 20 * time.Second
)

// ServerConfig cấu hình Server.
type ServerConfig struct {
	// Addr là địa chỉ lắng nghe, ví dụ ":8080". Rỗng thì dùng ":8080".
	Addr string

	// Handler là handler gốc. Bắt buộc.
	Handler http.Handler

	// ReadTimeout là thời gian tối đa để đọc toàn bộ request.
	ReadTimeout time.Duration

	// WriteTimeout là thời gian tối đa để ghi response.
	WriteTimeout time.Duration

	// IdleTimeout là thời gian giữ connection keep-alive khi rảnh.
	IdleTimeout time.Duration

	// ReadHeaderTimeout là thời gian tối đa để đọc xong header.
	//
	// Đây là hàng rào chống Slowloris: kẻ tấn công mở hàng nghìn connection rồi gửi
	// header chậm từng byte một, và không có timeout này thì mỗi connection chiếm
	// một goroutine vô thời hạn. Mặc định 5 giây, và **không** nên tắt.
	ReadHeaderTimeout time.Duration

	// TLS bật HTTPS khi có cert. Không khai thì chạy HTTP.
	TLS tlsx.Options

	// ShutdownTimeout là thời gian tối đa chờ các request đang chạy hoàn tất.
	ShutdownTimeout time.Duration

	// Logger dùng để ghi log vòng đời. nil thì dùng slog.Default().
	Logger *slog.Logger

	// BeforeShutdown chạy **trước** khi server ngừng nhận connection mới.
	//
	// Đây là chỗ gọi health.SetNotReady và chờ load balancer rút traffic. Không có
	// bước này thì những request đang trên đường tới sẽ nhận connection reset, vì
	// load balancer chưa biết pod đã đóng.
	BeforeShutdown func(context.Context)
}

// Server là http.Server có graceful shutdown.
type Server struct {
	cfg    ServerConfig
	log    *slog.Logger
	srv    *http.Server
	tlsCfg *tls.Config

	// listenerAddr là địa chỉ thật sau khi bind, cần khi cấu hình dùng cổng 0.
	//
	// Dùng atomic vì Run ghi nó từ goroutine của mình còn Addr đọc từ goroutine
	// khác — thường là code khởi động hoặc test đang chờ server bind xong.
	listenerAddr atomic.Pointer[string]
}

// NewServer dựng Server từ cấu hình.
//
// Trả lỗi nếu thiếu Handler hoặc cấu hình TLS sai — lỗi cấu hình phải lộ ra lúc
// khởi động, không phải ở request đầu tiên.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Handler == nil {
		return nil, errors.New("httpx: ServerConfig thiếu Handler")
	}

	s := &Server{cfg: cfg, log: cfg.Logger}
	if s.log == nil {
		s.log = slog.Default()
	}

	if s.cfg.Addr == "" {
		s.cfg.Addr = ":8080"
	}
	setIfZero(&s.cfg.ReadTimeout, DefaultReadTimeout)
	setIfZero(&s.cfg.WriteTimeout, DefaultWriteTimeout)
	setIfZero(&s.cfg.IdleTimeout, DefaultIdleTimeout)
	setIfZero(&s.cfg.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	setIfZero(&s.cfg.ShutdownTimeout, DefaultShutdownTimeout)

	if hasTLS(cfg.TLS) {
		tlsCfg, err := tlsx.ServerConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("httpx: cấu hình TLS: %w", err)
		}
		s.tlsCfg = tlsCfg
	}

	s.srv = &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.cfg.Handler,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout,
		TLSConfig:         s.tlsCfg,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	return s, nil
}

func setIfZero(d *time.Duration, def time.Duration) {
	if *d <= 0 {
		*d = def
	}
}

func hasTLS(o tlsx.Options) bool {
	return len(o.CertPEM) > 0 || o.CertFile != "" || o.CertB64 != ""
}

// Addr trả về địa chỉ server đang lắng nghe.
//
// Sau khi Run đã bind thì đây là địa chỉ thật, gồm cả cổng đã cấp khi cấu hình dùng
// cổng 0 — cần cho test.
func (s *Server) Addr() string {
	if addr := s.listenerAddr.Load(); addr != nil {
		return *addr
	}
	return s.cfg.Addr
}

// Run lắng nghe và phục vụ tới khi ctx bị cancel, rồi drain các request đang chạy.
//
// Trình tự khi ctx cancel:
//
//  1. Gọi BeforeShutdown (nơi đặt SetNotReady và chờ LB rút traffic);
//  2. Ngừng nhận connection mới;
//  3. Chờ các request đang chạy hoàn tất, tối đa ShutdownTimeout;
//  4. Hết thời gian thì đóng cứng.
//
// Trả nil khi shutdown xong bình thường. http.ErrServerClosed không được coi là lỗi
// vì đó là kết quả mong muốn của việc đóng có kiểm soát.
func (s *Server) Run(ctx context.Context) error {
	// ListenConfig thay vì net.Listen: việc bind cũng tôn trọng ctx, nên một ctx đã
	// bị cancel trước khi khởi động sẽ không để lại listener treo.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("httpx: lắng nghe %s: %w", s.cfg.Addr, err)
	}
	addr := ln.Addr().String()
	s.listenerAddr.Store(&addr)

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("server bắt đầu chạy",
			slog.String("addr", ln.Addr().String()),
			slog.Bool("tls", s.tlsCfg != nil))

		var serveErr error
		if s.tlsCfg != nil {
			serveErr = s.srv.ServeTLS(ln, "", "")
		} else {
			serveErr = s.srv.Serve(ln)
		}
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		errCh <- serveErr
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	if s.cfg.BeforeShutdown != nil {
		// Dùng context.WithoutCancel: ctx đã cancel rồi, mà bước này cần chạy được.
		beforeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
		s.cfg.BeforeShutdown(beforeCtx)
		cancel()
	}

	s.log.Info("server bắt đầu đóng", slog.Duration("timeout", s.cfg.ShutdownTimeout))

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		// Hết thời gian mà vẫn còn request: đóng cứng, và nói rõ trong log là đã có
		// request bị cắt giữa đường.
		s.log.Error("hết thời gian drain, đóng cứng", slog.Any("error", err))
		_ = s.srv.Close()
		return fmt.Errorf("httpx: shutdown: %w", err)
	}

	s.log.Info("server đã đóng")
	return <-errCh
}
