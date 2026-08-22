// Package cron chạy các job theo lịch trên **đúng một** instance của service.
//
// Ghép hai thứ: một scheduler cron và leader election. Không có leader election
// thì mười replica chạy mười lần cùng một job. Không có scheduler thì leader
// election chỉ cho biết ai là leader mà không có gì để chạy.
//
//	e, err := leader.NewElector(leader.ElectorConfig{Key: "billing", Redis: rdb})
//	if err != nil { return err }
//
//	c := cron.New(e, log)
//	if err := c.Add("dong-so-ngay", "0 1 * * *", closeDailyBooks); err != nil { return err }
//	if err := c.Add("don-session", "@every 10m", cleanSessions); err != nil { return err }
//
//	app.Add("cron", c.Run, nil)
//
// Cú pháp lịch là cron 5 trường tiêu chuẩn (phút giờ ngày tháng thứ), kèm các
// bí danh @hourly, @daily, @weekly, @monthly, @yearly và @every <duration>.
//
// Một chi tiết của thư viện bên dưới cần biết: `@every` nhỏ hơn một giây bị
// **âm thầm nâng lên một giây**. Nghĩa là "@every 100ms" chạy mỗi giây, không
// phải mỗi 100ms. Việc cần chu kỳ dưới một giây thì không thuộc về cron — dùng
// một vòng lặp với time.Ticker trong leader.Elector.Run.
package cron

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	robfig "github.com/robfig/cron/v3"

	"github.com/cqt002/gokit/cache/leader"
)

// Cron là tập job có lịch, chỉ chạy khi instance đang là leader.
//
// An toàn khi Add từ nhiều goroutine, nhưng Add sau khi Run đã chạy sẽ không có
// tác dụng cho nhiệm kỳ leader hiện tại — khai hết job trước khi gọi Run.
type Cron struct {
	elector *leader.Elector
	log     *slog.Logger

	mu   sync.Mutex
	jobs []job
}

// job là một mục trong lịch.
type job struct {
	name     string
	spec     string
	schedule robfig.Schedule
	run      func(context.Context) error

	// running chặn việc chạy chồng lên nhau. Trên mỗi job một cờ, không phải
	// một cờ chung: một job chạy lâu không được chặn các job khác.
	running atomic.Bool
}

// New dựng Cron trên một Elector.
//
// log nil thì dùng slog.Default().
func New(e *leader.Elector, log *slog.Logger) *Cron {
	if log == nil {
		log = slog.Default()
	}
	return &Cron{elector: e, log: log}
}

// Add thêm một job.
//
// Trả lỗi nếu spec sai cú pháp hoặc name đã tồn tại. Kiểm ngay lúc Add, không
// đợi tới Run: một biểu thức cron sai là lỗi cấu hình, và phát hiện nó lúc khởi
// động rẻ hơn nhiều so với phát hiện vào lúc job đáng lẽ phải chạy mà không chạy.
func (c *Cron) Add(name, spec string, fn func(context.Context) error) error {
	if name == "" {
		return errors.New("cron: name rỗng")
	}
	if fn == nil {
		return fmt.Errorf("cron: job %q không có hàm chạy", name)
	}

	schedule, err := robfig.ParseStandard(spec)
	if err != nil {
		return fmt.Errorf("cron: lịch %q của job %q không hợp lệ: %w", spec, name, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.jobs {
		if c.jobs[i].name == name {
			return fmt.Errorf("cron: job %q đã tồn tại", name)
		}
	}
	c.jobs = append(c.jobs, job{name: name, spec: spec, schedule: schedule, run: fn})
	return nil
}

// Jobs trả về tên các job đã khai, theo thứ tự thêm vào. Dùng cho log và
// endpoint chẩn đoán.
func (c *Cron) Jobs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, len(c.jobs))
	for i := range c.jobs {
		out[i] = c.jobs[i].name
	}
	return out
}

// Run tranh quyền leader và chạy scheduler khi thắng.
//
// Block tới khi ctx bị cancel. Mất quyền leader thì scheduler dừng và instance
// này quay lại tranh quyền — job đang chạy nhận context đã cancel.
//
// Trả nil khi dừng vì ctx bị cancel, nên ghép thẳng vào httpx.App được.
func (c *Cron) Run(ctx context.Context) error {
	if c.elector == nil {
		return errors.New("cron: Cron không có Elector")
	}

	c.mu.Lock()
	n := len(c.jobs)
	c.mu.Unlock()
	if n == 0 {
		return errors.New("cron: không có job nào")
	}

	return c.elector.Run(ctx, c.schedule)
}

// schedule chạy scheduler trong một nhiệm kỳ leader.
//
// Dựng một *robfig.Cron **mới** cho mỗi nhiệm kỳ thay vì start/stop lại một
// instance dùng chung: sau một lần đổi leader, trạng thái "lần chạy tiếp theo"
// của instance cũ đã lệch, và một scheduler mới thì không có gì để lệch.
func (c *Cron) schedule(ctx context.Context) error {
	c.mu.Lock()
	jobs := make([]*job, len(c.jobs))
	for i := range c.jobs {
		jobs[i] = &c.jobs[i]
	}
	c.mu.Unlock()

	// Không truyền logger của robfig: mọi thứ đáng ghi log đều đã được wrap của
	// package này ghi, kèm tên job và thời gian chạy.
	sched := robfig.New()
	for _, j := range jobs {
		sched.Schedule(j.schedule, c.wrap(ctx, j))
	}

	c.log.InfoContext(ctx, "bắt đầu chạy cron", slog.Any("jobs", c.Jobs()))
	sched.Start()

	<-ctx.Done()

	// Stop trả về context đóng khi các job đang chạy xong. Chờ nó, nhưng có
	// trần: một job treo không được giữ cả quá trình shutdown lại vô hạn — quá
	// giờ thì SIGKILL sẽ tới và tệ hơn nhiều.
	stopped := sched.Stop()
	select {
	case <-stopped.Done():
		c.log.Info("cron đã dừng, mọi job đã xong")
	case <-time.After(30 * time.Second):
		c.log.Warn("cron dừng nhưng còn job chưa xong sau 30s")
	}

	return ctx.Err()
}

// wrap bọc một job: chặn chạy chồng, bắt panic, ghi log kết quả.
func (c *Cron) wrap(ctx context.Context, j *job) robfig.Job {
	return robfig.FuncJob(func() {
		// Lần chạy trước chưa xong thì bỏ lượt này. Cho chạy chồng nghĩa là một
		// job chậm hơn chu kỳ của nó sẽ nhân đôi số bản chạy mỗi vòng, và đó là
		// cách làm sập database bằng chính scheduler của mình.
		if !j.running.CompareAndSwap(false, true) {
			c.log.WarnContext(ctx, "bỏ lượt vì lần chạy trước chưa xong",
				slog.String("job", j.name), slog.String("spec", j.spec))
			return
		}
		defer j.running.Store(false)

		log := c.log.With(slog.String("job", j.name))
		start := time.Now()

		defer func() {
			// Panic trong một job không được làm chết cả process: các job khác
			// và phần còn lại của service không liên quan gì tới nó.
			if r := recover(); r != nil {
				log.ErrorContext(ctx, "job panic",
					slog.Any("panic", r),
					slog.Duration("took", time.Since(start)))
			}
		}()

		log.DebugContext(ctx, "job bắt đầu")
		err := j.run(ctx)
		took := time.Since(start)

		switch {
		case err == nil:
			log.InfoContext(ctx, "job xong", slog.Duration("took", took))
		case errors.Is(err, context.Canceled):
			// Mất quyền leader hoặc service đang shutdown. Không phải lỗi của job.
			log.InfoContext(ctx, "job bị dừng giữa đường", slog.Duration("took", took))
		default:
			// Job lỗi **không** làm Run trả lỗi: một job thất bại không có nghĩa
			// là cả scheduler hỏng, và các job khác vẫn phải chạy. Đây là chỗ
			// alert nên bám vào.
			log.ErrorContext(ctx, "job thất bại",
				slog.String("error", err.Error()),
				slog.Duration("took", took))
		}
	})
}
