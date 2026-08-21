package httpx_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/tlsx"
	"github.com/cqt002/gokit/httpx"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustServer(t *testing.T, cfg httpx.ServerConfig) *httpx.Server {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	s, err := httpx.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func TestNewServer_ThieuHandler(t *testing.T) {
	if _, err := httpx.NewServer(httpx.ServerConfig{}); err == nil {
		t.Error("thiếu Handler không báo lỗi")
	}
}

func TestNewServer_TLSSai(t *testing.T) {
	_, err := httpx.NewServer(httpx.ServerConfig{
		Handler: http.NotFoundHandler(),
		TLS:     tlsx.Options{CertPEM: []byte("khong phai PEM"), KeyPEM: []byte("cung khong phai")},
	})
	if err == nil {
		t.Error("cấu hình TLS sai không báo lỗi lúc khởi động")
	}
}

func TestServer_PhucVuRoiDong(t *testing.T) {
	srv := mustServer(t, httpx.ServerConfig{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("xin chào"))
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	addr := waitForAddr(t, srv)
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "xin chào" {
		t.Errorf("body = %q", body)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run = %v, muốn nil khi đóng có kiểm soát", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run không thoát sau khi cancel")
	}
}

// Graceful shutdown: request đang chạy phải được hoàn tất, không bị cắt giữa đường.
func TestServer_DrainRequestDangChay(t *testing.T) {
	started := make(chan struct{})
	srv := mustServer(t, httpx.ServerConfig{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write([]byte("hoàn tất"))
		}),
		ShutdownTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	addr := waitForAddr(t, srv)

	type result struct {
		body string
		err  error
	}
	respCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			respCh <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		respCh <- result{body: string(b), err: err}
	}()

	<-started
	cancel() // shutdown giữa lúc request đang chạy

	select {
	case r := <-respCh:
		if r.err != nil {
			t.Fatalf("request đang chạy bị cắt: %v", r.err)
		}
		if r.body != "hoàn tất" {
			t.Errorf("body = %q, request đang chạy phải được hoàn tất", r.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request không hoàn tất")
	}

	<-errCh
}

// BeforeShutdown là chỗ gọi health.SetNotReady và chờ LB rút traffic; nó phải chạy
// TRƯỚC khi server ngừng nhận connection.
func TestServer_BeforeShutdown(t *testing.T) {
	var called atomic.Bool
	srv := mustServer(t, httpx.ServerConfig{
		Handler:        http.NotFoundHandler(),
		BeforeShutdown: func(context.Context) { called.Store(true) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	waitForAddr(t, srv)
	cancel()
	<-errCh

	if !called.Load() {
		t.Error("BeforeShutdown không được gọi")
	}
}

func TestServer_PortDaBiChiem(t *testing.T) {
	first := mustServer(t, httpx.ServerConfig{Handler: http.NotFoundHandler()})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = first.Run(ctx) }()
	defer cancel()

	addr := waitForAddr(t, first)

	second := mustServer(t, httpx.ServerConfig{Handler: http.NotFoundHandler(), Addr: addr})
	if err := second.Run(context.Background()); err == nil {
		t.Error("bind vào cổng đã bị chiếm mà không báo lỗi")
	}
}

// ---------- App ----------

func TestApp_KhongCoThanhPhan(t *testing.T) {
	if err := httpx.NewApp(quietLogger()).Run(context.Background()); err == nil {
		t.Error("App rỗng không báo lỗi")
	}
}

func TestApp_DungTheoThuTuNguoc(t *testing.T) {
	var order []string
	app := httpx.NewApp(quietLogger())

	for _, name := range []string{"db", "http", "consumer"} {
		app.Add(name,
			func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			},
			func(context.Context) error {
				order = append(order, name)
				return nil
			})
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run không thoát")
	}

	// Thành phần thêm sau phụ thuộc thành phần thêm trước, nên phải dừng ngược lại.
	want := "consumer,http,db"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("thứ tự dừng = %q, muốn %q", got, want)
	}
}

// Một thành phần lỗi phải làm cả app dừng: service còn HTTP sống nhưng consumer đã
// chết sẽ vẫn qua health check và vẫn nhận traffic, trong khi nửa chức năng đã ngừng.
func TestApp_MotThanhPhanLoiThiDungHet(t *testing.T) {
	loi := errors.New("consumer chết")

	var httpStopped atomic.Bool
	app := httpx.NewApp(quietLogger())
	app.Add("http",
		func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
		func(context.Context) error {
			httpStopped.Store(true)
			return nil
		})
	app.Add("consumer", func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		return loi
	}, nil)

	err := app.Run(context.Background())
	if !errors.Is(err, loi) {
		t.Errorf("Run = %v, muốn bọc lỗi của consumer", err)
	}
	if !httpStopped.Load() {
		t.Error("thành phần khác không được dừng")
	}
}

func TestApp_ThanhPhanPanic(t *testing.T) {
	app := httpx.NewApp(quietLogger())
	app.Add("hong", func(context.Context) error { panic("bùm") }, nil)
	app.Add("khac", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}, nil)

	err := app.Run(context.Background())
	if err == nil {
		t.Fatal("panic của thành phần không thành lỗi")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("lỗi = %v, phải nói rõ là panic", err)
	}
}

// Thành phần không tôn trọng ctx không được giữ process sống mãi.
func TestApp_ThanhPhanKhongChiuDung(t *testing.T) {
	app := httpx.NewApp(quietLogger())
	app.ShutdownTimeout = 50 * time.Millisecond
	app.Add("cung dau", func(context.Context) error {
		time.Sleep(10 * time.Second)
		return nil
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("App bị treo bởi thành phần không tôn trợng ctx")
	}
}

func TestApp_LoiKhiDung(t *testing.T) {
	loiDung := errors.New("không đóng được kết nối")

	app := httpx.NewApp(quietLogger())
	app.Add("db",
		func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
		func(context.Context) error { return loiDung })

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	if err := <-errCh; !errors.Is(err, loiDung) {
		t.Errorf("Run = %v, muốn lỗi khi dừng", err)
	}
}

// ---------- helper ----------

func waitForAddr(t *testing.T, s *httpx.Server) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		addr := s.Addr()
		// Trước khi bind xong, Addr trả về giá trị cấu hình có cổng 0.
		if addr != "" && !strings.HasSuffix(addr, ":0") {
			return addr
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("server không bind được trong 5 giây")
	return ""
}
