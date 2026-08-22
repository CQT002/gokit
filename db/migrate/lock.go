package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// lockPollInterval là khoảng nghỉ giữa hai lần thử lấy khoá của Postgres.
const lockPollInterval = 250 * time.Millisecond

// withLock chạy fn khi đang giữ khoá migration.
//
// Khoá là advisory lock ở tầng database, không phải mutex trong process: thứ cần
// chặn là **hai tiến trình** cùng chạy migration, tình huống mặc định khi deploy
// nhiều pod và mỗi pod migrate lúc khởi động.
//
// Driver không có advisory lock (ví dụ SQLite) chạy fn mà không khoá, kèm một
// dòng log. Đó là đánh đổi có ý thức: SQLite chỉ có một writer nên rủi ro thấp,
// và trả lỗi ở đây sẽ làm test đơn vị không chạy được migration.
func withLock(ctx context.Context, db *gorm.DB, opts Options, fn func() error) error {
	if opts.DisableLock {
		return fn()
	}

	dialect := ""
	if d := db.Dialector; d != nil {
		dialect = d.Name()
	}
	if dialect != "postgres" && dialect != "mysql" {
		opts.Logger.DebugContext(ctx, "driver không có advisory lock, chạy migration không khoá",
			slog.String("dialect", dialect))
		return fn()
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("migrate: lấy connection pool: %w", err)
	}

	// Advisory lock của cả Postgres và MySQL gắn với **session**, nên phải giữ
	// đúng một connection từ lúc lấy khoá tới lúc nhả. Lấy qua pool thì lần
	// unlock có thể rơi vào connection khác và khoá bị treo tới khi hết session.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: mở connection cho khoá migration: %w", err)
	}
	defer func() { _ = conn.Close() }()

	lockCtx, cancel := context.WithTimeout(ctx, opts.LockTimeout)
	defer cancel()

	if dialect == "postgres" {
		err = lockPostgres(lockCtx, conn, lockKey(opts.Table))
	} else {
		err = lockMySQL(lockCtx, conn, opts.Table, opts.LockTimeout)
	}
	if err != nil {
		return err
	}

	defer func() {
		// Dùng context.WithoutCancel: khi fn thất bại vì ctx bị cancel thì vẫn
		// phải nhả khoá, nếu không thì lần deploy sau phải chờ hết session.
		unlockCtx, unlockCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer unlockCancel()

		var uerr error
		if dialect == "postgres" {
			_, uerr = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", lockKey(opts.Table))
		} else {
			_, uerr = conn.ExecContext(unlockCtx, "SELECT RELEASE_LOCK(?)", opts.Table)
		}
		if uerr != nil {
			// Không trả lỗi này ra ngoài: migration đã chạy xong, và khoá tự mất
			// khi connection đóng ngay sau đây.
			opts.Logger.WarnContext(ctx, "nhả khoá migration thất bại",
				slog.String("error", uerr.Error()))
		}
	}()

	return fn()
}

// lockPostgres lấy advisory lock bằng cách thử lại cho tới khi hết ctx.
//
// Dùng pg_try_advisory_lock trong vòng lặp thay vì pg_advisory_lock chờ sẵn:
// dạng chờ sẵn phải cancel query giữa lúc đang chờ mới dừng được, còn dạng thử
// thì mỗi lần gọi là một câu lệnh kết thúc ngay và deadline của ctx được tôn
// trọng đúng như mọi chỗ khác.
func lockPostgres(ctx context.Context, conn *sql.Conn, key int64) error {
	ticker := time.NewTicker(lockPollInterval)
	defer ticker.Stop()

	for {
		var got bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil {
			return fmt.Errorf("migrate: lấy khoá migration: %w", err)
		}
		if got {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", errNoLock, ctx.Err())
		case <-ticker.C:
		}
	}
}

// lockMySQL lấy khoá bằng GET_LOCK, hàm có sẵn timeout.
//
// GET_LOCK trả 1 khi lấy được, 0 khi hết thời gian chờ, và NULL khi có lỗi —
// nên kết quả phải scan vào *bool để phân biệt được NULL.
func lockMySQL(ctx context.Context, conn *sql.Conn, name string, timeout time.Duration) error {
	secs := int(timeout.Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}

	var got *bool
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", name, secs).Scan(&got); err != nil {
		return fmt.Errorf("migrate: lấy khoá migration: %w", err)
	}
	if got == nil {
		return fmt.Errorf("%w: GET_LOCK trả NULL", errNoLock)
	}
	if !*got {
		return fmt.Errorf("%w: hết %s chờ", errNoLock, timeout)
	}
	return nil
}

// lockKey đổi tên bảng lịch sử thành khoá số cho pg_advisory_lock.
//
// Postgres chỉ nhận khoá dạng số, nên phải hash. Dẫn xuất từ tên bảng để hai
// database dùng chung một Postgres instance nhưng khác bảng lịch sử không chặn
// nhau — advisory lock có phạm vi cả cluster, không phải từng database.
//
// FNV-1a chứ không phải hash mật mã: đây là khoá điều phối, không phải giá trị
// bảo mật, và va chạm hash chỉ dẫn tới việc chờ nhau không cần thiết.
func lockKey(table string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("gokit/migrate:" + table))
	return int64(h.Sum64()) //nolint:gosec // cần đúng 64 bit, dấu không có nghĩa
}
