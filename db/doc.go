// Package db cung cấp glue cho GORM: mở kết nối có pool và TLS đúng cách, một
// logger nối GORM vào slog, và metric cho connection pool.
//
// Các package con:
//
//   - [github.com/cqt002/gokit/db/model]   base entity ghép được + plugin điền audit
//   - [github.com/cqt002/gokit/db/query]   filter/sort/phân trang có whitelist cột
//   - [github.com/cqt002/gokit/db/migrate] chạy migration có ghi lịch sử và khoá
//
// Package này **không** bọc *gorm.DB trong interface riêng. GORM đã là lớp
// abstract driver rồi; thêm một interface nữa chỉ làm mất tính năng chứ không
// đổi được implementation.
package db
