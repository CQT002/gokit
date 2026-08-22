package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cqt002/gokit/core/secret"
	"github.com/cqt002/gokit/core/tlsx"
	"github.com/cqt002/gokit/obs"
)

func TestOpen_ConfigSaiThiBaoLoiNgay(t *testing.T) {
	_, err := Open(context.Background(), Config{})
	if err == nil {
		t.Fatal("Config rỗng mà Open không báo lỗi")
	}
	if !strings.Contains(err.Error(), "Driver") {
		t.Errorf("lỗi không nhắc Driver: %v", err)
	}
}

// Open phải ping trước khi trả về: sai host là lỗi lúc khởi động, không phải lỗi
// 500 ở request đầu tiên.
func TestOpen_PingThatBai(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := Config{
		Driver:   Postgres,
		Host:     "127.0.0.1",
		Port:     1, // không có gì lắng nghe ở đây
		User:     "app",
		Database: "app",
	}
	_, err := Open(ctx, cfg)
	if err == nil {
		t.Fatal("Open thành công dù không có database nào")
	}
	if !strings.Contains(err.Error(), "không kết nối được") {
		t.Errorf("lỗi không nói rõ là lỗi kết nối: %v", err)
	}
}

// Mật khẩu có ký tự đặc biệt phải đi qua được: đây là chỗ hỏng khi DSN được nối
// bằng tay.
func TestOpenSQL_MatKhauKyTuDacBiet(t *testing.T) {
	for _, driver := range []Driver{Postgres, MySQL} {
		cfg := Config{
			Driver:   driver,
			Host:     "127.0.0.1",
			User:     "app user",
			Password: secret.Secret(`p@ss w/ord:'"#?&`),
			Database: "app",
		}
		sqlDB, err := openSQL(cfg.withDefaults())
		if err != nil {
			t.Errorf("%s: openSQL lỗi: %v", driver, err)
			continue
		}
		_ = sqlDB.Close()
	}
}

func TestOpenSQL_TLSSai(t *testing.T) {
	cfg := validConfig().withDefaults()
	cfg.TLS = tlsx.Options{CertPEM: []byte("không phải PEM"), KeyPEM: []byte("cũng không")}

	if _, err := openSQL(cfg); err == nil {
		t.Fatal("cert rác mà openSQL không báo lỗi")
	}
}

func TestHasTLS(t *testing.T) {
	tests := []struct {
		name string
		opts tlsx.Options
		want bool
	}{
		{"rỗng", tlsx.Options{}, false},
		{"chỉ ServerName", tlsx.Options{ServerName: "db.local"}, false},
		{"chỉ MinVersion", tlsx.Options{MinVersion: 0x0304}, false},
		{"có CA file", tlsx.Options{RootCAFile: "/tmp/ca.pem"}, true},
		{"có cert PEM", tlsx.Options{CertPEM: []byte("x")}, true},
		{"InsecureSkipVerify", tlsx.Options{InsecureSkipVerify: true}, true},
	}
	for _, tc := range tests {
		if got := hasTLS(tc.opts); got != tc.want {
			t.Errorf("hasTLS(%s) = %v, muốn %v", tc.name, got, tc.want)
		}
	}
}

func TestTZOffset(t *testing.T) {
	tests := map[string]string{
		"UTC":                 "+00:00",
		"Asia/Ho_Chi_Minh":    "+07:00",
		"Asia/Kolkata":        "+05:30",
		"America/Los_Angeles": "-0", // chỉ kiểm tra dấu
	}
	for name, want := range tests {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Skipf("máy không có tzdata cho %s", name)
		}
		if got := tzOffset(loc); !strings.HasPrefix(got, want) {
			t.Errorf("tzOffset(%s) = %q, muốn tiền tố %q", name, got, want)
		}
	}
}

func TestCloseVaStats(t *testing.T) {
	gdb := newDryRunDB(t, Postgres)

	stats, err := Stats(gdb)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats().MaxOpenConnections < 0 {
		t.Error("Stats trả về số liệu vô nghĩa")
	}
	if err := Close(gdb); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRegisterMetrics(t *testing.T) {
	gdb := newDryRunDB(t, Postgres)
	t.Cleanup(func() { _ = Close(gdb) })

	reg := obs.NewRegistry()
	if err := RegisterMetrics(reg, "primary", gdb); err != nil {
		t.Fatalf("RegisterMetrics: %v", err)
	}

	// Đăng ký hai database khác name vào cùng registry phải được — đó là lý do
	// obs đưa name vào constant label.
	gdb2 := newDryRunDB(t, MySQL)
	t.Cleanup(func() { _ = Close(gdb2) })
	if err := RegisterMetrics(reg, "replica", gdb2); err != nil {
		t.Fatalf("RegisterMetrics lần hai: %v", err)
	}

	if err := RegisterMetrics(reg, "primary", gdb); err == nil {
		t.Error("đăng ký trùng name mà không báo lỗi")
	}

	names, err := metricNames(reg)
	if err != nil {
		t.Fatalf("thu thập metric: %v", err)
	}
	if !names["db_pool_connections"] {
		t.Errorf("thiếu metric db_pool_connections, có: %v", names)
	}
}

// metricNames thu thập metric trong registry và trả về tập tên.
func metricNames(reg *prometheus.Registry) (map[string]bool, error) {
	mfs, err := reg.Gather()
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = true
	}
	return out, nil
}
