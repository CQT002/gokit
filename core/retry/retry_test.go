package retry_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/retry"
)

// fast là policy cho test: delay đủ nhỏ để test nhanh, và Jitter âm để tắt phần
// ngẫu nhiên nên khẳng định về thời gian mới có nghĩa.
func fast(maxAttempts int) retry.Policy {
	return retry.Policy{
		MaxAttempts: maxAttempts,
		BaseDelay:   time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		Multiplier:  2,
		Jitter:      -1,
	}
}

var errTam = errors.New("lỗi tạm thời")

func TestDo_ThanhCongLanDau(t *testing.T) {
	var calls int
	err := retry.Do(context.Background(), fast(3), func(context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("Do = %v, muốn nil", err)
	}
	if calls != 1 {
		t.Errorf("số lần gọi = %d, muốn 1", calls)
	}
}

func TestDo_ThanhCongSauVaiLan(t *testing.T) {
	var calls int
	err := retry.Do(context.Background(), fast(5), func(context.Context) error {
		calls++
		if calls < 3 {
			return errTam
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do = %v, muốn nil", err)
	}
	if calls != 3 {
		t.Errorf("số lần gọi = %d, muốn 3", calls)
	}
}

func TestDo_HetLuotTraLoiCuoi(t *testing.T) {
	errCuoi := errors.New("lần cuối")
	var calls int

	err := retry.Do(context.Background(), fast(4), func(context.Context) error {
		calls++
		if calls == 4 {
			return errCuoi
		}
		return errTam
	})

	if calls != 4 {
		t.Errorf("số lần gọi = %d, muốn 4", calls)
	}
	if !errors.Is(err, errCuoi) {
		t.Errorf("Do = %v, muốn lỗi của lần thử cuối", err)
	}
}

// MaxAttempts đếm cả lần đầu, nên 1 nghĩa là không thử lại.
func TestDo_MaxAttemptsMotThiKhongThuLai(t *testing.T) {
	var calls int
	err := retry.Do(context.Background(), retry.Policy{MaxAttempts: 1}, func(context.Context) error {
		calls++
		return errTam
	})
	if calls != 1 {
		t.Errorf("số lần gọi = %d, muốn 1", calls)
	}
	if !errors.Is(err, errTam) {
		t.Errorf("Do = %v", err)
	}
}

func TestDo_MacDinhBaLan(t *testing.T) {
	var calls int
	// Policy zero: phải dùng mặc định, nhưng delay mặc định 100ms nên chỉ khai
	// BaseDelay để test không chờ 300ms.
	_ = retry.Do(context.Background(), retry.Policy{BaseDelay: time.Millisecond, Jitter: -1},
		func(context.Context) error {
			calls++
			return errTam
		})
	if calls != retry.DefaultMaxAttempts {
		t.Errorf("số lần gọi = %d, muốn %d", calls, retry.DefaultMaxAttempts)
	}
}

// Lỗi không đáng thử lại phải dừng ngay, không đợi hết lượt: thử lại một lỗi
// không bao giờ tự khỏi chỉ nhân bản tải lên hệ thống đang lỗi.
func TestDo_LoiKhongDangThuLaiDungNgay(t *testing.T) {
	errVinhVien := errors.New("400 bad request")
	var calls int

	p := fast(5)
	p.Retryable = func(err error) bool { return !errors.Is(err, errVinhVien) }

	err := retry.Do(context.Background(), p, func(context.Context) error {
		calls++
		return errVinhVien
	})
	if calls != 1 {
		t.Errorf("số lần gọi = %d, muốn 1 — đã thử lại lỗi vĩnh viễn", calls)
	}
	if !errors.Is(err, errVinhVien) {
		t.Errorf("Do = %v", err)
	}
}

func TestDefaultRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"lỗi thường", errTam, true},
		{"context.Canceled", context.Canceled, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
		{"context.Canceled bị bọc", errors.Join(errTam, context.Canceled), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retry.DefaultRetryable(tt.err); got != tt.want {
				t.Errorf("DefaultRetryable(%v) = %v, muốn %v", tt.err, got, tt.want)
			}
		})
	}
}

// Mặc định không thử lại lỗi context: chỗ gọi đã bỏ cuộc thì làm tiếp là vô ích.
func TestDo_MacDinhKhongThuLaiLoiContext(t *testing.T) {
	var calls int
	err := retry.Do(context.Background(), fast(5), func(context.Context) error {
		calls++
		return context.DeadlineExceeded
	})
	if calls != 1 {
		t.Errorf("số lần gọi = %d, muốn 1", calls)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Do = %v", err)
	}
}

func TestDo_ContextDaCancelTruocKhiBatDau(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls int
	err := retry.Do(ctx, fast(3), func(context.Context) error {
		calls++
		return nil
	})
	if calls != 0 {
		t.Errorf("fn được gọi %d lần dù context đã cancel", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Do = %v, muốn context.Canceled", err)
	}
}

// Cancel giữa lúc đang chờ phải thoát ngay, và lỗi trả về phải giữ được cả hai
// thông tin: vì sao đang thử lại, và vì sao dừng.
func TestDo_CancelTrongLucCho(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32

	p := retry.Policy{MaxAttempts: 10, BaseDelay: 2 * time.Second, Jitter: -1}

	start := time.Now()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := retry.Do(ctx, p, func(context.Context) error {
		calls.Add(1)
		return errTam
	})
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("mất %v mới thoát — không tôn trọng context khi đang chờ", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("số lần gọi = %d, muốn 1", got)
	}
	if !errors.Is(err, errTam) {
		t.Errorf("mất lỗi của lần thử cuối: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("mất lỗi của context: %v", err)
	}
}

// Backoff phải thực sự luỹ tiến: 1ms + 2ms + 4ms, không phải chờ bằng nhau.
func TestDo_BackoffLuyTien(t *testing.T) {
	p := retry.Policy{
		MaxAttempts: 4,
		BaseDelay:   20 * time.Millisecond,
		MaxDelay:    time.Second,
		Multiplier:  2,
		Jitter:      -1,
	}

	start := time.Now()
	_ = retry.Do(context.Background(), p, func(context.Context) error { return errTam })
	elapsed := time.Since(start)

	// Ba lần chờ: 20 + 40 + 80 = 140ms.
	const want = 140 * time.Millisecond
	if elapsed < want {
		t.Errorf("mất %v, muốn ít nhất %v — delay không luỹ tiến", elapsed, want)
	}
	if elapsed > want*4 {
		t.Errorf("mất %v, quá lâu so với %v mong đợi", elapsed, want)
	}
}

func TestDo_MaxDelayLaTran(t *testing.T) {
	p := retry.Policy{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Multiplier:  1000, // sẽ vượt trần ngay từ lần chờ thứ hai
		Jitter:      -1,
	}

	start := time.Now()
	_ = retry.Do(context.Background(), p, func(context.Context) error { return errTam })
	elapsed := time.Since(start)

	// Bốn lần chờ, trần 5ms mỗi lần: tối đa 20ms cộng phần chạy.
	if elapsed > 500*time.Millisecond {
		t.Errorf("mất %v — MaxDelay không được tôn trọng", elapsed)
	}
}

// Multiplier lớn với nhiều lần thử làm BaseDelay * Multiplier^n tràn khỏi int64;
// Duration âm do tràn số sẽ biến lần chờ thành 0 giây.
func TestDo_KhongTranSo(t *testing.T) {
	p := retry.Policy{
		MaxAttempts: 40,
		BaseDelay:   time.Hour,
		MaxDelay:    2 * time.Millisecond,
		Multiplier:  1e9,
		Jitter:      -1,
	}

	start := time.Now()
	err := retry.Do(context.Background(), p, func(context.Context) error { return errTam })
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("muốn lỗi")
	}
	// 39 lần chờ, trần 2ms: khoảng 78ms. Nếu tràn số thành delay âm thì sẽ nhanh
	// hơn nhiều, còn nếu không kẹp trần thì sẽ treo mãi.
	if elapsed > 5*time.Second {
		t.Errorf("mất %v — trần không có tác dụng khi Multiplier quá lớn", elapsed)
	}
}

func TestDo_MultiplierNhoHonMotBiKepVeMot(t *testing.T) {
	p := retry.Policy{
		MaxAttempts: 4,
		BaseDelay:   15 * time.Millisecond,
		MaxDelay:    time.Second,
		Multiplier:  0.1,
		Jitter:      -1,
	}

	start := time.Now()
	_ = retry.Do(context.Background(), p, func(context.Context) error { return errTam })
	elapsed := time.Since(start)

	// Kẹp về 1 nghĩa là delay không đổi: 15 * 3 = 45ms. Nếu để 0.1 thì tổng chỉ
	// khoảng 16ms.
	if elapsed < 45*time.Millisecond {
		t.Errorf("mất %v, muốn ít nhất 45ms — Multiplier < 1 không bị kẹp", elapsed)
	}
}

// Jitter phải thực sự ngẫu nhiên, nếu không thì N instance vẫn thử lại cùng lúc.
func TestJitter_TaoRaKhacBiet(t *testing.T) {
	p := retry.Policy{
		MaxAttempts: 2,
		BaseDelay:   20 * time.Millisecond,
		MaxDelay:    time.Second,
		Jitter:      0.9,
	}

	seen := make(map[time.Duration]struct{})
	for range 12 {
		start := time.Now()
		_ = retry.Do(context.Background(), p, func(context.Context) error { return errTam })
		seen[time.Since(start).Round(time.Millisecond)] = struct{}{}
	}
	if len(seen) < 3 {
		t.Errorf("12 lần chạy chỉ ra %d giá trị delay khác nhau — jitter không hoạt động", len(seen))
	}
}

// Jitter chưa khai phải dùng mặc định, không phải tắt: mất jitter là mất đúng
// tính chất package này tồn tại để bảo đảm.
func TestJitter_ZeroDungMacDinh(t *testing.T) {
	p := retry.Policy{MaxAttempts: 2, BaseDelay: 20 * time.Millisecond, MaxDelay: time.Second}

	seen := make(map[time.Duration]struct{})
	for range 12 {
		start := time.Now()
		_ = retry.Do(context.Background(), p, func(context.Context) error { return errTam })
		seen[time.Since(start).Round(time.Millisecond)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Errorf("Jitter chưa khai cho ra %d giá trị delay — mặc định đã bị tắt", len(seen))
	}
}

// ---------- DoValue ----------

func TestDoValue(t *testing.T) {
	var calls int
	got, err := retry.DoValue(context.Background(), fast(5), func(context.Context) (string, error) {
		calls++
		if calls < 2 {
			return "", errTam
		}
		return "xong", nil
	})
	if err != nil {
		t.Fatalf("DoValue = %v", err)
	}
	if got != "xong" {
		t.Errorf("giá trị = %q, muốn \"xong\"", got)
	}
}

func TestDoValue_LoiThiTraZero(t *testing.T) {
	got, err := retry.DoValue(context.Background(), fast(2), func(context.Context) (int, error) {
		return 42, errTam
	})
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if got != 0 {
		t.Errorf("giá trị = %d, khi có lỗi phải trả zero", got)
	}
}

func TestDoValue_KieuConTro(t *testing.T) {
	type kq struct{ Name string }
	got, err := retry.DoValue(context.Background(), fast(2), func(context.Context) (*kq, error) {
		return nil, errTam
	})
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if got != nil {
		t.Errorf("giá trị = %v, muốn nil", got)
	}
}

// fn phải nhận đúng context đã truyền vào, để nó tự huỷ được thao tác bên trong.
func TestDo_TruyenContextVaoFn(t *testing.T) {
	type keyT struct{}
	ctx := context.WithValue(context.Background(), keyT{}, "giá trị")

	err := retry.Do(ctx, fast(1), func(inner context.Context) error {
		if inner.Value(keyT{}) != "giá trị" {
			t.Error("fn nhận context khác context đã truyền vào")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do = %v", err)
	}
}
