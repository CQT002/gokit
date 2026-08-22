module github.com/cqt002/gokit/db

go 1.25.0

require (
	github.com/cqt002/gokit/core v0.0.0
	github.com/cqt002/gokit/obs v0.0.0

	// Chỉ dùng trong test: driver SQLite thuần Go, để test migrate chạy trên
	// một database thật mà không cần Docker. go mod tidy không phân biệt được
	// dep của test với dep thường nên nó nằm ở đây; không package nào ngoài
	// _test.go import nó.
	github.com/glebarez/sqlite v1.11.0
	github.com/go-sql-driver/mysql v1.8.1
	github.com/jackc/pgx/v5 v5.10.0
	github.com/prometheus/client_golang v1.24.1
	gorm.io/driver/mysql v1.6.0
	gorm.io/driver/postgres v1.6.2
	gorm.io/gorm v1.31.2
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.22.5 // indirect
	modernc.org/mathutil v1.5.0 // indirect
	modernc.org/memory v1.5.0 // indirect
	modernc.org/sqlite v1.23.1 // indirect
)

// core và obs chưa publish nên module proxy không có bản nào để tải. replace trỏ
// vào cây nguồn trong repo để `GOWORK=off` build được. Bỏ replace và điền version
// thật ở Phase 5, sau khi tag core/v0.1.0 và obs/v0.1.0.
replace (
	github.com/cqt002/gokit/core => ../core
	github.com/cqt002/gokit/obs => ../obs
)
