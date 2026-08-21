package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/config"
	"github.com/cqt002/gokit/core/secret"
)

type appConfig struct {
	App struct {
		Name string `yaml:"name" env:"APP_NAME"`
		Env  string `yaml:"env"  env:"APP_ENV"`
	} `yaml:"app"`
	DB struct {
		Host     string        `yaml:"host"     env:"DB_HOST"`
		Port     int           `yaml:"port"     env:"DB_PORT"`
		Password secret.Secret `yaml:"password" env:"DB_PASSWORD"`
		Timeout  time.Duration `yaml:"timeout"  env:"DB_TIMEOUT"`
		Debug    bool          `yaml:"debug"    env:"DB_DEBUG"`
	} `yaml:"db"`
}

const yamlDayDu = `
app:
  name: dich-vu-a
  env: staging
db:
  host: db.local
  port: 5432
  password: mat-khau-tu-yaml
  timeout: 5s
  debug: true
`

func TestLoad_ChiYAML(t *testing.T) {
	cfg, err := config.LoadWith[appConfig]([]byte(yamlDayDu), config.MapLookuper(nil))
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	if cfg.App.Name != "dich-vu-a" || cfg.App.Env != "staging" {
		t.Errorf("App = %+v", cfg.App)
	}
	if cfg.DB.Host != "db.local" || cfg.DB.Port != 5432 {
		t.Errorf("DB = host %q port %d", cfg.DB.Host, cfg.DB.Port)
	}
	if cfg.DB.Password.Reveal() != "mat-khau-tu-yaml" {
		t.Errorf("Password = %q", cfg.DB.Password.Reveal())
	}
	if cfg.DB.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v", cfg.DB.Timeout)
	}
	if !cfg.DB.Debug {
		t.Error("Debug = false")
	}
}

// Thứ tự ưu tiên là hợp đồng chính của package: env > YAML > zero.
func TestLoad_EnvGhiDeYAML(t *testing.T) {
	cfg, err := config.LoadWith[appConfig]([]byte(yamlDayDu), config.MapLookuper(map[string]string{
		"DB_HOST":     "db.production",
		"DB_PORT":     "6432",
		"DB_PASSWORD": "mat-khau-tu-env",
	}))
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}

	if cfg.DB.Host != "db.production" {
		t.Errorf("Host = %q, env phải thắng YAML", cfg.DB.Host)
	}
	if cfg.DB.Port != 6432 {
		t.Errorf("Port = %d, env phải thắng YAML", cfg.DB.Port)
	}
	if cfg.DB.Password.Reveal() != "mat-khau-tu-env" {
		t.Errorf("Password = %q, env phải thắng YAML", cfg.DB.Password.Reveal())
	}

	// Field không có env tương ứng phải giữ giá trị từ YAML, không bị xoá về zero.
	if cfg.App.Name != "dich-vu-a" {
		t.Errorf("Name = %q, phải giữ giá trị YAML khi env không khai", cfg.App.Name)
	}
	if cfg.DB.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, phải giữ giá trị YAML", cfg.DB.Timeout)
	}
}

func TestLoad_ChiEnv(t *testing.T) {
	cfg, err := config.LoadWith[appConfig](nil, config.MapLookuper(map[string]string{
		"APP_NAME": "chi-env",
		"DB_PORT":  "1234",
	}))
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.App.Name != "chi-env" || cfg.DB.Port != 1234 {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.DB.Host != "" {
		t.Errorf("Host = %q, không khai ở đâu thì phải là zero", cfg.DB.Host)
	}
}

func TestLoad_YAMLRong(t *testing.T) {
	for _, in := range [][]byte{nil, {}, []byte("   \n\t\n  ")} {
		cfg, err := config.LoadWith[appConfig](in, config.MapLookuper(nil))
		if err != nil {
			t.Fatalf("LoadWith(%q): %v", in, err)
		}
		if cfg.App.Name != "" {
			t.Errorf("cfg = %+v, muốn toàn zero", cfg)
		}
	}
}

// Một key gõ sai bị im lặng bỏ qua là kiểu sự cố tốn nhiều giờ nhất của mọi config
// loader: cấu hình trông như đã khai mà thực tế đang chạy giá trị mặc định.
func TestLoad_KeyLaLaLoi(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"key lạ ở gốc", "khong_ton_tai: 1\napp:\n  name: a\n"},
		{"key lạ trong nhánh con", "db:\n  hostt: db.local\n"},
		{"gõ sai tên field", "app:\n  nane: a\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.LoadWith[appConfig]([]byte(tt.yaml), config.MapLookuper(nil))
			if err == nil {
				t.Fatal("muốn lỗi, không có lỗi")
			}
			if !strings.Contains(err.Error(), "YAML") {
				t.Errorf("lỗi %v không nói rõ đang đọc YAML", err)
			}
		})
	}
}

// Lỗi phải nói rõ field nào sai, nếu không thì người khai config không biết sửa ở đâu.
func TestLoad_KieuSaiThiLoiRoRang(t *testing.T) {
	// yaml.v3 báo số dòng chứ không báo tên field; số dòng đủ để tìm ra chỗ sai.
	t.Run("từ YAML", func(t *testing.T) {
		_, err := config.LoadWith[appConfig]([]byte("db:\n  port: khong-phai-so\n"), config.MapLookuper(nil))
		if err == nil {
			t.Fatal("muốn lỗi")
		}
		if !strings.Contains(err.Error(), "line 2") {
			t.Errorf("lỗi %q không chỉ ra dòng sai", err)
		}
		if !strings.Contains(err.Error(), "int") {
			t.Errorf("lỗi %q không nói kiểu mong đợi", err)
		}
	})

	// envconfig báo đường dẫn field ("DB: Port") chứ không báo tên biến.
	t.Run("từ env", func(t *testing.T) {
		_, err := config.LoadWith[appConfig](nil, config.MapLookuper(map[string]string{
			"DB_PORT": "khong-phai-so",
		}))
		if err == nil {
			t.Fatal("muốn lỗi")
		}
		if !strings.Contains(err.Error(), "Port") {
			t.Errorf("lỗi %q không chỉ ra field nào sai", err)
		}
		if !strings.Contains(err.Error(), "khong-phai-so") {
			t.Errorf("lỗi %q không nhắc lại giá trị sai", err)
		}
	})

	t.Run("duration sai", func(t *testing.T) {
		_, err := config.LoadWith[appConfig](nil, config.MapLookuper(map[string]string{
			"DB_TIMEOUT": "5 ngày",
		}))
		if err == nil {
			t.Fatal("muốn lỗi")
		}
	})
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yamlDayDu), 0o600); err != nil {
		t.Fatalf("ghi file: %v", err)
	}

	cfg, err := config.LoadFile[appConfig](path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.App.Name != "dich-vu-a" {
		t.Errorf("Name = %q", cfg.App.Name)
	}
}

func TestLoadFile_KhongTonTai(t *testing.T) {
	_, err := config.LoadFile[appConfig](filepath.Join(t.TempDir(), "khong-co.yaml"))
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if !strings.Contains(err.Error(), "khong-co.yaml") {
		t.Errorf("lỗi %q không nhắc tới đường dẫn", err)
	}
}

// Load đọc biến môi trường thật của process. Dùng t.Setenv nên test này không
// chạy song song được — đó chính là lý do LoadWith tồn tại.
func TestLoad_DocEnvThat(t *testing.T) {
	t.Setenv("APP_NAME", "tu-env-that")

	cfg, err := config.Load[appConfig]([]byte(yamlDayDu))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Name != "tu-env-that" {
		t.Errorf("Name = %q, muốn giá trị từ biến môi trường thật", cfg.App.Name)
	}
}

// secret.Secret phải nhận được giá trị từ cả hai nguồn, và không lộ khi in ra.
func TestSecret_NhanGiaTriVaKhongLo(t *testing.T) {
	cfg, err := config.LoadWith[appConfig]([]byte(yamlDayDu), config.MapLookuper(nil))
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.DB.Password.Reveal() != "mat-khau-tu-yaml" {
		t.Fatalf("Password = %q", cfg.DB.Password.Reveal())
	}
	if got := cfg.DB.Password.String(); got != secret.Redacted {
		t.Errorf("String() = %q, muốn %q", got, secret.Redacted)
	}
}

// Thứ tự đầy đủ: env > YAML > default trong tag > zero.
//
// Điểm cần khoá lại là `default=` KHÔNG ghi đè YAML, dù LoadWith đã bật
// DefaultOverwrite: envconfig chỉ ghi đè khi biến thực sự có mặt trong lookuper,
// còn default thì chỉ điền vào field còn là zero. Nếu hành vi này đổi thì godoc
// của package sai, nên test này canh nó.
func TestThuTuUuTien_YAMLThangDefaultTrongTag(t *testing.T) {
	type coDefault struct {
		Host string `yaml:"host" env:"X_HOST,default=tu-default"`
		Port int    `yaml:"port" env:"X_PORT,default=8080"`
	}

	cfg, err := config.LoadWith[coDefault]([]byte("host: tu-yaml\n"), config.MapLookuper(nil))
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg.Host != "tu-yaml" {
		t.Errorf("Host = %q, YAML phải thắng default trong tag", cfg.Host)
	}
	// Field YAML không khai thì mới nhận default.
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, muốn 8080 từ default", cfg.Port)
	}

	// Và env thắng cả hai.
	cfg2, err := config.LoadWith[coDefault]([]byte("host: tu-yaml\n"),
		config.MapLookuper(map[string]string{"X_HOST": "tu-env"}))
	if err != nil {
		t.Fatalf("LoadWith: %v", err)
	}
	if cfg2.Host != "tu-env" {
		t.Errorf("Host = %q, env phải thắng cả YAML và default", cfg2.Host)
	}
}

func TestMapLookuper_KhongCanBienMoiTruongThat(t *testing.T) {
	// Cùng một biến, hai giá trị khác nhau, chạy tuần tự mà không ảnh hưởng nhau —
	// điều không làm được nếu phải os.Setenv.
	for _, want := range []string{"a", "b"} {
		cfg, err := config.LoadWith[appConfig](nil, config.MapLookuper(map[string]string{"APP_NAME": want}))
		if err != nil {
			t.Fatalf("LoadWith: %v", err)
		}
		if cfg.App.Name != want {
			t.Errorf("Name = %q, muốn %q", cfg.App.Name, want)
		}
	}
}
