// Package migrate chạy migration schema có ghi lịch sử và khoá chống chạy trùng.
//
// Migration ở đây là code Go nhận *gorm.DB, không phải file .sql. Lý do: phần
// lớn migration thật cần đọc dữ liệu trước khi đổi schema (chuẩn hoá giá trị cũ,
// backfill cột mới), và một file SQL thuần thì không làm được điều đó mà không
// nhúng logic vào SQL. Cần chạy SQL thô thì gọi tx.Exec — vẫn còn đó.
//
//	var List = []migrate.Migration{
//	    {
//	        ID:   "20260821_create_users",
//	        Up:   func(tx *gorm.DB) error { return tx.AutoMigrate(&User{}) },
//	        Down: func(tx *gorm.DB) error { return tx.Migrator().DropTable(&User{}) },
//	    },
//	}
//
//	if err := migrate.Run(ctx, gdb, List, migrate.Options{}); err != nil { ... }
//
// Thứ tự chạy là **thứ tự trong slice**, không phải thứ tự tên ID. Sắp xếp theo
// ID sẽ làm một migration đổi tên nhảy sang vị trí khác trong hàng đợi, còn
// slice thì thứ tự nằm ngay trong code và đọc được ở diff.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"gorm.io/gorm"
)

// Migration là một bước thay đổi schema.
type Migration struct {
	// ID là mã định danh, duy nhất trong cả danh sách. Đây là giá trị được ghi
	// vào bảng lịch sử, nên **không được đổi** sau khi đã chạy ở bất kỳ môi
	// trường nào: đổi ID nghĩa là migration đó sẽ chạy lại từ đầu.
	//
	// Quy ước tốt: tiền tố ngày, ví dụ "20260821_create_users".
	ID string

	// Up áp dụng thay đổi. Bắt buộc.
	//
	// tx là transaction riêng của migration này. Trả về lỗi thì transaction bị
	// rollback và bản ghi lịch sử không được tạo.
	Up func(tx *gorm.DB) error

	// Down hoàn tác thay đổi. Không bắt buộc; nil nghĩa là migration này không
	// rollback được và Rollback sẽ trả lỗi khi chạm tới nó.
	Down func(tx *gorm.DB) error
}

// Giá trị mặc định của Options.
const (
	// DefaultTable là tên bảng lưu lịch sử migration.
	DefaultTable = "schema_migrations"

	// DefaultLockTimeout là thời gian chờ khoá tối đa.
	DefaultLockTimeout = 30 * time.Second
)

// Options cấu hình Run, Rollback và Applied.
//
// Zero value dùng được: mọi field đều có mặc định.
type Options struct {
	// Table là tên bảng lưu lịch sử. Rỗng → DefaultTable.
	//
	// Mọi lần gọi trên cùng một database phải dùng cùng giá trị, nếu không thì
	// lần sau không thấy lịch sử của lần trước và chạy lại toàn bộ migration.
	Table string

	// Logger ghi log từng migration. nil thì dùng slog.Default().
	Logger *slog.Logger

	// LockTimeout là thời gian chờ khoá. 0 → DefaultLockTimeout.
	LockTimeout time.Duration

	// DisableLock tắt khoá.
	//
	// Chỉ tắt khi biết chắc chỉ có một tiến trình chạy migration. Khoá tồn tại
	// cho tình huống rất thường gặp: deploy nhiều pod cùng lúc, mỗi pod chạy
	// migration lúc khởi động, và hai pod cùng CREATE TABLE thì một pod chết.
	DisableLock bool
}

// normalize trả về bản copy đã điền mặc định.
func (o Options) normalize() Options {
	if o.Table == "" {
		o.Table = DefaultTable
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.LockTimeout <= 0 {
		o.LockTimeout = DefaultLockTimeout
	}
	return o
}

// Record là một dòng trong bảng lịch sử migration.
type Record struct {
	// ID là ID của migration đã chạy.
	ID string `gorm:"primaryKey;size:255"`
	// AppliedAt là thời điểm chạy xong.
	AppliedAt time.Time `gorm:"not null"`
}

// Run chạy các migration chưa được áp dụng, theo thứ tự trong ms.
//
// Mỗi migration chạy trong transaction riêng cùng với việc ghi bản ghi lịch sử,
// nên không có trạng thái "đã chạy nhưng chưa ghi nhận". Cảnh báo: MySQL không
// transaction hoá được DDL, nên ở MySQL một migration lỗi giữa đường có thể để
// lại schema dở dang — hãy giữ mỗi migration chỉ làm một việc.
//
// Migration đã chạy rồi thì bỏ qua. Migration có trong lịch sử nhưng không còn
// trong ms được ghi log ở mức Warn: thường là dấu hiệu code đã bị revert mà
// database thì không.
//
// Gọi Run nhiều lần là an toàn, kể cả song song từ nhiều tiến trình — xem
// Options.DisableLock.
func Run(ctx context.Context, db *gorm.DB, ms []Migration, opts Options) error {
	opts = opts.normalize()
	if err := validate(ms); err != nil {
		return err
	}

	return withLock(ctx, db, opts, func() error {
		applied, err := appliedSet(ctx, db, opts)
		if err != nil {
			return err
		}
		warnOrphans(ms, applied, opts.Logger)

		for _, m := range ms {
			if _, ok := applied[m.ID]; ok {
				continue
			}
			if err := apply(ctx, db, m, opts); err != nil {
				return err
			}
		}
		return nil
	})
}

// Rollback hoàn tác các migration đã chạy, theo thứ tự **ngược** với ms, cho tới
// khi tới migration có ID bằng to.
//
// Migration to **không** bị hoàn tác — nó là mốc muốn dừng lại, giống cách
// `git reset` nhận commit muốn giữ. to rỗng thì hoàn tác toàn bộ.
//
// Trả lỗi và không hoàn tác gì nếu to không có trong ms, hoặc nếu một migration
// cần hoàn tác có Down bằng nil. Kiểm tra trước khi chạy chứ không dừng giữa
// đường: rollback một nửa để lại schema mà không môi trường nào khác có.
func Rollback(ctx context.Context, db *gorm.DB, ms []Migration, to string, opts Options) error {
	opts = opts.normalize()
	if err := validate(ms); err != nil {
		return err
	}
	if to != "" && indexOf(ms, to) < 0 {
		return fmt.Errorf("migrate: không có migration nào tên %q", to)
	}

	return withLock(ctx, db, opts, func() error {
		applied, err := appliedSet(ctx, db, opts)
		if err != nil {
			return err
		}

		plan, err := rollbackPlan(ms, applied, to)
		if err != nil {
			return err
		}
		for _, m := range plan {
			if err := revert(ctx, db, m, opts); err != nil {
				return err
			}
		}
		return nil
	})
}

// Applied trả về lịch sử migration đã chạy, cũ trước mới sau.
//
// Dùng cho endpoint chẩn đoán hoặc script vận hành: so danh sách này với danh
// sách trong code là cách biết một môi trường đang lệch schema.
func Applied(ctx context.Context, db *gorm.DB, opts Options) ([]Record, error) {
	opts = opts.normalize()
	if err := ensureTable(ctx, db, opts); err != nil {
		return nil, err
	}

	var out []Record
	err := db.WithContext(ctx).Table(opts.Table).
		Order("applied_at, id").Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("migrate: đọc bảng %s: %w", opts.Table, err)
	}
	return out, nil
}

// rollbackPlan chọn các migration cần hoàn tác, theo thứ tự ngược.
//
// Tách khỏi Rollback để kiểm tra được toàn bộ kế hoạch trước khi chạm vào
// database.
func rollbackPlan(ms []Migration, applied map[string]struct{}, to string) ([]Migration, error) {
	var plan []Migration
	for i := len(ms) - 1; i >= 0; i-- {
		m := ms[i]
		if m.ID == to {
			break
		}
		if _, ok := applied[m.ID]; !ok {
			continue
		}
		if m.Down == nil {
			return nil, fmt.Errorf("migrate: migration %q không có Down nên không hoàn tác được", m.ID)
		}
		plan = append(plan, m)
	}
	return plan, nil
}

// apply chạy Up của một migration và ghi lịch sử, trong cùng transaction.
func apply(ctx context.Context, db *gorm.DB, m Migration, opts Options) error {
	log := opts.Logger.With(slog.String("migration", m.ID))
	log.InfoContext(ctx, "chạy migration")
	start := time.Now()

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.Up(tx); err != nil {
			return err
		}
		return tx.Table(opts.Table).Create(&Record{ID: m.ID, AppliedAt: time.Now().UTC()}).Error
	})
	if err != nil {
		return fmt.Errorf("migrate: migration %q thất bại: %w", m.ID, err)
	}

	log.InfoContext(ctx, "migration xong", slog.Duration("took", time.Since(start)))
	return nil
}

// revert chạy Down của một migration và xoá bản ghi lịch sử, trong cùng
// transaction.
func revert(ctx context.Context, db *gorm.DB, m Migration, opts Options) error {
	log := opts.Logger.With(slog.String("migration", m.ID))
	log.InfoContext(ctx, "hoàn tác migration")
	start := time.Now()

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.Down(tx); err != nil {
			return err
		}
		return tx.Table(opts.Table).Where("id = ?", m.ID).Delete(&Record{}).Error
	})
	if err != nil {
		return fmt.Errorf("migrate: hoàn tác migration %q thất bại: %w", m.ID, err)
	}

	log.InfoContext(ctx, "hoàn tác xong", slog.Duration("took", time.Since(start)))
	return nil
}

// ensureTable tạo bảng lịch sử nếu chưa có.
func ensureTable(ctx context.Context, db *gorm.DB, opts Options) error {
	if err := db.WithContext(ctx).Table(opts.Table).AutoMigrate(&Record{}); err != nil {
		return fmt.Errorf("migrate: tạo bảng %s: %w", opts.Table, err)
	}
	return nil
}

// appliedSet đọc tập ID đã chạy, tạo bảng lịch sử nếu cần.
func appliedSet(ctx context.Context, db *gorm.DB, opts Options) (map[string]struct{}, error) {
	recs, err := Applied(ctx, db, opts)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		set[r.ID] = struct{}{}
	}
	return set, nil
}

// validate kiểm tra danh sách migration trước khi chạm vào database.
//
// Kiểm tra hết một lượt rồi mới trả lỗi đầu tiên tìm được, chứ không vừa chạy
// vừa kiểm: một ID trùng ở cuối danh sách phải chặn cả những migration trước
// nó, không thì database dừng ở một trạng thái nửa vời.
func validate(ms []Migration) error {
	seen := make(map[string]struct{}, len(ms))
	for i, m := range ms {
		if m.ID == "" {
			return fmt.Errorf("migrate: migration ở vị trí %d không có ID", i)
		}
		if _, dup := seen[m.ID]; dup {
			return fmt.Errorf("migrate: ID %q xuất hiện nhiều lần", m.ID)
		}
		seen[m.ID] = struct{}{}

		if m.Up == nil {
			return fmt.Errorf("migrate: migration %q không có Up", m.ID)
		}
	}
	return nil
}

// warnOrphans cảnh báo các ID có trong lịch sử nhưng không còn trong code.
func warnOrphans(ms []Migration, applied map[string]struct{}, log *slog.Logger) {
	known := make(map[string]struct{}, len(ms))
	for _, m := range ms {
		known[m.ID] = struct{}{}
	}
	orphans := make([]string, 0)
	for id := range applied {
		if _, ok := known[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	// Sắp xếp để thứ tự dòng log không đổi giữa hai lần chạy — map trong Go trả
	// key theo thứ tự ngẫu nhiên, và log không lặp lại được thì khó so sánh.
	slices.Sort(orphans)
	for _, id := range orphans {
		log.Warn("migration có trong database nhưng không còn trong code",
			slog.String("migration", id))
	}
}

// indexOf tìm vị trí của một ID trong danh sách, -1 nếu không có.
func indexOf(ms []Migration, id string) int {
	for i, m := range ms {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// errNoLock là lỗi khi không lấy được khoá trong thời gian cho phép.
var errNoLock = errors.New("migrate: không lấy được khoá migration")
