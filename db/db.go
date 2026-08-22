package db

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open mở kết nối tới database và trả về *gorm.DB đã cấu hình pool, logger và
// TLS.
//
// Trả về *gorm.DB trực tiếp, không bọc interface: mọi thứ người dùng cần —
// Session, Transaction, Migrator, Clauses — đều nằm trên type đó, và một
// interface tự định nghĩa chỉ có thể **bớt** đi chứ không thêm được gì.
//
// Hàm này ping database trước khi trả về. Sai host, sai mật khẩu, hết
// connection — mọi thứ đó lộ ra ngay lúc khởi động thay vì ở request đầu tiên,
// nơi nó thành lỗi 500 cho một người dùng thật.
//
// ctx chỉ dùng cho lần ping. Vòng đời của pool dài hơn ctx, nên hãy đóng nó
// bằng Close chứ không phải bằng cách cancel ctx.
func Open(ctx context.Context, cfg Config) (*gorm.DB, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()

	sqlDB, err := openSQL(cfg)
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}

	gdb, err := gorm.Open(dialector(cfg, sqlDB), &gorm.Config{
		Logger: newGormLogger(cfg.Logger, cfg.SlowThreshold, level, cfg.LogSQLParams),

		// Ping ở đây thì lỗi không mang được ctx. Tự ping bên dưới để tôn trọng
		// deadline của chỗ gọi.
		DisableAutomaticPing: true,

		// Đổi lỗi riêng của từng driver thành gorm.ErrDuplicatedKey,
		// ErrForeignKeyViolated... Không có nó thì code nghiệp vụ phải so khớp
		// chuỗi thông báo lỗi, và chuỗi đó khác nhau giữa Postgres với MySQL.
		TranslateError: true,
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: khởi tạo gorm: %w", err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: không kết nối được tới %s %s:%d/%s: %w",
			cfg.Driver, cfg.Host, cfg.Port, cfg.Database, err)
	}
	return gdb, nil
}

// dialector chọn driver GORM tương ứng, dùng lại *sql.DB đã dựng sẵn.
func dialector(cfg Config, sqlDB *sql.DB) gorm.Dialector {
	if cfg.Driver == MySQL {
		return mysql.New(mysql.Config{Conn: sqlDB})
	}
	return postgres.New(postgres.Config{Conn: sqlDB})
}

// Close đóng connection pool bên dưới *gorm.DB.
//
// GORM không có method Close vì *gorm.DB là handle chứ không phải connection.
// Hàm này là chỗ duy nhất cần biết điều đó.
func Close(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("db: lấy connection pool: %w", err)
	}
	return sqlDB.Close()
}

// Stats trả về hàm đọc thống kê connection pool tại thời điểm gọi.
//
// Hàm trả về an toàn khi gọi từ nhiều goroutine và đủ nhanh để dùng làm nguồn
// cho một collector Prometheus — xem RegisterMetrics.
func Stats(gdb *gorm.DB) (func() sql.DBStats, error) {
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("db: lấy connection pool: %w", err)
	}
	return sqlDB.Stats, nil
}
