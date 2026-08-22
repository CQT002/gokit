package migrate_test

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"

	"github.com/cqt002/gokit/db/migrate"
)

// newDB mở một SQLite trên file tạm của test.
//
// SQLite chứ không phải Postgres vì test này phải chạy được mà không có Docker,
// và thứ đang kiểm tra là logic điều phối migration — thứ tự, lịch sử,
// transaction — chứ không phải cú pháp DDL của từng database. Phần phụ thuộc
// dialect (advisory lock) có test riêng.
//
// File tạm chứ không phải :memory: vì mỗi connection trong pool sẽ thấy một
// database :memory: khác nhau.
func newDB(t *testing.T) *gorm.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: glogger.Discard})
	if err != nil {
		t.Fatalf("mở sqlite: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return gdb
}

// newOptions trả về Options ghi log vào buffer để test đọc lại được.
func newOptions(t *testing.T) (migrate.Options, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return migrate.Options{Logger: slog.New(h)}, &buf
}

// recorder ghi lại thứ tự các bước migration đã chạy.
type recorder struct {
	steps []string
}

// up trả về hàm Up ghi lại lời gọi và tạo một bảng thật.
func (r *recorder) up(id string) func(*gorm.DB) error {
	return func(tx *gorm.DB) error {
		r.steps = append(r.steps, "up:"+id)
		return tx.Exec("CREATE TABLE " + id + " (id INTEGER)").Error
	}
}

// down trả về hàm Down ghi lại lời gọi và xoá bảng.
func (r *recorder) down(id string) func(*gorm.DB) error {
	return func(tx *gorm.DB) error {
		r.steps = append(r.steps, "down:"+id)
		return tx.Exec("DROP TABLE " + id).Error
	}
}
