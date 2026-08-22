package db

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm/logger"

	"github.com/cqt002/gokit/core/secret"
	"github.com/cqt002/gokit/core/tlsx"
)

// Driver là loại database.
type Driver string

// Các driver được hỗ trợ.
const (
	Postgres Driver = "postgres"
	MySQL    Driver = "mysql"
)

// Giá trị mặc định của Config.
const (
	DefaultMaxOpenConns    = 25
	DefaultMaxIdleConns    = 5
	DefaultConnMaxLifetime = 30 * time.Minute
	DefaultConnMaxIdleTime = 5 * time.Minute
	DefaultSlowThreshold   = 200 * time.Millisecond
	DefaultConnectTimeout  = 10 * time.Second

	// DefaultPortPostgres, DefaultPortMySQL là cổng mặc định theo driver.
	DefaultPortPostgres = 5432
	DefaultPortMySQL    = 3306
)

// Config cấu hình một kết nối database.
//
// Mọi field có giá trị mặc định hợp lý khi để zero, trừ Driver, Host, User và
// Database — bốn thứ không đoán được.
type Config struct {
	// Driver là loại database: Postgres hoặc MySQL. Bắt buộc.
	Driver Driver `yaml:"driver" env:"DB_DRIVER"`

	// Host là hostname hoặc IP của database. Bắt buộc.
	Host string `yaml:"host" env:"DB_HOST"`

	// Port là cổng. 0 thì dùng cổng mặc định của driver.
	Port int `yaml:"port" env:"DB_PORT"`

	// User là user đăng nhập. Bắt buộc.
	User string `yaml:"user" env:"DB_USER"`

	// Password là mật khẩu. Kiểu secret.Secret nên nó không lọt ra log khi
	// ai đó in cả struct Config.
	Password secret.Secret `yaml:"password" env:"DB_PASSWORD"`

	// Database là tên database. Bắt buộc.
	Database string `yaml:"database" env:"DB_NAME"`

	// Schema đặt search_path. Chỉ dùng cho Postgres — khai với MySQL là lỗi,
	// vì ở MySQL "schema" đồng nghĩa với database và một giá trị bị bỏ qua
	// trong im lặng là cách tạo ra sự cố không ai tìm được nguyên nhân.
	Schema string `yaml:"schema" env:"DB_SCHEMA"`

	// TimeZone là timezone của session, ví dụ "Asia/Ho_Chi_Minh". Rỗng thì
	// dùng mặc định của server.
	//
	// Với MySQL, giá trị này cũng là *time.Location dùng để parse cột DATETIME
	// — kiểu DATETIME không mang offset nên không có nó thì giờ đọc ra lệch.
	TimeZone string `yaml:"time_zone" env:"DB_TIME_ZONE"`

	// MaxOpenConns là số connection tối đa (đang dùng + rảnh). 0 → 25.
	//
	// Đây là con số cần tính chứ không phải tăng dần cho hết lỗi: tổng
	// MaxOpenConns của **mọi** instance service phải nhỏ hơn max_connections
	// của database, nếu không thì pod scale lên là database từ chối kết nối.
	MaxOpenConns int `yaml:"max_open_conns" env:"DB_MAX_OPEN_CONNS"`

	// MaxIdleConns là số connection rảnh được giữ lại. 0 → 5.
	//
	// Lớn hơn MaxOpenConns thì database/sql tự hạ xuống bằng MaxOpenConns.
	MaxIdleConns int `yaml:"max_idle_conns" env:"DB_MAX_IDLE_CONNS"`

	// ConnMaxLifetime là tuổi tối đa của một connection. 0 → 30 phút.
	//
	// Cần khác 0 khi database nằm sau proxy hoặc load balancer: connection
	// sống mãi sẽ dính vào đúng một node và mọi lần failover đều thành lỗi ở
	// tầng ứng dụng.
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime" env:"DB_CONN_MAX_LIFETIME"`

	// ConnMaxIdleTime là thời gian một connection rảnh được giữ. 0 → 5 phút.
	ConnMaxIdleTime time.Duration `yaml:"conn_max_idle_time" env:"DB_CONN_MAX_IDLE_TIME"`

	// ConnectTimeout là thời gian tối đa cho một lần quay số. 0 → 10 giây.
	ConnectTimeout time.Duration `yaml:"connect_timeout" env:"DB_CONNECT_TIMEOUT"`

	// TLS bật kết nối mã hoá khi có khai cert hoặc CA. Không khai gì thì kết
	// nối chạy plaintext.
	//
	// Cấu hình TLS đi qua tlsx nên nó là *tls.Config thật, không phải chuỗi
	// sslmode: xác thực certificate do tlsx quyết định, giống hệt phía HTTP.
	TLS tlsx.Options `yaml:"tls"`

	// SlowThreshold là ngưỡng coi một câu query là chậm. 0 → 200ms.
	SlowThreshold time.Duration `yaml:"slow_threshold" env:"DB_SLOW_THRESHOLD"`

	// LogLevel là mức log của GORM: "silent", "error", "warn", "info".
	// Rỗng → "warn". Chỉ "info" mới log mọi câu query.
	LogLevel string `yaml:"log_level" env:"DB_LOG_LEVEL"`

	// LogSQLParams cho phép ghi **giá trị tham số** của câu query vào log.
	//
	// Mặc định false, và đó là mặc định đúng: tham số của query chính là dữ
	// liệu người dùng — số điện thoại, số tài khoản, email — và log tập trung
	// có nhiều người đọc được hơn database. Khi tắt, câu SQL vào log vẫn đủ
	// để biết query nào chậm vì nó giữ nguyên dạng có `?`.
	LogSQLParams bool `yaml:"log_sql_params" env:"DB_LOG_SQL_PARAMS"`

	// Logger dùng để ghi log của GORM. nil thì dùng slog.Default().
	Logger *slog.Logger `yaml:"-"`
}

// validate kiểm tra các field bắt buộc và các tổ hợp không dùng được.
func (c Config) validate() error {
	switch c.Driver {
	case Postgres, MySQL:
	case "":
		return fmt.Errorf("db: Config thiếu Driver (%q hoặc %q)", Postgres, MySQL)
	default:
		return fmt.Errorf("db: Driver %q không được hỗ trợ", c.Driver)
	}

	if c.Host == "" {
		return fmt.Errorf("db: Config thiếu Host")
	}
	if c.User == "" {
		return fmt.Errorf("db: Config thiếu User")
	}
	if c.Database == "" {
		return fmt.Errorf("db: Config thiếu Database")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("db: Port %d ngoài khoảng hợp lệ", c.Port)
	}
	if c.Schema != "" && c.Driver != Postgres {
		return fmt.Errorf("db: Schema chỉ dùng được với driver %q", Postgres)
	}
	if _, err := parseLogLevel(c.LogLevel); err != nil {
		return err
	}
	if c.TimeZone != "" {
		if _, err := time.LoadLocation(c.TimeZone); err != nil {
			return fmt.Errorf("db: TimeZone %q không hợp lệ: %w", c.TimeZone, err)
		}
	}
	return nil
}

// withDefaults trả về bản copy đã điền giá trị mặc định cho mọi field zero.
func (c Config) withDefaults() Config {
	if c.Port == 0 {
		if c.Driver == MySQL {
			c.Port = DefaultPortMySQL
		} else {
			c.Port = DefaultPortPostgres
		}
	}
	setIfZero(&c.MaxOpenConns, DefaultMaxOpenConns)
	setIfZero(&c.MaxIdleConns, DefaultMaxIdleConns)
	setIfZero(&c.ConnMaxLifetime, DefaultConnMaxLifetime)
	setIfZero(&c.ConnMaxIdleTime, DefaultConnMaxIdleTime)
	setIfZero(&c.ConnectTimeout, DefaultConnectTimeout)
	setIfZero(&c.SlowThreshold, DefaultSlowThreshold)
	if c.LogLevel == "" {
		c.LogLevel = "warn"
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// parseLogLevel đổi tên mức log thành logger.LogLevel của GORM.
//
// Rỗng cho ra Warn: mức đó log lỗi và query chậm mà không log mọi câu query,
// tức là đúng thứ cần trong production.
func parseLogLevel(s string) (logger.LogLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return logger.Warn, nil
	case "silent", "none", "off":
		return logger.Silent, nil
	case "error":
		return logger.Error, nil
	case "warn", "warning":
		return logger.Warn, nil
	case "info", "debug":
		return logger.Info, nil
	default:
		return 0, fmt.Errorf("db: LogLevel %q không hợp lệ (silent|error|warn|info)", s)
	}
}

// setIfZero điền v khi *p đang là giá trị zero.
func setIfZero[T comparable](p *T, v T) {
	var zero T
	if *p == zero {
		*p = v
	}
}
