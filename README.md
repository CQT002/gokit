# gokit

Bộ thư viện dùng chung cho các service Go: logging, config, error, HTTP, database, cache, queue.

Module path: `github.com/cqt002/gokit`

> **Trạng thái:** đang thiết kế, chưa có code. Xem [plan-code.md](plan-code.md) để biết kiến trúc và lộ trình.

## Cấu trúc

Multi-module — mỗi thành phần là một module riêng để service chỉ kéo về đúng thứ nó cần:

| Module | Nội dung |
|---|---|
| `core` | log, errs, config, tracectx, secret, crypto, retry, tlsx |
| `obs` | Prometheus metrics |
| `httpx` | middleware, server, client, auth, idempotency, health |
| `db` | GORM (Postgres/MySQL), base entity, query builder, migrate |
| `cache` | Redis, distributed lock, leader election, cron |
| `kafka` | producer/consumer trên franz-go |
| `testx` | helper cho test (testcontainers, log capture) |
