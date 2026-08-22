package db

import (
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"

	"github.com/cqt002/gokit/obs"
)

// RegisterMetrics đăng ký metric của connection pool vào registry.
//
// name phân biệt khi service nối tới nhiều database, ví dụ "primary" và
// "replica". Đăng ký hai lần cùng một name vào cùng registry là lỗi.
//
// Xem godoc của obs.RegisterDBStats để biết danh sách metric. Nhóm quan trọng
// nhất là db_pool_waits_total: nó tăng nghĩa là goroutine đang xếp hàng chờ
// connection, và đó là nguyên nhân của phần lớn các vụ "service chậm mà CPU
// thấp".
func RegisterMetrics(reg *prometheus.Registry, name string, gdb *gorm.DB) error {
	stats, err := Stats(gdb)
	if err != nil {
		return err
	}
	return obs.RegisterDBStats(reg, name, stats)
}
