package db

import (
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newDryRunDB dựng *gorm.DB không cần database thật.
//
// DryRun làm GORM chạy hết chuỗi callback và dựng câu SQL nhưng không gửi đi,
// nên test kiểm tra được đúng thứ cần kiểm — câu lệnh sinh ra — mà không cần
// Docker. Pool bên dưới là thật (database/sql mở kết nối lười), nên Stats và
// Close vẫn dùng được.
//
// MySQL cần SkipInitializeWithVersion vì driver mặc định chạy SELECT VERSION()
// lúc khởi tạo. Bản Open dùng ở production **không** tắt bước đó: GORM cần biết
// version để chọn cú pháp RETURNING và kiểu JSON.
func newDryRunDB(t *testing.T, driver Driver) *gorm.DB {
	t.Helper()

	cfg := Config{
		Driver:   driver,
		Host:     "127.0.0.1",
		User:     "app",
		Password: "secret",
		Database: "app",
	}.withDefaults()

	sqlDB, err := openSQL(cfg)
	if err != nil {
		t.Fatalf("openSQL(%s): %v", driver, err)
	}

	var dial gorm.Dialector
	if driver == MySQL {
		dial = mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true})
	} else {
		dial = postgres.New(postgres.Config{Conn: sqlDB})
	}

	gdb, err := gorm.Open(dial, &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
		Logger:               logger.Discard,
	})
	if err != nil {
		t.Fatalf("gorm.Open(%s): %v", driver, err)
	}
	return gdb
}
