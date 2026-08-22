module github.com/cqt002/gokit/cache

go 1.25.0

require (
	// Chỉ dùng trong test: server Redis thuần Go. Nhờ nó test đơn vị kiểm được
	// hành vi thật (TTL, SET NX, script Lua) mà không cần Docker. go mod tidy
	// không phân biệt dep của test với dep thường nên nó nằm ở đây; không
	// package nào ngoài _test.go import nó.
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/bsm/redislock v0.10.0
	github.com/cqt002/gokit/core v0.0.0
	github.com/cqt002/gokit/httpx v0.0.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/robfig/cron/v3 v3.0.1
	golang.org/x/sync v0.22.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

// core và httpx chưa publish nên module proxy không có bản nào để tải. replace
// trỏ vào cây nguồn trong repo để `GOWORK=off` build được. Bỏ replace và điền
// version thật ở Phase 5.
//
// httpx ở đây chỉ vì cache/idemstore cài đặt interface httpx/idempotency.Store.
// Chiều phụ thuộc là cache → httpx, không có chiều ngược lại: đó là lý do
// interface nằm ở httpx còn implementation Redis nằm ở đây.
replace (
	github.com/cqt002/gokit/core => ../core
	github.com/cqt002/gokit/httpx => ../httpx
)
