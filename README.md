# gokit

Bộ thư viện dùng chung cho các service Go: logging, config, error, HTTP, database, cache, queue.

Module path: `github.com/cqt002/gokit` — **luôn viết thường**. Go coi `CQT002` và `cqt002`
là hai module khác nhau; lẫn cả hai vào một build graph sinh ra lỗi kiểu `type X is not X`.
CI có bước chặn việc này (`make check-module-path`).

> **Trạng thái:** Phase 0 xong (bộ khung repo, CI). Chưa có code chức năng.
> Xem [plan-code.md](plan-code.md) để biết kiến trúc và lộ trình.

## Cấu trúc

Multi-module — mỗi thành phần là một module riêng để service chỉ kéo về đúng thứ nó cần:

| Module | Nội dung |
|---|---|
| [core](core/) | log, errs, config, tracectx, secret, crypto, retry, tlsx |
| [obs](obs/) | Prometheus metrics |
| [httpx](httpx/) | middleware, server, client, auth, idempotency, health |
| [db](db/) | GORM (Postgres/MySQL), base entity, query builder, migrate |
| [cache](cache/) | Redis, distributed lock, leader election, cron |
| [queue/kafka](queue/kafka/) | producer/consumer trên franz-go |
| [testx](testx/) | helper cho test (testcontainers, log capture) |
| [examples](examples/) | service mẫu — nơi kiểm chứng thiết kế |

Phụ thuộc một chiều: `core` và `obs` không phụ thuộc gì trong repo; `httpx`, `db`,
`cache`, `queue/kafka` phụ thuộc `core` + `obs`.

`queue/` là thư mục thường, không phải module — nó chỉ là chỗ đứng cho driver thứ hai
sau này (`queue/rabbitmq`, `queue/sqs`) mà không bắt người dùng driver đó kéo theo
dependency của Kafka. Không có interface chung phủ các driver: mô hình của chúng khác
nhau ở đúng chỗ quan trọng nhất.

## Cài đặt

Mỗi module cài riêng, và **tag có prefix theo tên module**:

```sh
go get github.com/cqt002/gokit/core@core/v0.1.0
go get github.com/cqt002/gokit/httpx@httpx/v0.1.0
go get github.com/cqt002/gokit/queue/kafka@queue/kafka/v0.1.0
```

Yêu cầu Go 1.25 trở lên.

## Phát triển

`go.work` được commit để dev local không cần `replace`. Nhưng workspace **che** lỗi
version giữa các module, nên CI có một job build/test với `GOWORK=off` — đúng như
cách người ngoài build.

```sh
make            # liệt kê target
make test       # unit test + race, mọi module (không cần Docker)
make lint       # golangci-lint (tự tải đúng phiên bản nếu máy chưa có)
make fmt        # gofmt + sắp lại import
make check      # module path + vet + lint + test
```

Cần Docker:

```sh
make test-integration   # test có build tag `integration`
```

Vì mỗi module có `go.mod` riêng, `go test ./...` ở gốc repo **không** chạm tới module
con — luôn dùng target trong `Makefile` hoặc `cd` vào từng module.
