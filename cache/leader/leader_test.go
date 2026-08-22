package leader_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache/leader"
)

// newRedis dựng client trên một Redis giả dùng chung cho nhiều Elector — cần
// thiết để mô phỏng nhiều instance tranh quyền.
func newRedis(t *testing.T) (redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func newElector(t *testing.T, rdb redis.UniversalClient, ttl, renew time.Duration) *leader.Elector {
	t.Helper()

	e, err := leader.NewElector(leader.ElectorConfig{
		Key:           "test",
		TTL:           ttl,
		RenewInterval: renew,
		Redis:         rdb,
		Logger:        quietLogger(),
	})
	if err != nil {
		t.Fatalf("NewElector: %v", err)
	}
	return e
}

func TestNewElector_ThieuCauHinh(t *testing.T) {
	rdb, _ := newRedis(t)

	if _, err := leader.NewElector(leader.ElectorConfig{Redis: rdb}); err == nil {
		t.Error("thiếu Key mà không báo lỗi")
	}
	if _, err := leader.NewElector(leader.ElectorConfig{Key: "k"}); err == nil {
		t.Error("thiếu Redis mà không báo lỗi")
	}
}

// RenewInterval >= TTL nghĩa là khoá hết hạn trước khi được gia hạn lần nào.
func TestNewElector_RenewIntervalKhongNhoHonTTL(t *testing.T) {
	rdb, _ := newRedis(t)

	_, err := leader.NewElector(leader.ElectorConfig{
		Key: "k", Redis: rdb,
		TTL: time.Second, RenewInterval: time.Second,
	})
	if err == nil {
		t.Fatal("RenewInterval = TTL mà không báo lỗi")
	}
}

func TestRun_ThieuOnLead(t *testing.T) {
	rdb, _ := newRedis(t)
	e := newElector(t, rdb, time.Second, 200*time.Millisecond)

	if err := e.Run(t.Context(), nil); err == nil {
		t.Fatal("onLead nil mà không báo lỗi")
	}
}

func TestRun_TroThanhLeaderRoiDungKhiCtxCancel(t *testing.T) {
	rdb, _ := newRedis(t)
	e := newElector(t, rdb, time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	led := make(chan struct{})

	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		runErr = e.Run(ctx, func(leadCtx context.Context) error {
			close(led)
			<-leadCtx.Done()
			return leadCtx.Err()
		})
	}()

	select {
	case <-led:
	case <-time.After(3 * time.Second):
		t.Fatal("không trở thành leader")
	}
	if !e.IsLeader() {
		t.Error("IsLeader() = false trong lúc onLead đang chạy")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run không dừng sau khi ctx cancel")
	}

	// Trả nil khi dừng vì ctx cancel, khớp với httpx.App.Run.
	if runErr != nil {
		t.Errorf("Run = %v, muốn nil khi ctx cancel", runErr)
	}
	if e.IsLeader() {
		t.Error("IsLeader() = true sau khi Run đã dừng")
	}
}

// Điều kiện duy nhất thật sự quan trọng: **đúng một** instance là leader tại
// mỗi thời điểm.
func TestRun_ChiMotLeader(t *testing.T) {
	rdb, _ := newRedis(t)

	const n = 5
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var active atomic.Int32
	var maxActive atomic.Int32
	var leadCount atomic.Int32

	var wg sync.WaitGroup
	for range n {
		e := newElector(t, rdb, time.Second, 50*time.Millisecond)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.Run(ctx, func(leadCtx context.Context) error {
				leadCount.Add(1)
				cur := active.Add(1)
				for {
					if m := maxActive.Load(); cur <= m || maxActive.CompareAndSwap(m, cur) {
						break
					}
				}
				defer active.Add(-1)

				<-leadCtx.Done()
				return leadCtx.Err()
			})
		}()
	}

	time.Sleep(600 * time.Millisecond)
	cancel()
	wg.Wait()

	if leadCount.Load() == 0 {
		t.Fatal("không instance nào trở thành leader")
	}
	if got := maxActive.Load(); got != 1 {
		t.Errorf("có lúc %d instance cùng làm leader, muốn 1", got)
	}
}

// Leader chết thì một instance khác phải lên thay.
func TestRun_LeaderChetThiCoNguoiLenThay(t *testing.T) {
	rdb, mr := newRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Instance 1 lên leader rồi bị "kill": mô phỏng bằng cách cancel ctx riêng
	// của nó, đúng như pod nhận SIGTERM.
	ctx1, cancel1 := context.WithCancel(ctx)
	e1 := newElector(t, rdb, 300*time.Millisecond, 60*time.Millisecond)
	led1 := make(chan struct{})
	go func() {
		_ = e1.Run(ctx1, func(leadCtx context.Context) error {
			close(led1)
			<-leadCtx.Done()
			return leadCtx.Err()
		})
	}()

	select {
	case <-led1:
	case <-time.After(3 * time.Second):
		t.Fatal("instance 1 không lên leader")
	}

	e2 := newElector(t, rdb, 300*time.Millisecond, 60*time.Millisecond)
	led2 := make(chan struct{})
	go func() {
		_ = e2.Run(ctx, func(leadCtx context.Context) error {
			close(led2)
			<-leadCtx.Done()
			return leadCtx.Err()
		})
	}()

	// Trong lúc instance 1 còn sống, instance 2 không được lên.
	select {
	case <-led2:
		t.Fatal("hai instance cùng làm leader")
	case <-time.After(200 * time.Millisecond):
	}

	cancel1()
	// Khoá được nhả lúc instance 1 dừng; nếu nhả không thành thì nó cũng hết hạn
	// sau TTL — cả hai đường đều phải dẫn tới instance 2 lên leader.
	mr.FastForward(400 * time.Millisecond)

	select {
	case <-led2:
	case <-time.After(3 * time.Second):
		t.Fatal("instance 1 chết mà không ai lên thay")
	}
}

// Mất khoá giữa nhiệm kỳ thì leadCtx bị cancel, và onLead phải dừng.
func TestRun_MatQuyenThiLeadCtxBiCancel(t *testing.T) {
	rdb, mr := newRedis(t)
	e := newElector(t, rdb, 200*time.Millisecond, 40*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lost := make(chan error, 4)
	go func() {
		_ = e.Run(ctx, func(leadCtx context.Context) error {
			<-leadCtx.Done()
			lost <- context.Cause(leadCtx)
			return leadCtx.Err()
		})
	}()

	// Chờ lên leader rồi xoá khoá dưới chân goroutine gia hạn.
	deadline := time.Now().Add(3 * time.Second)
	for !e.IsLeader() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !e.IsLeader() {
		t.Fatal("không lên leader")
	}
	mr.FlushAll()

	select {
	case cause := <-lost:
		if cause == nil {
			t.Error("leadCtx bị cancel mà không có cause")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mất khoá mà leadCtx không bị cancel")
	}
}

// Công việc thất bại thật thì Run trả lỗi ra ngoài — để cả service chết còn hơn
// để nó im lặng không chạy.
func TestRun_OnLeadLoiThiTraLoi(t *testing.T) {
	rdb, _ := newRedis(t)
	e := newElector(t, rdb, time.Second, 100*time.Millisecond)

	boom := errors.New("job sập")
	err := e.Run(t.Context(), func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, muốn bọc lỗi gốc", err)
	}
}

// Lỗi context từ onLead không phải lỗi của công việc: vòng lặp tranh lại.
func TestRun_OnLeadTraLoiContextThiTranhLai(t *testing.T) {
	rdb, _ := newRedis(t)
	e := newElector(t, rdb, 500*time.Millisecond, 80*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	var rounds atomic.Int32

	done := make(chan error, 1)
	go func() {
		done <- e.Run(ctx, func(context.Context) error {
			if rounds.Add(1) >= 3 {
				cancel()
			}
			return context.Canceled
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, muốn nil", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Run không dừng")
	}
	if rounds.Load() < 3 {
		t.Errorf("chỉ tranh %d lượt — không tranh lại sau lỗi context", rounds.Load())
	}
}

// Redis không truy cập được thì chờ rồi thử lại, không làm cả service chết:
// trong lúc đó cũng không ai là leader nên không có nguy cơ chạy trùng.
func TestRun_RedisSapThiThuLai(t *testing.T) {
	rdb, mr := newRedis(t)
	e := newElector(t, rdb, time.Second, 50*time.Millisecond)
	mr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	var led atomic.Bool
	err := e.Run(ctx, func(context.Context) error {
		led.Store(true)
		return nil
	})
	if err != nil {
		t.Errorf("Run = %v, muốn nil khi ctx hết hạn", err)
	}
	if led.Load() {
		t.Error("lên leader dù Redis không truy cập được")
	}
}

// Hai cuộc bầu khác Key không chặn nhau.
func TestRun_KeyKhacNhauKhongChanNhau(t *testing.T) {
	rdb, _ := newRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	led := make([]atomic.Bool, 2)

	for i, key := range []string{"bau-a", "bau-b"} {
		e, err := leader.NewElector(leader.ElectorConfig{
			Key: key, Redis: rdb,
			TTL: time.Second, RenewInterval: 50 * time.Millisecond,
			Logger: quietLogger(),
		})
		if err != nil {
			t.Fatalf("NewElector: %v", err)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.Run(ctx, func(leadCtx context.Context) error {
				led[i].Store(true)
				<-leadCtx.Done()
				return leadCtx.Err()
			})
		}()
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
	wg.Wait()

	if !led[0].Load() || !led[1].Load() {
		t.Errorf("led = %v, %v — hai Key khác nhau phải cùng lên leader được",
			led[0].Load(), led[1].Load())
	}
}
