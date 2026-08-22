package db

import (
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

func validConfig() Config {
	return Config{
		Driver:   Postgres,
		Host:     "127.0.0.1",
		User:     "app",
		Database: "app",
	}
}

func TestConfigValidate_HopLe(t *testing.T) {
	if err := validConfig().validate(); err != nil {
		t.Fatalf("config hợp lệ mà báo lỗi: %v", err)
	}
}

func TestConfigValidate_ThieuField(t *testing.T) {
	tests := map[string]func(*Config){
		"Driver":   func(c *Config) { c.Driver = "" },
		"Host":     func(c *Config) { c.Host = "" },
		"User":     func(c *Config) { c.User = "" },
		"Database": func(c *Config) { c.Database = "" },
	}
	for field, mutate := range tests {
		t.Run(field, func(t *testing.T) {
			cfg := validConfig()
			mutate(&cfg)

			err := cfg.validate()
			if err == nil {
				t.Fatalf("thiếu %s mà không báo lỗi", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("lỗi không nhắc tên field %s: %v", field, err)
			}
		})
	}
}

func TestConfigValidate_DriverLa(t *testing.T) {
	cfg := validConfig()
	cfg.Driver = "oracle"

	if err := cfg.validate(); err == nil {
		t.Fatal("driver không hỗ trợ mà không báo lỗi")
	}
}

func TestConfigValidate_PortNgoaiKhoang(t *testing.T) {
	for _, port := range []int{-1, 70000} {
		cfg := validConfig()
		cfg.Port = port

		if err := cfg.validate(); err == nil {
			t.Errorf("port %d mà không báo lỗi", port)
		}
	}
}

// Schema là khái niệm của Postgres. Với MySQL nó bị bỏ qua, và một giá trị bị
// bỏ qua trong im lặng là thứ phải báo lỗi.
func TestConfigValidate_SchemaVoiMySQL(t *testing.T) {
	cfg := validConfig()
	cfg.Driver = MySQL
	cfg.Schema = "app"

	err := cfg.validate()
	if err == nil {
		t.Fatal("Schema với MySQL mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "Schema") {
		t.Errorf("lỗi không nhắc Schema: %v", err)
	}
}

func TestConfigValidate_TimeZoneSai(t *testing.T) {
	cfg := validConfig()
	cfg.TimeZone = "Mars/Olympus"

	if err := cfg.validate(); err == nil {
		t.Fatal("timezone không tồn tại mà không báo lỗi")
	}
}

func TestConfigValidate_LogLevelSai(t *testing.T) {
	cfg := validConfig()
	cfg.LogLevel = "verbose"

	if err := cfg.validate(); err == nil {
		t.Fatal("log level lạ mà không báo lỗi")
	}
}

func TestConfigWithDefaults(t *testing.T) {
	got := validConfig().withDefaults()

	if got.Port != DefaultPortPostgres {
		t.Errorf("Port = %d, muốn %d", got.Port, DefaultPortPostgres)
	}
	if got.MaxOpenConns != DefaultMaxOpenConns {
		t.Errorf("MaxOpenConns = %d, muốn %d", got.MaxOpenConns, DefaultMaxOpenConns)
	}
	if got.MaxIdleConns != DefaultMaxIdleConns {
		t.Errorf("MaxIdleConns = %d, muốn %d", got.MaxIdleConns, DefaultMaxIdleConns)
	}
	if got.ConnMaxLifetime != DefaultConnMaxLifetime {
		t.Errorf("ConnMaxLifetime = %v, muốn %v", got.ConnMaxLifetime, DefaultConnMaxLifetime)
	}
	if got.ConnMaxIdleTime != DefaultConnMaxIdleTime {
		t.Errorf("ConnMaxIdleTime = %v, muốn %v", got.ConnMaxIdleTime, DefaultConnMaxIdleTime)
	}
	if got.ConnectTimeout != DefaultConnectTimeout {
		t.Errorf("ConnectTimeout = %v, muốn %v", got.ConnectTimeout, DefaultConnectTimeout)
	}
	if got.SlowThreshold != DefaultSlowThreshold {
		t.Errorf("SlowThreshold = %v, muốn %v", got.SlowThreshold, DefaultSlowThreshold)
	}
	if got.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, muốn %q", got.LogLevel, "warn")
	}
	if got.Logger == nil {
		t.Error("Logger vẫn nil sau withDefaults")
	}
}

func TestConfigWithDefaults_PortTheoDriver(t *testing.T) {
	cfg := validConfig()
	cfg.Driver = MySQL

	if got := cfg.withDefaults().Port; got != DefaultPortMySQL {
		t.Errorf("Port = %d, muốn %d", got, DefaultPortMySQL)
	}
}

func TestConfigWithDefaults_KhongGhiDeGiaTriDaKhai(t *testing.T) {
	cfg := validConfig()
	cfg.Port = 6543
	cfg.MaxOpenConns = 3
	cfg.SlowThreshold = time.Second
	cfg.LogLevel = "info"

	got := cfg.withDefaults()
	if got.Port != 6543 || got.MaxOpenConns != 3 ||
		got.SlowThreshold != time.Second || got.LogLevel != "info" {
		t.Errorf("withDefaults ghi đè giá trị đã khai: %+v", got)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		in   string
		want logger.LogLevel
	}{
		{"", logger.Warn},
		{"silent", logger.Silent},
		{"off", logger.Silent},
		{"error", logger.Error},
		{"warn", logger.Warn},
		{"warning", logger.Warn},
		{"info", logger.Info},
		{"debug", logger.Info},
		// Giá trị từ YAML hay env thường mang theo khoảng trắng và chữ hoa.
		{" INFO ", logger.Info},
	}
	for _, tc := range tests {
		in, want := tc.in, tc.want
		got, err := parseLogLevel(in)
		if err != nil {
			t.Errorf("parseLogLevel(%q) lỗi: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseLogLevel(%q) = %v, muốn %v", in, got, want)
		}
	}
}
