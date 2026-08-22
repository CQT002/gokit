module github.com/cqt002/gokit/httpx

go 1.25.0

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

require (
	github.com/cqt002/gokit/core v0.0.0
	github.com/go-playground/validator/v10 v10.30.3
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/prometheus/client_golang v1.24.1
	golang.org/x/time v0.15.0
)

// core chưa publish nên module proxy không có bản nào để tải. replace trỏ vào cây
// nguồn trong repo để `GOWORK=off` build được. Bỏ replace và điền version thật ở
// Phase 5, sau khi tag core/v0.1.0.
//
// Không có obs ở đây: httpx không import obs. obs.HTTPMetrics tự là một middleware
// nên app ghép nó vào chain, httpx không cần biết obs tồn tại.
replace github.com/cqt002/gokit/core => ../core
