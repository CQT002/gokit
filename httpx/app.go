package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"
)

// Component là một thành phần có vòng đời trong app.
type Component struct {
	// Name dùng trong log.
	Name string
	// Run chạy thành phần, block tới khi xong hoặc lỗi.
	Run func(context.Context) error
	// Stop dừng thành phần. nil nghĩa là Run tự dừng khi ctx cancel.
	Stop func(context.Context) error
}

// App quản lý vòng đời của nhiều thành phần chạy song song.
//
// Vấn đề nó giải quyết: một service thật có HTTP server, consumer Kafka, và vài
// cron job. Cả bốn phải khởi động cùng nhau, và khi nhận SIGTERM phải dừng **theo
// đúng thứ tự ngược lại** với thứ tự thêm vào. Viết tay bằng WaitGroup và channel
// thì mỗi service ra một phiên bản khác nhau, và phiên bản nào cũng thiếu một nhánh.
//
// Thứ tự dừng ngược lại có lý do: thành phần thêm sau thường phụ thuộc thành phần
// thêm trước. Đóng DB trước khi HTTP server drain xong nghĩa là những request cuối
// cùng nhận lỗi "connection closed".
type App struct {
	log        *slog.Logger
	components []Component

	// ShutdownTimeout là thời gian tối đa cho toàn bộ quá trình dừng.
	// <= 0 thì dùng DefaultShutdownTimeout.
	ShutdownTimeout time.Duration
}

// NewApp tạo App rỗng.
func NewApp(l *slog.Logger) *App {
	if l == nil {
		l = slog.Default()
	}
	return &App{log: l}
}

// Add thêm một thành phần.
//
// Thứ tự thêm là thứ tự khởi động, và ngược lại là thứ tự dừng.
func (a *App) Add(name string, run func(context.Context) error, stop func(context.Context) error) {
	a.components = append(a.components, Component{Name: name, Run: run, Stop: stop})
}

// Run khởi động mọi thành phần và block tới khi ctx cancel hoặc có thành phần lỗi.
//
// Thành phần nào lỗi cũng làm cả app dừng. Đây là lựa chọn có chủ ý: một service
// còn HTTP server sống nhưng consumer Kafka đã chết sẽ vẫn qua health check và vẫn
// nhận traffic, trong khi một nửa chức năng đã không hoạt động — và không ai biết.
// Thà chết hẳn để Kubernetes restart.
//
// Trả về lỗi đầu tiên khiến app dừng, hoặc nil nếu dừng vì ctx cancel.
func (a *App) Run(ctx context.Context) error {
	if len(a.components) == 0 {
		return errors.New("httpx: App không có thành phần nào")
	}

	timeout := a.ShutdownTimeout
	if timeout <= 0 {
		timeout = DefaultShutdownTimeout
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	type result struct {
		name string
		err  error
	}
	done := make(chan result, len(a.components))

	for _, c := range a.components {
		go func(c Component) {
			defer func() {
				if v := recover(); v != nil {
					done <- result{c.Name, fmt.Errorf("httpx: %s panic: %v", c.Name, v)}
				}
			}()
			a.log.Info("thành phần bắt đầu chạy", slog.String("component", c.Name))
			done <- result{c.Name, c.Run(runCtx)}
		}(c)
	}

	var (
		firstErr error
		// reported đếm số thành phần đã báo cáo xong. Dừng vì ctx cancel thì chưa
		// có ai báo cáo, và trừ cứng đi 1 sẽ làm vòng chờ bên dưới thiếu một cái.
		reported int
	)
	select {
	case r := <-done:
		reported = 1
		// Một thành phần thoát: dừng cả app.
		if r.err != nil {
			firstErr = fmt.Errorf("httpx: thành phần %s lỗi: %w", r.name, r.err)
			a.log.Error("thành phần lỗi, dừng app",
				slog.String("component", r.name), slog.Any("error", r.err))
		} else {
			a.log.Info("thành phần đã thoát, dừng app", slog.String("component", r.name))
		}
	case <-ctx.Done():
		a.log.Info("nhận tín hiệu dừng")
	}

	// Báo cho mọi thành phần biết phải dừng.
	cancelRun()

	stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancelStop()

	// Dừng theo thứ tự ngược: thành phần thêm sau phụ thuộc thành phần thêm trước.
	for i := len(a.components) - 1; i >= 0; i-- {
		c := a.components[i]
		if c.Stop == nil {
			continue
		}
		a.log.Info("đang dừng thành phần", slog.String("component", c.Name))
		if err := c.Stop(stopCtx); err != nil {
			a.log.Error("lỗi khi dừng thành phần",
				slog.String("component", c.Name), slog.Any("error", err))
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	// Chờ các goroutine còn lại thoát, nhưng không chờ quá thời gian cho phép:
	// một thành phần không tôn trọng ctx không được giữ process sống mãi.
	for range len(a.components) - reported {
		select {
		case r := <-done:
			if r.err != nil && !errors.Is(r.err, context.Canceled) && firstErr == nil {
				firstErr = r.err
			}
		case <-stopCtx.Done():
			a.log.Warn("hết thời gian chờ thành phần dừng, thoát luôn")
			return firstErr
		}
	}

	a.log.Info("app đã dừng")
	return firstErr
}

// RunWithSignals như Run nhưng tự dừng khi nhận SIGINT hoặc SIGTERM.
//
// Đây là hàm mà main gọi:
//
//	func main() {
//	    app := httpx.NewApp(logger)
//	    app.Add("http", srv.Run, nil)
//	    if err := app.RunWithSignals(context.Background()); err != nil {
//	        logger.Error("app dừng vì lỗi", slog.Any("error", err))
//	        os.Exit(1)
//	    }
//	}
func (a *App) RunWithSignals(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return a.Run(ctx)
}
