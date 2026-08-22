package cache_test

import (
	"strings"
	"testing"

	"github.com/cqt002/gokit/cache"
	"github.com/cqt002/gokit/core/tlsx"
)

func TestNew_ThieuAddrs(t *testing.T) {
	_, err := cache.New(cache.Config{})
	if err == nil {
		t.Fatal("Config rỗng mà New không báo lỗi")
	}
	if !strings.Contains(err.Error(), "Addrs") {
		t.Errorf("lỗi không nhắc Addrs: %v", err)
	}
}

func TestNew_AddrRong(t *testing.T) {
	_, err := cache.New(cache.Config{Addrs: []string{"127.0.0.1:6379", ""}})
	if err == nil {
		t.Fatal("Addrs có phần tử rỗng mà không báo lỗi")
	}
}

// Redis Cluster chỉ có database 0. Bỏ qua DB trong im lặng nghĩa là service ghi
// vào database 0 trong khi cấu hình nói database 3.
func TestNew_DBKhacKhongVoiCluster(t *testing.T) {
	_, err := cache.New(cache.Config{
		Addrs: []string{"127.0.0.1:6379", "127.0.0.1:6380"},
		DB:    3,
	})
	if err == nil {
		t.Fatal("DB != 0 với nhiều địa chỉ mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "Cluster") {
		t.Errorf("lỗi không giải thích nguyên nhân: %v", err)
	}
}

func TestNew_DBKhacKhongVoiStandalone(t *testing.T) {
	c, err := cache.New(cache.Config{Addrs: []string{"127.0.0.1:6379"}, DB: 3})
	if err != nil {
		t.Fatalf("DB != 0 với một địa chỉ phải hợp lệ: %v", err)
	}
	_ = c.Close()
}

func TestNew_PoolSizeAm(t *testing.T) {
	_, err := cache.New(cache.Config{Addrs: []string{"127.0.0.1:6379"}, PoolSize: -1})
	if err == nil {
		t.Fatal("PoolSize âm mà không báo lỗi")
	}
}

func TestNew_TLSSai(t *testing.T) {
	_, err := cache.New(cache.Config{
		Addrs: []string{"127.0.0.1:6379"},
		TLS:   tlsx.Options{CertPEM: []byte("không phải PEM"), KeyPEM: []byte("cũng không")},
	})
	if err == nil {
		t.Fatal("cert rác mà New không báo lỗi")
	}
}

// Một địa chỉ → standalone, nhiều địa chỉ → cluster. Không có cờ nào để lệch.
func TestNew_ChonCheDoTheoSoDiaChi(t *testing.T) {
	one, err := cache.New(cache.Config{Addrs: []string{"127.0.0.1:6379"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer one.Close()

	many, err := cache.New(cache.Config{Addrs: []string{"127.0.0.1:6379", "127.0.0.1:6380"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer many.Close()

	if got := typeName(one); !strings.Contains(got, "*redis.Client") {
		t.Errorf("một địa chỉ cho ra %s, muốn *redis.Client", got)
	}
	if got := typeName(many); !strings.Contains(got, "*redis.ClusterClient") {
		t.Errorf("nhiều địa chỉ cho ra %s, muốn *redis.ClusterClient", got)
	}
}

func TestPing(t *testing.T) {
	c, mr := newClient(t)

	if err := c.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	mr.Close()
	if err := c.Ping(t.Context()); err == nil {
		t.Error("Ping thành công dù server đã tắt")
	}
}

func TestNewWithRedis(t *testing.T) {
	if _, err := cache.NewWithRedis(nil, nil); err == nil {
		t.Fatal("NewWithRedis(nil) mà không báo lỗi")
	}

	rdb, _ := newRedis(t)
	c, err := cache.NewWithRedis(rdb, nil)
	if err != nil {
		t.Fatalf("NewWithRedis: %v", err)
	}
	if c.Redis() != rdb {
		t.Error("Redis() không trả về client đã truyền vào")
	}
	if c.Logger() == nil {
		t.Error("Logger() nil dù đã rơi về slog.Default()")
	}
}
