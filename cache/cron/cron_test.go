package cron_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache/cron"
	"github.com/cqt002/gokit/cache/leader"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// newCron dựng Cron trên một Elector thật, chạy trên Redis giả.
func newCron(t *testing.T) (*cron.Cron, *leader.Elector, redis.UniversalClient) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	t.Cleanup(func() { _ = rdb.Close() })

	e, err := leader.NewElector(leader.ElectorConfig{
		Key:           "cron-test",
		TTL:           time.Second,
		RenewInterval: 100 * time.Millisecond,
		Redis:         rdb,
		Logger:        quietLogger(),
	})
	if err != nil {
		t.Fatalf("NewElector: %v", err)
	}
	return cron.New(e, quietLogger()), e, rdb
}

func TestAdd_SpecSai(t *testing.T) {
	c, _, _ := newCron(t)

	if err := c.Add("job", "khong-phai-cron", func(context.Context) error { return nil }); err == nil {
		t.Error("spec sai mà Add không báo lỗi")
	}
	if err := c.Add("job", "60 * * * *", func(context.Context) error { return nil }); err == nil {
		t.Error("phút 60 nằm ngoài khoảng mà Add không báo lỗi")
	}
	if err := c.Add("job", "* * * *", func(context.Context) error { return nil }); err == nil {
		t.Error("spec thiếu trường mà Add không báo lỗi")
	}
}

func TestAdd_ThamSoSai(t *testing.T) {
	c, _, _ := newCron(t)

	if err := c.Add("", "@every 1m", func(context.Context) error { return nil }); err == nil {
		t.Error("name rỗng mà không báo lỗi")
	}
	if err := c.Add("job", "@every 1m", nil); err == nil {
		t.Error("hàm nil mà không báo lỗi")
	}
}

func TestAdd_TrungTen(t *testing.T) {
	c, _, _ := newCron(t)
	fn := func(context.Context) error { return nil }

	if err := c.Add("job", "@every 1m", fn); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := c.Add("job", "@every 2m", fn); err == nil {
		t.Error("tên trùng mà không báo lỗi")
	}
}

func TestAdd_CacDangSpecHopLe(t *testing.T) {
	c, _, _ := newCron(t)
	fn := func(context.Context) error { return nil }

	specs := []string{"0 1 * * *", "*/5 * * * *", "@daily", "@hourly", "@every 10m"}
	for i, spec := range specs {
		if err := c.Add(string(rune('a'+i)), spec, fn); err != nil {
			t.Errorf("spec %q bị từ chối: %v", spec, err)
		}
	}
	if got := len(c.Jobs()); got != len(specs) {
		t.Errorf("Jobs() = %d, muốn %d", got, len(specs))
	}
}

func TestJobs_TheoThuTuThem(t *testing.T) {
	c, _, _ := newCron(t)
	fn := func(context.Context) error { return nil }

	for _, name := range []string{"c", "a", "b"} {
		if err := c.Add(name, "@every 1m", fn); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if got := c.Jobs(); !slices.Equal(got, []string{"c", "a", "b"}) {
		t.Errorf("Jobs() = %v", got)
	}
}

func TestRun_KhongCoJob(t *testing.T) {
	c, _, _ := newCron(t)

	if err := c.Run(t.Context()); err == nil {
		t.Fatal("không có job nào mà Run không báo lỗi")
	}
}

func TestRun_KhongCoElector(t *testing.T) {
	c := cron.New(nil, quietLogger())
	if err := c.Add("job", "@every 1m", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := c.Run(t.Context()); err == nil {
		t.Fatal("không có Elector mà Run không báo lỗi")
	}
}

func TestRun_JobChayTheoLich(t *testing.T) {
	c, _, _ := newCron(t)

	// Chu kỳ 1 giây là mức nhỏ nhất dùng được: robfig ép mọi `@every` dưới một
	// giây lên thành một giây.
	var runs atomic.Int32
	if err := c.Add("dem", "@every 1s", func(context.Context) error {
		runs.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()

	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := runs.Load(); got < 2 {
		t.Errorf("job chạy %d lần trong 2.2s với chu kỳ 1s, muốn ít nhất 2", got)
	}
}

// Chỉ instance đang là leader chạy job. Không có phần này thì mười replica gửi
// mười email giống nhau.
func TestRun_ChiLeaderChayJob(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	defer rdb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var total atomic.Int32
	const instances = 4

	for range instances {
		e, err := leader.NewElector(leader.ElectorConfig{
			Key:           "shared",
			TTL:           time.Second,
			RenewInterval: 100 * time.Millisecond,
			Redis:         rdb,
			Logger:        quietLogger(),
		})
		if err != nil {
			t.Fatalf("NewElector: %v", err)
		}

		c := cron.New(e, quietLogger())
		if err := c.Add("dem", "@every 1s", func(context.Context) error {
			total.Add(1)
			return nil
		}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		go func() { _ = c.Run(ctx) }()
	}

	time.Sleep(2200 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)

	got := total.Load()
	if got == 0 {
		t.Fatal("không job nào chạy")
	}
	// Với chu kỳ 1s trong ~2.2s, một instance chạy tối đa 2–3 lần. Bốn instance
	// cùng chạy sẽ cho quanh 8.
	if got > 3 {
		t.Errorf("job chạy %d lần với %d instance — có instance không phải leader cũng chạy",
			got, instances)
	}
}

// Cho chạy chồng nghĩa là một job chậm hơn chu kỳ của nó sẽ nhân đôi số bản chạy
// mỗi vòng — đó là cách làm sập database bằng chính scheduler của mình.
func TestRun_KhongChayChongLenNhau(t *testing.T) {
	c, _, _ := newCron(t)

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	if err := c.Add("cham", "@every 1s", func(ctx context.Context) error {
		cur := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			if m := maxConcurrent.Load(); cur <= m || maxConcurrent.CompareAndSwap(m, cur) {
				break
			}
		}
		// Chạy lâu hơn chu kỳ.
		time.Sleep(1200 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2600*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := maxConcurrent.Load(); got > 1 {
		t.Errorf("có lúc %d bản của cùng một job cùng chạy", got)
	}
}

// Panic trong một job không được làm chết process, và không được chặn job khác.
func TestRun_JobPanic(t *testing.T) {
	c, _, _ := newCron(t)

	var lanh atomic.Int32
	if err := c.Add("no", "@every 1s", func(context.Context) error {
		panic("nổ")
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := c.Add("lanh", "@every 1s", func(context.Context) error {
		lanh.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lanh.Load() == 0 {
		t.Error("job lành không chạy — panic của job kia đã chặn nó")
	}
}

// Job lỗi không làm Run trả lỗi: một job thất bại không có nghĩa là cả scheduler
// hỏng, và các job khác vẫn phải chạy.
func TestRun_JobLoiKhongLamDungScheduler(t *testing.T) {
	c, _, _ := newCron(t)

	var runs atomic.Int32
	if err := c.Add("loi", "@every 1s", func(context.Context) error {
		runs.Add(1)
		return errors.New("thất bại")
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run = %v, muốn nil", err)
	}
	if runs.Load() < 2 {
		t.Errorf("job chỉ chạy %d lần — lỗi lần đầu đã dừng scheduler", runs.Load())
	}
}

// Job đang chạy nhận context đã cancel khi service shutdown.
func TestRun_JobNhanContextCancelKhiShutdown(t *testing.T) {
	c, _, _ := newCron(t)

	started := make(chan struct{})
	sawCancel := make(chan struct{})
	var once atomic.Bool

	if err := c.Add("dai", "@every 1s", func(ctx context.Context) error {
		if !once.CompareAndSwap(false, true) {
			return nil
		}
		close(started)
		<-ctx.Done()
		close(sawCancel)
		return ctx.Err()
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("job không chạy")
	}
	cancel()

	select {
	case <-sawCancel:
	case <-time.After(3 * time.Second):
		t.Fatal("job không nhận được context đã cancel")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, muốn nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run không dừng")
	}
}
