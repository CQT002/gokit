package query_test

import (
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// user là model dùng cho mọi test trong package.
type user struct {
	ID        string
	Name      string
	Age       int
	CreatedAt string
}

// allowed là whitelist chuẩn của các test: tên field client gửi lên → tên cột.
//
// Cặp "createdAt" → "created_at" là chỗ đáng chú ý: nó cho thấy tên client dùng
// không nhất thiết trùng tên cột, nên schema database không lộ ra API.
var allowed = map[string]string{
	"id":        "id",
	"name":      "name",
	"age":       "age",
	"createdAt": "created_at",
}

// newDB dựng *gorm.DB chế độ DryRun cho một dialect, không cần database thật.
func newDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()

	var dial gorm.Dialector
	switch dialect {
	case "postgres":
		dial = postgres.New(postgres.Config{DSN: "postgres://u:p@127.0.0.1:5432/d?sslmode=disable"})
	case "mysql":
		dial = mysql.New(mysql.Config{
			DSN:                       "u:p@tcp(127.0.0.1:3306)/d",
			SkipInitializeWithVersion: true,
		})
	default:
		t.Fatalf("dialect lạ: %s", dialect)
	}

	gdb, err := gorm.Open(dial, &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
		Logger:               logger.Discard,
	})
	if err != nil {
		t.Fatalf("gorm.Open(%s): %v", dialect, err)
	}
	return gdb
}

// buildSQL chạy Find ở chế độ DryRun và trả về câu SQL cùng tham số.
func buildSQL(t *testing.T, tx *gorm.DB) (string, []any) {
	t.Helper()
	var out []user
	tx = tx.Find(&out)
	if tx.Error != nil {
		t.Fatalf("dựng câu lệnh thất bại: %v", tx.Error)
	}
	return tx.Statement.SQL.String(), tx.Statement.Vars
}
