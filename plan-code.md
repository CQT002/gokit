# gokit — Plan phát triển

> Module: `github.com/cqt002/gokit`
> Cập nhật: 2026-08-20

---

## 0. Bảng theo dõi tiến độ

Ký hiệu: `⬜` chưa làm · `🔄` đang làm · `✅` xong

| Phase | Nội dung | Trạng thái |
|---|---|---|
| 0 | Bộ khung repo, go.mod, go.work, CI | ✅ |
| 1 | `core` — log/errs/config/trace/crypto... | ✅ |
| 2 | `obs` + `httpx` — middleware, server, client | ✅ |
| 3 | `db` + `cache` + `testx` | ⬜ |
| 4 | `queue/kafka` | ⬜ |
| 5 | Hoàn thiện, godoc, tag version | ⬜ |

Chi tiết từng hạng mục ở [mục 5](#5-lộ-trình-implement). Làm xong hạng mục nào thì tick checkbox ở đó và cập nhật bảng này.

---

## 1. Bối cảnh & mục tiêu

Xây một bộ thư viện dùng chung cho các service Go, lấy **danh sách tính năng** của một bộ lib nội bộ đã có làm tham chiếu, nhưng **viết lại hoàn toàn từ đặc tả** — không copy code, không mang theo bất kỳ thành phần nghiệp vụ nào của hệ thống cũ.

Nguyên tắc thiết kế:

1. **Mỏng và có quan điểm** — cung cấp default hợp lý + phần glue, không cố abstract mọi driver.
2. **Không khoá người dùng vào framework** — middleware theo chuẩn `net/http`, không phụ thuộc router.
3. **Không có global mutable state** — mọi thứ inject tường minh, test được.
4. **Trả về type stdlib khi có thể** — `*slog.Logger` thay vì interface tự định nghĩa.
5. **Multi-module** — service chỉ cần logger thì không phải kéo về Kafka + GORM + Redis.
6. **An toàn mặc định** — luật bảo vệ (mask, giới hạn kích thước, whitelist cột) phải hoạt động khi không khai gì; khai thêm chỉ để tinh chỉnh.

---

## 2. Quyết định đã chốt

| Hạng mục | Quyết định | Lý do |
|---|---|---|
| Module path | `github.com/cqt002/gokit` | Chữ thường toàn bộ. **Không bao giờ** viết dạng `CQT002` — Go sẽ coi là 2 module khác nhau |
| Cấu trúc | Multi-module (8 module trong 1 repo) | Cách ly dependency nặng |
| HTTP | `net/http` thuần, **không phụ thuộc router** | Middleware `func(http.Handler) http.Handler` dùng được ở mọi router; Echo tiêu thụ được stdlib chứ không ngược lại |
| Kafka | `twmb/franz-go` | Pure Go, bảo trì tích cực, tự xử lý reconnect/metadata, có `kprom` cho metrics sẵn |
| Vị trí module Kafka | `queue/kafka`, **không** phải `kafka` | Module path là API công khai: sau khi tag `kafka/v0.1.0` thì đổi path là breaking change cho mọi importer, còn path cũ vẫn nằm mãi trong proxy. Driver thứ hai (RabbitMQ/SQS/NATS) mang cây dependency riêng và API khác hẳn, không thể nằm chung module với Kafka |
| `queue/` là gì | **Thư mục thường**, không phải module | Nếu `queue` là module rồi `queue/kafka` là subpackage thì franz-go lây sang mọi người import `queue` — phá đúng lý do chọn multi-module |
| Interface chung cho queue | **Không làm** (xem mục 7) | Kafka có partition/offset/consumer group/commit tay; RabbitMQ có exchange/ack/prefetch; SQS có visibility timeout. Mẫu số chung `Publish(topic, msg)` bỏ mất đúng những thứ khiến người ta chọn Kafka |
| `db` và `cache` giữ phẳng | Không đổi thành `db/gorm`, `cache/redis` | GORM **đã là** lớp abstract driver (postgres/mysql trong cùng module). Còn `cache/lock`, `cache/leader`, `cache/cron` bản chất là redis-specific (redislock, `SET NX`) — thêm một tầng thư mục không mở ra khả năng nào thật |
| Config | `yaml.v3` + `sethvargo/go-envconfig` | 2 deps thay vì 13 của viper; không global state; env override grep được |
| Observability | Prometheus metrics + trace ID chuẩn W3C (chưa cài OTel SDK) | Metrics rẻ và giá trị cao; format trace ID là thứ gần như không đổi được về sau |
| Masking log | **3 lớp**: chặn theo kích thước → tag `log:` trên struct → danh sách tên field (fallback) | Luật theo kích thước an toàn mặc định; tag đi cùng field nên không lệch pha |
| Log body | **Luôn bật**, cả request và response, mọi status code | Yêu cầu vận hành: phải tra được mọi request khi cần |
| Oracle / SQL Server | Không hỗ trợ | Tránh hoàn toàn rắc rối CGO của `godror` |
| DI container | Không làm | Wiring bằng constructor trong `main.go` rõ ràng hơn |
| Logger | `log/slog` stdlib | Không tự định nghĩa `LoggerInterface` |

---

## 3. Cấu trúc repo

```
gokit/
├── go.work                    # dev local; CI phải có 1 job chạy GOWORK=off
├── .golangci.yml
├── Makefile
├── plan-code.md
│
├── core/          go.mod → github.com/cqt002/gokit/core
│   ├── idx/                   # UUID v4/v7, trace ID, span ID
│   ├── tracectx/              # W3C traceparent: parse/format/propagate
│   ├── ctxmeta/               # request-scoped metadata (request_id, user_id...)
│   ├── errs/                  # error có code + HTTP status + Unwrap
│   ├── log/                   # slog + masking 3 lớp
│   ├── config/                # Load[T] từ YAML + env overlay
│   ├── secret/                # type Secret — không lộ khi log/marshal
│   ├── crypto/                # AES-GCM, HMAC, argon2id
│   ├── retry/                 # backoff + jitter dùng chung
│   ├── tlsx/                  # tls.Config từ file / base64 / bytes
│   ├── timex/                 # layout + helper tối thiểu
│   └── deps: google/uuid, yaml.v3, go-envconfig, x/crypto
│
├── obs/           go.mod → .../obs
│   └── deps: prometheus/client_golang  (KHÔNG phụ thuộc core)
│
├── httpx/         go.mod → .../httpx
│   ├── middleware/            # trace, bodylog, recover, timeout, cors, ratelimit
│   ├── auth/                  # JWT, API key, basic
│   ├── client/                # HTTP client: retry, circuit breaker, propagation
│   ├── idempotency/           # Idempotency-Key: chống tạo trùng khi client retry
│   ├── health/                # /healthz, /readyz
│   ├── validate/
│   └── deps: core, obs, validator/v10, golang-jwt/v5
│
├── db/            go.mod → .../db
│   ├── (root)                 # Open, Config, gorm slog logger
│   ├── model/                 # base entity: Timestamps, Audit, SoftDelete, UUIDKey
│   ├── query/                 # Paginate, ApplyFilters (có whitelist cột)
│   ├── migrate/
│   └── deps: core, obs, gorm + driver postgres/mysql
│
├── cache/         go.mod → .../cache
│   ├── (root)                 # Client trên redis.UniversalClient
│   ├── lock/                  # distributed lock, auto-renew
│   ├── leader/                # leader election
│   ├── cron/                  # distributed cron (leader + scheduler)
│   ├── idemstore/             # store Redis cho httpx/idempotency
│   └── deps: core, obs, go-redis/v9, redislock
│
├── queue/                     # thư mục thường, KHÔNG có go.mod ở đây
│   └── kafka/     go.mod → .../queue/kafka
│       └── deps: core, obs, franz-go, franz-go/plugin/kprom
│
├── testx/         go.mod → .../testx
│   └── deps: testcontainers-go, testify
│
└── examples/
    └── api/                   # service mẫu — nơi kiểm chứng thiết kế
```

**Đồ thị phụ thuộc** (một chiều, không có cycle):

```
core ← httpx       ← ─┐
core ← db          ← ─┤
core ← cache       ← ─├─ examples/api
core ← queue/kafka ← ─┘
obs  ← {httpx, db, cache, queue/kafka}
httpx/idempotency ← cache/idemstore     (interface ở httpx, impl ở cache)
testx ← (chỉ dùng trong test)
```

---

## 4. Thiết kế từng module

### 4.1 `core/idx`

```go
func NewUUIDv4() string
func NewUUIDv7() string      // sortable theo thời gian — ưu tiên làm khoá chính
func NewTraceID() string     // 32 ký tự hex (chuẩn W3C)
func NewSpanID() string      // 16 ký tự hex
func RandomString(n int) string
func RandomDigits(n int) string
```

Dùng `crypto/rand`, **không** dùng `math/rand`.

### 4.2 `core/tracectx` — nền tảng cho observability

```go
type SpanContext struct {
    TraceID string   // 32 hex
    SpanID  string   // 16 hex
    Sampled bool
}

func NewRoot() SpanContext
func (sc SpanContext) NewChild() SpanContext
func (sc SpanContext) Traceparent() string          // "00-<trace>-<span>-01"
func ParseTraceparent(v string) (SpanContext, error)

func WithSpanContext(ctx context.Context, sc SpanContext) context.Context
func FromContext(ctx context.Context) (SpanContext, bool)
```

Đây là phần trả cổ tức về sau: cùng một cơ chế propagate dùng cho **cả HTTP header và Kafka header**. Khi nào cần OTel thật thì thay `FromContext` bằng bản của OTel, không phải sửa call site.

### 4.3 `core/ctxmeta`

```go
type Meta struct {
    RequestID     string
    CorrelationID string
    UserID        string
    UserType      string
}

func With(ctx context.Context, m Meta) context.Context
func From(ctx context.Context) Meta
func WithRequestID(ctx context.Context, id string) context.Context
// ... accessor cho từng field
```

Một context key duy nhất chứa struct → 1 lần lookup thay vì 5.

### 4.4 `core/errs`

```go
type Code string

const (
    CodeBadRequest   Code = "bad_request"
    CodeValidation   Code = "validation_failed"
    CodeUnauthorized Code = "unauthorized"
    CodeForbidden    Code = "forbidden"
    CodeNotFound     Code = "not_found"
    CodeConflict     Code = "conflict"
    CodeTooManyReq   Code = "too_many_requests"
    CodeInternal     Code = "internal_error"
    CodeUnavailable  Code = "unavailable"
    CodeTimeout      Code = "timeout"
)

type Error struct {
    Code       Code
    Message    string   // an toàn để trả cho client
    HTTPStatus int
    Fields     []Field  // lỗi validate theo field
    Data       any
    // err error — wrapped, không export
}

func New(code Code, msg string, opts ...Option) *Error
func Wrap(err error, code Code, msg string) *Error
func (e *Error) Unwrap() error
func (e *Error) Error() string

func Is(err error, code Code) bool
func As(err error) (*Error, bool)
func Register(code Code, httpStatus int, defaultMsg string)   // cho code riêng của app
```

Khác biệt then chốt so với bản tham chiếu: **hỗ trợ đầy đủ `errors.Is`/`errors.As`/`Unwrap`**, code là typed constant chứ không phải string lấy từ `err.Error()`.

---

### 4.5 `core/log` — logger + masking 3 lớp

#### Khởi tạo

```go
type Format string
const (FormatJSON Format = "json"; FormatText Format = "text")

type Options struct {
    Level     slog.Level
    Format    Format
    Output    io.Writer      // default os.Stdout
    AppName   string
    AddSource bool
    Mask      MaskConfig
}

func New(opts Options) *slog.Logger      // <- trả về type stdlib
```

Handler chain: `MaskHandler → ContextHandler → JSONHandler`.

```go
func NewContextHandler(next slog.Handler) slog.Handler
// logger.InfoContext(ctx, "...") tự thêm trace_id, span_id, request_id,
// correlation_id, user_id — không cần gọi WithCtx thủ công
```

#### Vấn đề cần giải

Log đầy đủ body request/response cho **mọi** request (quyết định vận hành ở mục 2) tạo ra hai rủi ro:

1. **Dữ liệu nhạy cảm** lọt vào log — password, token, số thẻ. Phải che **bất kể dài ngắn**.
2. **Dung lượng** — một field base64 ảnh vài MB làm phình chi phí log, chậm pipeline, và có thể **vượt giới hạn kích thước dòng của Loki/CloudWatch khiến mất trắng cả dòng log đó**.

Đây là hai bài toán khác nhau và cần hai luật khác nhau. Vì vậy masking chia 3 lớp.

#### Lớp 1 — Chặn theo kích thước (không cần khai gì)

Mọi string dài hơn `MaxLen` tự động bị rút gọn. **Đây là lưới an toàn**: bắt được mọi field dài kể cả field chưa ai nghĩ tới — sprint sau thêm `signature_base64` thì vẫn tự động an toàn.

Quan trọng: **rút gọn thành metadata, không cắt chuỗi.**

```json
"image": { "_elided": "base64", "bytes": 1248332, "sha256": "9f2a1c4e" }
```

Lý do: khi debug, 50 ký tự đầu của base64 hoàn toàn vô dụng — không đọc được gì từ nó. Cái thực sự cần biết là *có ảnh hay không, nặng bao nhiêu, có đúng file client nói họ gửi không*. Size + 8 hex đầu của sha256 trả lời cả ba trong ~60 byte, và hash còn cho phép đối chiếu "client gửi trùng file 2 lần".

Nhận dạng `_elided` bằng heuristic chỉ để **đặt nhãn cho dễ đọc**, không dùng làm điều kiện kích hoạt:

- khớp `^[A-Za-z0-9+/]+=*$` → `"base64"`
- bắt đầu bằng `data:` → `"data-uri"`
- còn lại → `"text"`

#### Lớp 2 — Tag `log:` trên struct (cơ chế chính)

```go
type UploadDocRequest struct {
    UserID   string `json:"user_id"`
    Image    string `json:"image"    log:"elide"`
    Note     string `json:"note"     log:"truncate=100"`
    Password string `json:"password" log:"redact"`
    CardNo   string `json:"card_no"  log:"edges=6,4"`
    Internal string `json:"-"        log:"omit"`
}
```

Luật **đi cùng field**: đổi tên field thì tag đi theo → không thể lệch pha. Struct lồng nhau thì recursion tự đi theo type → không cần cú pháp đường dẫn kiểu `data.documents[*].image`.

```go
type Rule string
const (
    RuleRedact   Rule = "redact"        // "********" — bất kể dài ngắn
    RuleElide    Rule = "elide"         // → metadata {bytes, sha256}
    RuleTruncate Rule = "truncate"      // giữ N ký tự đầu
    RuleEdges    Rule = "edges"         // giữ p đầu, s cuối (số thẻ)
    RuleOmit     Rule = "omit"          // bỏ hẳn key khỏi log
    RuleHash     Rule = "hash"          // chỉ còn sha256 — đối chiếu được, không đọc được
)

func Safe(v any) slog.LogValuer
```

`Safe` parse tag **một lần cho mỗi type** rồi cache theo `reflect.Type` → chi phí reflection chỉ trả lần đầu.

Dùng:
```go
logger.InfoContext(ctx, "request", slog.Any("body", log.Safe(req)))
```

#### Lớp 3 — Danh sách tên field (fallback khi không có type)

Dùng khi payload là `map[string]any`: proxy payload của đối tác, webhook, endpoint động — không có struct để gắn tag.

```go
type MaskConfig struct {
    Fields map[string]Rule   // lớp 3 — theo tên field, khớp ở mọi độ sâu
    MaxLen int               // lớp 1 — mặc định 256. ĐÂY LÀ LƯỚI AN TOÀN
    HashElide bool           // có kèm sha256 khi elide hay không (mặc định true)

    MaxLineBytes int         // trần cho TOÀN dòng log, mặc định 32KB
}

func SafeMap(m map[string]any, cfg MaskConfig) map[string]any
```

`Fields` mặc định nên có sẵn nhóm phổ biến: `password`, `new_password`, `token`, `access_token`, `refresh_token`, `otp`, `pin`, `cvv`, `secret`, `authorization`, `api_key`.

#### Trần cho toàn dòng log

Vì log body bật cho mọi request, cần một chốt cuối: nếu sau khi mask xong mà dòng log vẫn vượt `MaxLineBytes`, **bỏ hẳn phần body** và thay bằng marker:

```json
{"msg":"request","body":{"_dropped":"log line exceeded 32KB","original_bytes":184320}}
```

Thà mất body của một request còn hơn mất **cả dòng log** vì backend từ chối — lúc đó bạn không còn cả trace_id lẫn status.

---

### 4.6 `core/config`

```go
func Load[T any](yamlBytes []byte) (*T, error)
func LoadFile[T any](path string) (*T, error)
func LoadWith[T any](yamlBytes []byte, lookup envconfig.Lookuper) (*T, error)  // test
```

Struct dùng 2 loại tag:

```go
type Config struct {
    App struct {
        Name string `yaml:"name" env:"APP_NAME"`
        Env  string `yaml:"env"  env:"APP_ENV"`
    } `yaml:"app"`
    DB struct {
        Host     string        `yaml:"host"     env:"DB_HOST"`
        Port     int           `yaml:"port"     env:"DB_PORT"`
        Password secret.Secret `yaml:"password" env:"DB_PASSWORD"`
    } `yaml:"db"`
}
```

Generic → không còn `Load(cfgMap any, ...)`. `Lookuper` inject được → test không cần set biến môi trường thật.

### 4.7 `core/secret`

```go
type Secret string

func (s Secret) String() string                  // "[REDACTED]"
func (s Secret) GoString() string                // "[REDACTED]"
func (s Secret) MarshalJSON() ([]byte, error)    // "\"[REDACTED]\""
func (s Secret) MarshalText() ([]byte, error)
func (s Secret) LogValue() slog.Value            // slog biết cách che
func (s Secret) Reveal() string                  // chỉ chỗ nào thật cần
```

~40 dòng, chặn được cả lớp sự cố lộ credential qua `fmt.Printf("%+v", cfg)` hoặc log struct config.

### 4.8 `core/crypto`

Loại code tuyệt đối không nên để mỗi service tự viết — sai một tham số là lỗ bảo mật.

```go
// Mã hoá field PII lưu trong DB — AES-256-GCM, nonce random mỗi lần
type Cipher struct{}
func NewCipher(key []byte) (*Cipher, error)           // key 32 byte
func (c *Cipher) Encrypt(plain []byte) ([]byte, error) // output: nonce || ciphertext || tag
func (c *Cipher) Decrypt(blob []byte) ([]byte, error)

// Type dùng trực tiếp trong model GORM — tự mã hoá khi ghi, giải mã khi đọc
type Encrypted string
func (e Encrypted) Value() (driver.Value, error)
func (e *Encrypted) Scan(v any) error
func (e Encrypted) LogValue() slog.Value              // luôn "[ENCRYPTED]"

// HMAC — xác thực webhook, ký request tích hợp đối tác
func SignHMAC(key, payload []byte) string             // hex
func VerifyHMAC(key, payload []byte, sig string) bool // hmac.Equal, chống timing attack

// Password
func HashPassword(pw secret.Secret) (string, error)   // argon2id, param theo OWASP
func VerifyPassword(pw secret.Secret, hash string) bool
```

Điểm cần chú ý khi implement:

- `VerifyHMAC` **bắt buộc** dùng `hmac.Equal`, không dùng `==` (timing attack)
- Nonce phải từ `crypto/rand`, không tái sử dụng
- Argon2id tham số theo khuyến nghị OWASP hiện hành, và ghi param vào chuỗi hash để đổi được về sau
- `Cipher` phải hỗ trợ **key rotation**: nhận nhiều key, giải mã thử theo `key_id` nhúng trong blob

### 4.9 `core/retry`

```go
type Policy struct {
    MaxAttempts int
    BaseDelay   time.Duration
    MaxDelay    time.Duration
    Multiplier  float64
    Jitter      float64          // 0..1 — chống thundering herd
    Retryable   func(error) bool
}

func Do(ctx context.Context, p Policy, fn func(ctx context.Context) error) error
func DoValue[T any](ctx context.Context, p Policy, fn func(ctx context.Context) (T, error)) (T, error)
```

Một implementation dùng chung cho HTTP client, Kafka consumer, DB reconnect.

### 4.10 `core/tlsx`

```go
type Options struct {
    CertPEM, KeyPEM, RootCAPEM []byte
    CertFile, KeyFile, RootCAFile string
    CertB64, KeyB64, RootCAB64 string   // cho K8s secret qua env
    ServerName string
    InsecureSkipVerify bool
    MinVersion uint16                    // default TLS 1.2
}

func ServerConfig(o Options) (*tls.Config, error)
func ClientConfig(o Options) (*tls.Config, error)
```

Gộp 4 hàm gần trùng nhau của bản tham chiếu thành 2, nguồn cert thành 3 dạng trong cùng một struct.

### 4.11 `core/timex` — cố tình giữ nhỏ

```go
const (
    DateISO      = "2006-01-02"
    DateCompact  = "20060102"
    RFC3339Milli = "2006-01-02T15:04:05.000Z07:00"
)

func StartOfDay(t time.Time) time.Time
func EndOfDay(t time.Time) time.Time
func ParseInLoc(layout, value string, loc *time.Location) (time.Time, error)
```

Bỏ hẳn interface `TimeHelper` — nó chỉ bọc `time.Now().In(loc)`.

---

### 4.12 `obs` — metrics

```go
func NewRegistry() *prometheus.Registry
func Handler(reg *prometheus.Registry) http.Handler        // cho /metrics

func HTTPMetrics(reg *prometheus.Registry, opts HTTPOptions) func(http.Handler) http.Handler
func RegisterDBStats(reg *prometheus.Registry, name string, fn func() sql.DBStats)
func RegisterRuntime(reg *prometheus.Registry)
```

Metric theo quy ước Prometheus:

- `http_server_requests_total{method,route,status}`
- `http_server_request_duration_seconds{method,route}` (histogram)
- `http_server_requests_in_flight`
- `db_pool_connections{name,state}`

**Quan trọng:** label theo **route pattern** (`/users/{id}`), không theo path thật (`/users/12345`) — nếu không sẽ nổ cardinality. Vì vậy:

```go
type HTTPOptions struct {
    RoutePattern func(*http.Request) string   // chi: RouteContext().RoutePattern()
                                              // Go 1.22 ServeMux: r.Pattern
}
```

Đây là cách xử lý đúng, thay cho việc bản tham chiếu dùng regex đoán `{id}`/`{code}` từ URL.

`obs` chỉ phụ thuộc `prometheus/client_golang` + stdlib (`database/sql`), **không phụ thuộc `core`** → module nào cũng import được không sợ cycle.

---

### 4.13 `httpx`

#### Middleware — tất cả đều `func(http.Handler) http.Handler`

```go
type Middleware = func(http.Handler) http.Handler

func Chain(mws ...Middleware) Middleware

func Trace(opts TraceOptions) Middleware        // đọc/sinh traceparent + request-id + correlation-id
func AccessLog(l *slog.Logger, opts LogOptions) Middleware
func BodyLog(l *slog.Logger, opts BodyLogOptions) Middleware
func Recover(l *slog.Logger) Middleware
func Timeout(d time.Duration) Middleware
func CORS(cfg CORSConfig) Middleware
func MaxBodySize(n int64) Middleware
func RateLimit(cfg RateLimitConfig) Middleware                  // in-process
func Compress(level int) Middleware
```

#### Log body — thiết kế 2 tầng

Yêu cầu: **mọi** request đều phải có log request và log response. Nhưng masking tốt nhất (lớp 2, theo tag) cần biết type, mà middleware chạy *trước* khi handler decode nên không biết type. Giải quyết bằng 2 tầng:

```
Tầng bảo đảm  (middleware BodyLog):  luôn ghi log, dùng lớp 1 + lớp 3 trên raw bytes
Tầng chất lượng (Decode/OK/Fail):    đăng ký bản đã mask theo tag vào context
```

Middleware cài một holder vào context; `Decode[T]` và `OK`/`Fail` ghi vào đó; cuối request middleware **ưu tiên bản đã đăng ký**, không có thì fallback về `SafeMap(raw)`.

```go
// httpx/middleware — nội bộ
type bodyCapture struct {
    mu       sync.Mutex
    request  any    // đã mask theo tag, nil nếu handler không dùng Decode
    response any
    rawReq   []byte
    rawResp  []byte
    truncated bool
}

func SetRequestBody(ctx context.Context, masked any)   // Decode[T] gọi
func SetResponseBody(ctx context.Context, masked any)  // OK/Fail gọi
```

Ba lợi ích:

1. **Không bao giờ mất log** — middleware bảo đảm luôn có dòng log dù handler làm gì.
2. **Masking chất lượng cao khi có type** — tag `log:` được áp dụng.
3. **Bỏ được một lần parse JSON.** Bản tham chiếu parse body để mask, marshal lại, rồi handler parse lần hai. Ở đây khi `Decode[T]` đã parse thành struct thì dùng luôn struct đó.

```go
type BodyLogOptions struct {
    MaxCapture   int64   // trần buffer response, mặc định 64KB — không giữ response 50MB trong RAM
    Mask         log.MaskConfig
    SkipPaths    []string  // /healthz, /readyz, /metrics
    SkipContentTypes []string
}
```

**Multipart:** không log nội dung, log metadata — bản tham chiếu bỏ qua hoàn toàn, mà upload file lại là chỗ hay lỗi nhất:

```json
"multipart": [
  {"field":"image","filename":"anh.jpg","content_type":"image/jpeg","bytes":248332},
  {"field":"user_id","value":"u_123"}
]
```

**Response nhị phân** (`image/*`, `application/pdf`, `octet-stream`): chỉ log content-type + bytes.

#### Auth

```go
type JWTConfig struct {
    Algorithm string
    Secret    secret.Secret     // HMAC
    PublicKey []byte            // RSA/ECDSA
    Issuer    string
    Audience  []string
    TTL       time.Duration
}

func NewJWT(cfg JWTConfig) (*JWT, error)      // trả error, KHÔNG panic
func (j *JWT) Sign(claims map[string]any, ttl time.Duration) (string, error)
func (j *JWT) Verify(token string) (Claims, error)
func (j *JWT) Middleware(opts ...MWOption) Middleware       // hỗ trợ nhiều key để rotate

func APIKey(validate func(context.Context, string) bool) Middleware
func BasicAuth(validate func(context.Context, string, string) bool) Middleware
```

#### Request / response

```go
type Envelope struct {
    TraceID   string  `json:"trace_id,omitempty"`
    Status    string  `json:"status"`                // ACCEPT | REJECT | PROCESSING
    Code      string  `json:"code,omitempty"`
    Message   string  `json:"message,omitempty"`
    Fields    []Field `json:"fields,omitempty"`
    Data      any     `json:"data,omitempty"`
    ElapsedMs int64   `json:"elapsed_ms,omitempty"`
}

func Decode[T any](r *http.Request) (T, error)        // JSON + validate + đăng ký log
func DecodeQuery[T any](r *http.Request) (T, error)

func OK(w http.ResponseWriter, r *http.Request, data any)
func Created(w http.ResponseWriter, r *http.Request, data any)
func Fail(w http.ResponseWriter, r *http.Request, err error)   // errs.Error → status + Envelope
func JSON(w http.ResponseWriter, status int, body any)
```

#### `httpx/idempotency`

Client mobile retry khi mạng lag là chuyện xảy ra hàng ngày. Không có lớp này thì một lần retry sinh ra hai bản ghi giao dịch.

```go
type Store interface {
    // Reserve: nếu key chưa có → đánh dấu đang xử lý, trả (nil, false).
    // Nếu đã có kết quả → trả (bản ghi, true).
    // Nếu đang xử lý dở → trả ErrInFlight.
    Reserve(ctx context.Context, key string, reqHash string, ttl time.Duration) (*Record, bool, error)
    Commit(ctx context.Context, key string, rec Record) error
    Release(ctx context.Context, key string) error   // handler panic → nhả khoá
}

type Record struct {
    Status  int
    Headers map[string]string
    Body    []byte
    ReqHash string
}

type Config struct {
    Store      Store
    TTL        time.Duration    // mặc định 24h
    HeaderName string           // mặc định "Idempotency-Key"
    Methods    []string         // mặc định POST, PATCH
    Required   bool             // true = thiếu header thì trả 400
}

func Middleware(cfg Config) Middleware
```

Ba tình huống phải xử lý đúng:

| Tình huống | Hành vi |
|---|---|
| Cùng key, cùng request hash | Trả lại response đã lưu, **không chạy handler** |
| Cùng key, **khác** request hash | Trả `422` — client dùng lại key cho payload khác, đây là lỗi phía client |
| Cùng key, request trước đang chạy | Trả `409` + header `Retry-After` |

Interface `Store` nằm ở `httpx`, implementation Redis nằm ở `cache/idemstore` → `httpx` không phải phụ thuộc Redis. Phase 2 làm bản in-memory để test, Phase 3 làm bản Redis.

#### Health check

```go
type Checker func(context.Context) error

type Health struct{}
func NewHealth() *Health
func (h *Health) Register(name string, c Checker)
func (h *Health) Live() http.Handler      // /healthz — process còn sống
func (h *Health) Ready() http.Handler     // /readyz  — dependency OK
func (h *Health) SetNotReady()            // gọi khi bắt đầu shutdown
```

Phân biệt liveness/readiness rất quan trọng với K8s: liveness fail → pod bị restart; readiness fail → chỉ bị rút khỏi load balancer.

#### HTTP client — bản tham chiếu KHÔNG có phần này

```go
type ClientConfig struct {
    BaseURL         string
    Timeout         time.Duration      // mỗi lần thử
    TotalTimeout    time.Duration      // toàn bộ kể cả retry
    Retry           retry.Policy
    Breaker         *BreakerConfig
    TLS             tlsx.Options
    MaxIdleConns    int
    Logger          *slog.Logger
    Mask            log.MaskConfig     // che field nhạy cảm khi log req/resp
    PropagateTrace  bool               // tự chèn traceparent
    Metrics         *prometheus.Registry
}

type Client struct{}
func NewClient(cfg ClientConfig) (*Client, error)

func (c *Client) Do(ctx context.Context, req Request) (*Response, error)
func Get[T any](ctx context.Context, c *Client, path string, opts ...ReqOption) (T, error)
func Post[T any](ctx context.Context, c *Client, path string, body any, opts ...ReqOption) (T, error)
```

Metric outbound: `http_client_requests_total{host,method,status}`, `http_client_request_duration_seconds`.

#### Server + graceful shutdown — bản tham chiếu KHÔNG có

```go
type ServerConfig struct {
    Addr              string
    Handler           http.Handler
    ReadTimeout       time.Duration
    WriteTimeout      time.Duration
    IdleTimeout       time.Duration
    ReadHeaderTimeout time.Duration     // chống Slowloris
    TLS               tlsx.Options
    ShutdownTimeout   time.Duration
    Logger            *slog.Logger
}

func NewServer(cfg ServerConfig) *Server
func (s *Server) Run(ctx context.Context) error   // block tới khi ctx cancel, rồi drain
```

Và một lifecycle manager cho toàn app:

```go
type App struct{}
func NewApp(l *slog.Logger) *App
func (a *App) Add(name string, run func(context.Context) error, stop func(context.Context) error)
func (a *App) Run(ctx context.Context) error
```

Thứ tự shutdown đúng: bắt SIGTERM → `SetNotReady()` → chờ LB rút traffic (~5s) → đóng HTTP server (drain request đang chạy) → dừng Kafka consumer (commit offset cuối) → đóng DB/Redis → hết `ShutdownTimeout` thì force.

---

### 4.14 `db`

```go
type Driver string
const (Postgres Driver = "postgres"; MySQL Driver = "mysql")

type Config struct {
    Driver   Driver
    Host     string
    Port     int
    User     string
    Password secret.Secret
    Database string
    Schema   string
    TimeZone string

    MaxOpenConns    int
    MaxIdleConns    int
    ConnMaxLifetime time.Duration
    ConnMaxIdleTime time.Duration

    TLS           tlsx.Options
    SlowThreshold time.Duration
    LogLevel      string
    Logger        *slog.Logger
}

func Open(ctx context.Context, cfg Config) (*gorm.DB, error)    // trả *gorm.DB trực tiếp
func NewGormLogger(l *slog.Logger, slow time.Duration) logger.Interface
```

**Quyết định:** không bọc `*gorm.DB` trong interface giả. Bản tham chiếu có interface `SqlGormDatabase` nhưng vẫn trả `*gorm.DB` — abstraction không abstract được gì. GORM chính là abstraction rồi.

#### Base entity — ghép được, thay vì 5 combo cứng

```go
package model

type Timestamps struct {
    CreatedAt time.Time `gorm:"autoCreateTime"`
    UpdatedAt time.Time `gorm:"autoUpdateTime"`
}
type Audit struct {
    CreatedAt time.Time `gorm:"autoCreateTime"`
    CreatedBy string
    UpdatedAt time.Time `gorm:"autoUpdateTime"`
    UpdatedBy string
}
type SoftDelete struct {
    DeletedAt gorm.DeletedAt `gorm:"index"`
}
type UUIDKey struct {
    ID string `gorm:"primaryKey;type:uuid"`
}
```

Dùng: `struct { model.UUIDKey; model.Audit; model.SoftDelete; Name string }`.

Tự điền `CreatedBy`/`UpdatedBy` bằng **gorm plugin**, không phải hook trên struct:

```go
func AuditPlugin(userFn func(context.Context) string) gorm.Plugin
```

Ưu điểm so với `BeforeCreate` hook: áp dụng cho mọi model không cần khai gì thêm, không tạo phụ thuộc từ model lên `core`.

#### Query — có whitelist chống injection

```go
package query

type Op string
const (Eq, Ne, Gt, Gte, Lt, Lte, Like, ILike, In, NotIn, IsNull Op = ...)
// CỐ TÌNH không có Op "Raw"

type Filter struct { Field string; Op Op; Value any }
type Sort   struct { Field string; Desc bool }
type Page   struct { Limit, Offset int; Sort []Sort }

// allowed: map từ tên field ở API → tên cột thật trong DB.
// Field nào không có trong map → trả lỗi. Đây là lớp chặn SQL injection.
func Apply(db *gorm.DB, fs []Filter, allowed map[string]string) (*gorm.DB, error)
func Paginate(p Page, allowed map[string]string) (func(*gorm.DB) *gorm.DB, error)

// Keyset pagination cho bảng lớn
type Cursor string
func EncodeCursor(vals map[string]any) Cursor
func DecodeCursor(c Cursor) (map[string]any, error)
func ApplyCursor(db *gorm.DB, c Cursor, sort []Sort, allowed map[string]string) (*gorm.DB, error)
```

#### Migrate

```go
type Migration struct {
    ID   string
    Up   func(*gorm.DB) error
    Down func(*gorm.DB) error
}
func Run(ctx context.Context, db *gorm.DB, ms []Migration, opts Options) error
func Rollback(ctx context.Context, db *gorm.DB, ms []Migration, to string) error
```

---

### 4.15 `cache`

```go
type Config struct {
    Addrs    []string          // 1 addr = standalone, nhiều = cluster
    Username string
    Password secret.Secret
    DB       int
    TLS      tlsx.Options
    PoolSize int
    Logger   *slog.Logger
}

type Client struct{}
func New(cfg Config) (*Client, error)
func (c *Client) Redis() redis.UniversalClient
func (c *Client) Close() error
```

**`redis.UniversalClient` xử lý cả standalone và cluster bằng một type** → bỏ hẳn việc phải viết 2 file gần trùng nhau như bản tham chiếu (redis.go + redis-cluster.go, mỗi file ~300–450 dòng).

#### Interface tách nhỏ

```go
type KV interface {
    Get(ctx context.Context, key string, dst any) error
    Set(ctx context.Context, key string, v any, ttl time.Duration) error
    SetNX(ctx context.Context, key string, v any, ttl time.Duration) (bool, error)
    Del(ctx context.Context, keys ...string) error
    Exists(ctx context.Context, keys ...string) (int64, error)
    TTL(ctx context.Context, key string) (time.Duration, error)
    Expire(ctx context.Context, key string, ttl time.Duration) error
    Incr(ctx context.Context, key string, by int64) (int64, error)
    MGet(ctx context.Context, keys []string, dst any) error
    Scan(ctx context.Context, pattern string, cursor uint64, count int64) ([]string, uint64, error)
}

type Hash     interface { HGet; HSet; HSetNX; HDel; HGetAll; HMSet; HMGet; HIncrBy }
type PubSub   interface { Publish; Subscribe }
type Stream   interface { XAdd; XCreateGroup; XReadGroup; XAck }
type Pipeline interface { ... }
```

`*Client` implement tất cả, nhưng consumer chỉ khai phụ thuộc vào interface hẹp mà nó cần → mock dễ. Bản tham chiếu có 1 interface ~30 method, thực tế không ai mock được.

Cache miss trả `errs.Code("cache_miss")` — consumer không phải import `go-redis` để so sánh với `redis.Nil`.

#### Cache-aside + chống stampede

```go
func GetOrLoad[T any](
    ctx context.Context, c KV, key string, ttl time.Duration,
    load func(context.Context) (T, error),
) (T, error)
```

Dùng `golang.org/x/sync/singleflight`: 100 request cùng miss một key thì chỉ 1 request xuống DB.

#### Lock

```go
func NewLocker(c redis.UniversalClient) *Locker
func (l *Locker) Acquire(ctx context.Context, key string, ttl time.Duration) (*Lock, error)
func (l *Locker) AcquireWithRenew(ctx context.Context, key string, ttl time.Duration) (*Lock, error)

func (lk *Lock) Context() context.Context   // BỊ CANCEL nếu mất lock
func (lk *Lock) Release(ctx context.Context) error
```

Điểm cải tiến: mất lock thì `Lock.Context()` bị cancel → công việc đang chạy dừng lại. Bản tham chiếu chỉ ghi log rồi tiếp tục chạy — nghĩa là 2 instance có thể cùng làm một việc.

#### Leader election

```go
type ElectorConfig struct {
    Key           string
    TTL           time.Duration
    RenewInterval time.Duration
    Redis         redis.UniversalClient
    Logger        *slog.Logger
}

func NewElector(cfg ElectorConfig) *Elector
func (e *Elector) Run(ctx context.Context, onLead func(context.Context) error) error
func (e *Elector) IsLeader() bool
```

Gọn hơn bản tham chiếu (4 callback: elected / notElected / demoted / error) → một hàm `onLead(ctx)`, ctx bị cancel khi mất quyền leader. Khó dùng sai.

#### Distributed cron

```go
type Cron struct{}
func NewCron(e *Elector, l *slog.Logger) *Cron
func (c *Cron) Add(name, spec string, job func(context.Context) error) error
func (c *Cron) Run(ctx context.Context) error   // chỉ chạy trên instance đang là leader
```

Bản tham chiếu có leader election và có field `WorkflowConfig.ScheduleCron` nhưng **không có scheduler**. Ghép 2 thứ lại là được tính năng hoàn chỉnh.

#### `cache/idemstore`

Implementation Redis cho `httpx/idempotency.Store`. Dùng `SET NX` để reserve, `SETEX` để commit.

---

### 4.16 `queue/kafka`

Đường dẫn module là `github.com/cqt002/gokit/queue/kafka`. Không có package nào ở
`queue/` — chỗ đó chỉ là namespace để driver thứ hai sau này (`queue/rabbitmq`,
`queue/sqs`) có nhà, mà không kéo theo dependency của Kafka.

Phần dùng chung giữa các driver **đã được factor sẵn** ở `core/tracectx`: propagate
trace qua header là cùng một cơ chế cho HTTP và cho mọi broker. Nếu về sau phát hiện
thêm phần chung thật thì tạo `queue/queue` — thêm package mới không breaking, còn đổi
đường dẫn module thì breaking.

```go
type SASLMechanism string
const (SASLPlain, SASLScram256, SASLScram512 SASLMechanism = ...)

type Message struct {
    Topic     string
    Key       string
    Value     []byte
    Headers   map[string]string
    Partition int32
    Offset    int64
    Timestamp time.Time
}
```

#### Producer

```go
type ProducerConfig struct {
    Brokers        []string
    ClientID       string
    SASL           *SASLConfig
    TLS            tlsx.Options
    Compression    string          // none|gzip|snappy|lz4|zstd
    RequiredAcks   Acks
    MaxRetries     int
    Async          bool
    PropagateTrace bool            // tự chèn traceparent vào header
    Logger         *slog.Logger
    Metrics        *prometheus.Registry
}

func NewProducer(cfg ProducerConfig) (*Producer, error)
func (p *Producer) Send(ctx context.Context, msgs ...Message) error
func (p *Producer) SendJSON(ctx context.Context, topic, key string, v any) error
func (p *Producer) Close()
```

#### Consumer — dùng handler, không dùng channel

```go
type Handler func(ctx context.Context, msg Message) error

type ConsumerConfig struct {
    Brokers      []string
    Group        string
    Topics       []string
    SASL         *SASLConfig
    TLS          tlsx.Options
    FromOldest   bool
    Concurrency  int             // số worker xử lý song song
    MaxRetries   int
    RetryBackoff retry.Policy
    DLQTopic     string          // gửi vào đây khi hết lượt retry
    Logger       *slog.Logger
    Metrics      *prometheus.Registry
}

func NewConsumer(cfg ConsumerConfig) (*Consumer, error)
func (c *Consumer) Run(ctx context.Context, h Handler) error   // chỉ commit khi h trả nil
func (c *Consumer) Close()
```

Ba điểm khác biệt so với bản tham chiếu:

1. **Handler thay vì expose channel.** Channel không cho biết message đã xử lý xong hay chưa → offset commit tách rời khỏi việc xử lý → mất message khi restart. Handler trả `error` thì commit mới có ý nghĩa.
2. **Có retry + DLQ.** Bản tham chiếu không có. Message lỗi hoặc bị bỏ, hoặc kẹt vòng lặp vô hạn.
3. **Không cần package worker pool riêng.** `Concurrency` nằm ngay trong consumer — nơi thực sự cần nó — dùng `errgroup`.

Trace: producer chèn `traceparent` vào header, consumer đọc ra và đặt vào ctx → log của producer và consumer nối được với nhau bằng cùng một `trace_id`.

---

### 4.17 `testx`

```go
// Container (build tag `integration`)
func PostgresContainer(t *testing.T) (dsn string)
func MySQLContainer(t *testing.T) (dsn string)
func RedisContainer(t *testing.T) (addr string)
func KafkaContainer(t *testing.T) (brokers []string)

// Log assertion
func CaptureLogs(t *testing.T) (*slog.Logger, *LogBuffer)
func (b *LogBuffer) Has(level slog.Level, msg string) bool
func (b *LogBuffer) Field(idx int, key string) any

// Khác
func FreezeTime(t *testing.T, at time.Time)
func Golden(t *testing.T, name string, got []byte)
func LoadFixture[T any](t *testing.T, path string) T
```

Module này là thứ khiến bộ lib **thực sự được dùng**: nếu người ta không test nổi service viết trên gokit thì họ sẽ không dùng gokit.

---

## 5. Lộ trình implement

### Phase 0 — Bộ khung `✅`

- [x] Đổi tên folder local `go-libs` → `gokit`, cập nhật remote URL
- [x] Đổi tên repo GitHub `go-libs` → `gokit` (Settings → Repository name)
- [x] 8 file `go.mod` + `go.work` (`go.work.sum` đã vào `.gitignore`)
- [x] `.golangci.yml`, `Makefile` (`make test`, `make lint`, `make test-integration`)
- [x] CI: matrix theo module; **1 job riêng chạy `GOWORK=off`**
- [x] CI: bước grep chặn module path viết hoa `CQT002` (`scripts/check-module-path.sh`)
- [x] `README.md`

**Việc phát sinh — di chuyển `kafka/` → `queue/kafka/`** (xem lý do ở mục 2). Làm
trước khi có tag đầu tiên: sau khi tag rồi thì đây là breaking change cho mọi importer.

- [x] `git mv kafka queue/kafka`; module path → `github.com/cqt002/gokit/queue/kafka`
- [x] `go.work`: `use ./queue/kafka`
- [x] `Makefile`: `MODULES` đổi `kafka` → `queue/kafka`
- [x] `Makefile`: **`--config=../.golangci.yml` → `$(CURDIR)/.golangci.yml`** ở target
      `lint` và `fmt`. Với module lồng một tầng, `../` trỏ vào `queue/` chứ không phải
      gốc repo, lint sẽ fail vì không thấy config. CI không bị vì đã dùng
      `${{ github.workspace }}`
- [x] CI `ci.yml`: matrix `module` đổi `kafka` → `queue/kafka` ở cả job `test` và `lint`
- [x] `README.md`: bảng module + giải thích tầng `queue/`
- [x] Kiểm chứng: `make check` + `make build-nowork` + `make tidy-check` đều xanh

Chốt trong lúc làm:

| Hạng mục | Giá trị | Ghi chú |
|---|---|---|
| Go tối thiểu | `go 1.25.0` ở cả 8 `go.mod` | CI test matrix `1.25.x` × `stable` |
| golangci-lint | `v2.13.0`, pin ở `Makefile` + CI | v2.6.x không đọc được export data của Go 1.27 |
| CI | GitHub Actions — job `guard`, `test`, `lint`, `nowork`, `tidy`, `integration` | `integration` chỉ chạy nightly/dispatch |
| Build khi `GOWORK=off` | `go build -o /dev/null ./...` | `-o dir/` đòi main package; `./...` trần thì đụng thư mục `examples/api` |
| revive `exported` | Bỏ `checkPrivateReceivers` | Nó đòi godoc cho method của type unexported — những method không có mặt trong godoc, nên comment chỉ phục vụ linter. Chuẩn đã chốt là "godoc cho mọi symbol export" |
| `make tidy-check` | Restore bằng `trap ... EXIT` | Bản đầu `exit 1` trước bước restore, nên khi fail nó để lại `go.mod` đã bị sửa và file `.bak` trên đĩa. Đã kiểm chứng hai chiều: pass khi tidy, fail mà không để lại dấu vết khi không tidy |

> Còn một việc bằng tay: remote URL của `origin` vẫn đang viết hoa tên user. Chạy
> `git remote set-url origin https://github.com/cqt002/gokit.git` cho khớp module path.
> (Guard `check-module-path` quét cả `*.md`, nên đừng viết dạng viết hoa vào file này.)

### Phase 1 — `core` `✅`

Thứ tự bắt buộc (do phụ thuộc):

- [x] `idx`
- [x] `secret`
- [x] `tracectx` (phụ thuộc `idx`)
- [x] `ctxmeta`
- [x] `errs`
- [x] `log` — handler chain + **masking lớp 1** (elide theo kích thước)
- [x] `log` — **masking lớp 2** (`Safe` đọc tag `log:`, cache theo `reflect.Type`)
- [x] `log` — **masking lớp 3** (`SafeMap` theo tên field) + trần `MaxLineBytes`
- [x] `config` (phụ thuộc `secret`)
- [x] `crypto` — AES-GCM + key rotation, HMAC, argon2id
- [x] `retry`
- [x] `tlsx`
- [x] `timex`

**Định nghĩa "xong":** coverage ≥ 80%, không cần Docker, godoc đầy đủ cho mọi symbol export.

Đã thêm ngoài đặc tả ở mục 4 (đều là bổ sung, không phá vỡ API đã chốt) — cần soi
lại khi đóng băng API cuối Phase 1:

| Package | Thêm | Lý do |
|---|---|---|
| `secret` | `MarshalYAML`, `IsZero`, `Equal` | Dump config ra YAML là đường lộ ngang hàng với JSON. `Equal` dùng `subtle.ConstantTimeCompare` — `httpx/auth` so khớp API key cần đúng thứ này, để mỗi service tự viết `==` là hở kênh phụ |
| `tracectx` | `Valid`, `String`, `TraceIDFrom`, `HeaderTraceparent`, `ErrInvalidTraceparent` | `Valid` là điều kiện dùng chung của `Traceparent`/`NewChild`; `HeaderTraceparent` để `httpx` và `queue/kafka` không tự viết lại tên header |
| `errs` | `HTTPStatus(err)`, `WithCause`, `opts` cho `Wrap` | `httpx.Fail` cần lấy status từ error bất kỳ, kể cả error thường (→ 500) |
| `log` | `MaskConfig.DisableElideHash` thay cho `HashElide` | Cùng hành vi, nhưng bool zero trong Go là `false` nên chỉ chiều phủ định giữ được mặc định "có hash" khi không khai gì |
| `log` | `AttrUserType` (`user_type`) trong ContextHandler | `ctxmeta.Meta` đã có field này; đính thêm gần như miễn phí |
| `log` | `Elided`, `LabelBase64/DataURI/Text`, `DefaultMaskFields()`, `NewMaskHandler` | Hình dạng metadata và danh sách field mặc định là thứ test của service cần assert được |
| `config` | `Lookuper` định nghĩa lại trong package, không dùng `envconfig.Lookuper` | Không để đường dẫn import của thư viện env lọt vào API công khai — đổi thư viện về sau sẽ không phải breaking change. Hai interface giống hệt nhau nên gán trực tiếp được |
| `crypto` | `NewCipherWithKeys`, `Key`, `PrimaryKeyID`, `KeyIDs` | Đặc tả yêu cầu key rotation nhưng chỉ có `NewCipher(key)` — cần đường khai nhiều khoá |
| `crypto` | `SetDefaultCipher`, `DefaultCipher`, `ErrNoCipher` | `Encrypted.Value()`/`Scan()` do GORM gọi, không nhận tham số nào, nên cipher phải đến từ chỗ method tìm được |
| `crypto` | `Encrypted.String/GoString/MarshalJSON/Reveal` | Đặc tả chỉ có `LogValue`, nhưng `fmt.Sprintf("%+v", model)` và `json.Marshal(model)` là hai đường lộ PII ngang hàng với log |
| `crypto` | `NeedsRehash` | Ghi tham số vào chuỗi hash chỉ có ích nếu phát hiện được hash cũ để nâng cấp |
| `retry` | `DefaultRetryable` và các hằng `Default*` | Chỗ gọi cần bọc lại luật mặc định thay vì viết lại từ đầu |
| `tlsx` | `ErrConflictingSources` | Khai nhiều nguồn cho cùng một vật liệu là lỗi cấu hình, chỗ gọi cần phân biệt được nó với lỗi đọc file |

Quyết định đáng ghi lại:

- `SpanContext.NewChild()` trên giá trị không hợp lệ trả về **trace gốc mới**, không
  nhân bản trace ID rỗng — nếu không, mọi request thiếu header sẽ dồn vào chung một
  "trace" vô nghĩa.
- `ParseTraceparent` **từ chối hex viết hoa** theo đúng đặc tả W3C. Chỗ gọi coi lỗi
  parse là "không có trace thượng nguồn" và tạo trace gốc mới, chứ không phải lỗi request.
- `errs.Is` quét **toàn bộ** chuỗi lỗi (kể cả qua `errors.Join`), còn `errs.As` trả
  **lớp ngoài cùng** — lớp gần chỗ trả response nhất là phân loại đúng nhất cho client.
- `errs.Register` là chỗ duy nhất có state toàn cục thay đổi được. Không tránh được:
  một `*Error` tạo ở tầng repository không có đường cầm theo registry. Bù lại nó chỉ
  dành cho lúc khởi tạo, và panic ngay nếu tham số sai.
- `errs.New` với mã lỗi chưa đăng ký trả HTTP 500 thay vì panic — một mã viết sai
  không được phép làm sập request đang xử lý.

Quyết định của `core/log`:

- **Thứ tự ưu tiên khi nhiều lớp cùng áp vào một giá trị:** tag (lớp 2) → tên field
  (lớp 3) → kích thước (lớp 1). Lớp 1 áp cả lên kết quả của lớp 2: `log:"truncate=1000"`
  với `MaxLen` 256 thì vẫn bị elide. Lưới an toàn có ngoại lệ thì không còn là lưới —
  muốn giữ dài hơn thì nâng `MaxLen`.
- **`MaskConfig.Fields` được trộn vào `DefaultMaskFields`, không thay thế.** Khai một
  field riêng của app mà vô tình tắt việc che `password` là đúng cái cách log lộ mật
  khẩu mà không ai nhận ra. Trùng tên thì bản khai tay thắng.
- **So khớp tên field không phân biệt hoa thường, coi `-` như `_`**, nên `Authorization`,
  `api-key`, `API_KEY` đều khớp.
- **Tag sai cú pháp bị coi là `redact`.** Che chặt hơn ý muốn là hướng sai an toàn, và
  output `********` thay vì giá trị mong đợi là thứ test nhận ra ngay.
- **Cắt chuỗi theo rune, không theo byte** (`truncate`, `edges`). Cắt giữa một ký tự
  UTF-8 làm dòng log chứa byte không hợp lệ, mà tiếng Việt thì ký tự nào cũng nhiều byte.
  Cùng lý do, ngưỡng `MaxLen` chỉ tính là vượt khi vượt **cả** theo byte lẫn theo rune.
- **`edges` trên giá trị ngắn hơn `p+s` thì che sạch**, không để lộ nguyên giá trị chỉ
  vì nó ngắn hơn dự kiến. Số dấu sao chặn ở 32 để chuỗi dài không thành hàng nghìn dấu sao.
- **`RuleHash` chỉ an toàn với giá trị đủ entropy.** sha256 của OTP 6 số hay số thẻ 16
  số bị dò ngược trong vài giây; những giá trị đó phải dùng `redact` hoặc `edges`. Đã
  ghi cảnh báo này trong godoc của `RuleHash`.
- **`MaxLen` có sàn 16.** Chuỗi thay thế do chính cơ chế che sinh ra (`[REDACTED]` dài
  10 byte) không được biến thành metadata elide.
- **Trần dòng log bỏ attribute lớn nhất trước**, lặp tới khi lọt trần. Nhờ vậy
  `trace_id`, `status`, `method` là những thứ sống sót cuối cùng — mất body còn tra được
  request, mất cả dòng thì không.
- **Kích thước dòng log là ước lượng theo byte thô**, chưa tính phần escape của JSON. Đo
  chính xác đòi serialize hai lần cho mọi dòng log. Bù lại bằng cách đặt `MaxLineBytes`
  thấp hơn giới hạn thật của backend.
- **`SafeMap` trả về dữ liệu thuần** (`map`, `string`, số) chứ không phải type của gokit,
  nên payload đã che có thể đem marshal bằng encoder nào cũng được.
- **Trải phẳng struct nhúng giống `encoding/json`**, kể cả khi type nhúng là unexported:
  bản thân field nhúng không lấy được qua `Interface()` nhưng các field export bên trong
  thì lấy được, và bỏ qua sẽ làm log thiếu field so với body thật.
- **Sắp xếp key của map trước khi ghi.** Thứ tự lặp map trong Go là ngẫu nhiên; không sắp
  thì golden test vô dụng.
- **Đệ quy chặn ở độ sâu 32.** Một struct trỏ vòng lại chính nó không được phép làm sập
  process — logger là thứ cuối cùng được phép gây sự cố.

Quyết định của 5 package cuối:

- **`config` bật `DefaultOverwrite` của envconfig.** Mặc định của thư viện là **không**
  ghi đè field đã có giá trị, nghĩa là mọi field khai trong YAML sẽ miễn nhiễm với biến
  môi trường — mất hẳn thứ tự ưu tiên mà package hứa. Đã kiểm chứng bằng test.
- **Thứ tự ưu tiên đầy đủ: env > YAML > `default=` trong tag > zero.** `default=` **không**
  ghi đè YAML dù đã bật `DefaultOverwrite`, vì envconfig chỉ ghi đè khi biến thực sự có
  mặt. Có test khoá hành vi này lại.
- **Key lạ trong YAML là lỗi** (`KnownFields(true)`). Một key gõ sai bị im lặng bỏ qua là
  kiểu sự cố tốn nhiều giờ nhất của config loader: cấu hình trông như đã khai mà thực tế
  đang chạy giá trị mặc định.
- **Blob của `crypto.Cipher` mang ID khoá, và ID đó nằm trong AAD của GCM.** Không xác thực
  ID thì ai cũng sửa được ID trong blob để lừa hệ thống giải mã bằng khoá khác. Có test
  đổi ID và test lật từng byte một ở mọi vị trí.
- **`crypto` không cho cấu hình tham số nào ảnh hưởng độ an toàn.** Thuật toán, kích thước
  nonce, tham số argon2 cố định trong code. Tham số quá thấp làm hash bị brute force, và
  người khai thường không có cơ sở để chọn khác.
- **`RuleHash`/`crypto` cảnh báo về entropy:** sha256 của OTP 6 số hay số thẻ 16 số bị dò
  ngược trong vài giây. Đã ghi trong godoc.
- **Mật khẩu rỗng bị từ chối ngay khi hash.** Nếu hash được thì `VerifyPassword("")` trả
  true, và đó là đường vào hệ thống không cần mật khẩu.
- **Lỗi giải mã không kể chi tiết của GCM** — chi tiết đó không giúp debug mà lại nói cho
  người thử tấn công biết họ sai ở đâu.
- **`tlsx` coi việc khai nhiều nguồn cho cùng một vật liệu là lỗi**, không phải thứ tự ưu
  tiên. Khi cert trên đĩa khác cert trong env, đoán xem cái nào thắng là tự tạo sự cố.
- **`retry.Jitter` chưa khai thì dùng mặc định 0.2, muốn tắt phải khai số âm.** Bất đối
  xứng có chủ ý: mất jitter làm hỏng đúng tính chất package tồn tại để bảo đảm, còn bị
  jitter khi không mong đợi thì không gây hại gì.
- **`retry` gộp lỗi bằng `errors.Join` khi context kết thúc giữa lúc chờ**, để `errors.Is`
  kiểm tra được cả lý do đang thử lại và lý do dừng.
- **`timex` giữ nguyên location trong `StartOfDay`/`EndOfDay`.** Với giờ sát nửa đêm, đổi
  sang UTC làm lệch hẳn một ngày.

> **Cổng chặn:** API của `core` phải **đóng băng** trước khi sang Phase 3. Trong thiết kế multi-module, sửa breaking change ở `core` nghĩa là release `core`, rồi bump require + release lần lượt 6 module còn lại.

### Phase 2 — `obs` + `httpx` `✅`

- [x] `obs`: registry, `/metrics`, `HTTPMetrics`, `RegisterDBStats`, `RegisterRuntime`
- [x] `httpx/middleware`: `Trace`
- [x] `httpx/middleware`: `AccessLog`, `Recover`, `Timeout`, `CORS`, `MaxBodySize`
- [x] `httpx/middleware`: `BodyLog` — holder trong context + fallback `SafeMap`
- [x] `httpx/middleware`: `RateLimit` (in-process)
- [x] `httpx`: `Envelope`, `Decode[T]` (+ đăng ký body đã mask), `OK`/`Created`/`Fail`
- [x] `httpx`: error mapper từ `errs`
- [x] `httpx/validate`: rule cắm thêm được
- [x] `httpx/auth`: JWT, APIKey, BasicAuth
- [x] `httpx/health`: `/healthz`, `/readyz`
- [x] `httpx`: `Server` + `App` lifecycle (graceful shutdown)
- [x] `httpx/client`: retry + breaker + propagate trace + mask log
- [x] `httpx/idempotency`: middleware + `Store` interface + store in-memory
- [x] `examples/api`: service chạy được thật — **nơi kiểm chứng thiết kế**

Coverage: `obs` 100%, `httpx` 88.1%. Chạy service mẫu thật (`ADDR=… go run ./api` + curl)
là bước đã bắt được hai lỗi mà toàn bộ test unit không thấy — xem phần dưới.

Test bằng `httptest`, không cần Docker.

**`httpx` KHÔNG phụ thuộc `obs`** — khác đồ thị ở mục 3. `obs.HTTPMetrics` tự là một
middleware nên app ghép nó vào chain; `httpx` không cần biết `obs` tồn tại. Ít một
dependency, và `go mod tidy` là thứ phát hiện ra điều đó.

**Cách xử lý module chưa publish:** `httpx/go.mod` và `examples/go.mod` khai `require`
kèm `replace` trỏ vào cây nguồn trong repo, vì `core` chưa có bản nào trên proxy. Hệ quả
cần biết: job `nowork` không còn kiểm được lệch version giữa các module nữa, chỉ còn
kiểm thiếu `require` và lệch `go` directive. Bỏ `replace` và điền version thật ở Phase 5.

Quyết định của Phase 2:

- **`obs.HTTPOptions.RoutePattern` không mặc định về `r.URL.Path`.** Không khai thì nhãn
  là `"unknown"`. Mặc định an toàn quan trọng hơn mặc định tiện: một scanner quét vài
  nghìn URL lạ sẽ sinh vài nghìn series và làm Prometheus hết bộ nhớ.
- **Có `obs.ServeMuxRoute`** vì `r.Pattern` của ServeMux gồm cả method (`GET /users/{id}`),
  nên dùng thẳng nó sẽ nhân đôi thông tin đã có ở label `method`.
- **`RegisterDBStats` đưa `name` vào constant label**, không phải variable label. Prometheus
  so trùng collector theo tập `Desc`, nên hai database sẽ bị coi là trùng nếu dùng variable
  label — test bắt được đúng lỗi này.
- **`middleware.Timeout` không dùng `http.TimeoutHandler`** của stdlib vì nó buffer toàn bộ
  response trong RAM. Thay bằng writer có mutex: hết hạn thì ghi 503 rồi khoá, handler ghi
  tiếp nhận `http.ErrHandlerTimeout`. Nhờ vậy client nhận response **ngay tại mốc timeout**
  thay vì phải chờ handler chịu dừng.
- **`middleware.CORS` và `RateLimit` trả `error`.** Cấu hình CORS sai biểu hiện thành lỗi
  trên trình duyệt của người dùng cuối, rất xa chỗ khai; `"*"` cùng `AllowCredentials` là
  cấu hình trình duyệt từ chối theo đặc tả, nên phải lộ ra lúc dựng.
- **`RateLimit` có trần số key bắt buộc.** Hạn mức theo IP với map không giới hạn là một
  đường làm hết bộ nhớ, và người tấn công chỉ cần đổi IP nguồn.
- **`Trace` mặc định KHÔNG tin request ID của client.** Tin nghĩa là client trộn được log
  của mình vào log của request khác. ID nhận từ client bị lọc ký tự và cắt độ dài — một ký
  tự xuống dòng trong đó là log injection.
- **`BodyLog` không tự parse multipart.** Chỉ đọc metadata từ `r.MultipartForm` nếu handler
  đã gọi `ParseMultipartForm`. Tự parse nghĩa là đọc hết body vào RAM hoặc đĩa tạm cho
  **mọi** request upload chỉ để ghi log.
- **`BodyLog` parse JSON với `UseNumber`.** Không có nó thì một ID 19 chữ số trong log thành
  `1.2345678901234568e+18` — mất đúng những chữ số cần để tra cứu.
- **`Decode` bật `DisallowUnknownFields`.** Client gửi `amout` thay vì `amount` mà server im
  lặng bỏ qua rồi xử lý với số tiền 0 là kiểu sự cố tệ nhất trong nhóm này.
- **`httpx.Fail` không bao giờ đưa `err.Error()` của error thường ra client** — nội dung đó
  thường chứa tên host, câu SQL, đường dẫn file.
- **`auth` từ chối thuật toán `none`, và bắt token dùng đúng thuật toán đã khai.** Có test
  riêng cho lỗ hổng algorithm confusion ở cả nhánh ECDSA và RSA: token ký HMAC với khoá là
  public key của server phải bị từ chối.
- **`auth` đòi khoá HMAC tối thiểu 32 byte.** Khoá ngắn làm HMAC bị brute force, và đây là
  loại sai không có triệu chứng nào cho tới khi bị khai thác.
- **`/healthz` cố tình không kiểm tra dependency nào.** Nối liveness với database nghĩa là
  database chập một nhịp sẽ làm Kubernetes restart toàn bộ pod cùng lúc — biến sự cố nhỏ
  thành sự cố toàn hệ thống.
- **`health.Ready` chạy checker song song và bọc `recover`** quanh từng checker: một checker
  panic không được biến việc kiểm tra sức khoẻ thành nguyên nhân gây sự cố.
- **`App` dừng thành phần theo thứ tự ngược thứ tự thêm**, và một thành phần lỗi làm cả app
  dừng. Service còn HTTP sống nhưng consumer đã chết vẫn qua health check và vẫn nhận
  traffic, trong khi nửa chức năng đã ngừng — thà chết hẳn để Kubernetes restart.
- **`client.Do` giữ lại response của lần thử cuối.** `retry.DoValue` trả giá trị zero khi
  lỗi, mà chi tiết của một lỗi 5xx nằm chính trong body đó. Và `statusError` chỉ là tín hiệu
  nội bộ cho vòng retry, không phải lỗi trả ra — đã nhận được response thì không phải lỗi
  "không gọi được". Test bắt được cả hai.
- **`client` đọc hết body vào bộ nhớ** trước khi gửi, vì retry cần gửi lại: một `io.Reader`
  đã đọc hết thì lần retry sẽ gửi body rỗng.
- **`idempotency` chỉ lưu kết quả 2xx/3xx.** Lưu cả 5xx nghĩa là client retry sẽ nhận lại
  đúng lỗi đó mãi dù nguyên nhân đã hết. Handler panic thì nhả khoá, nếu không client nhận
  409 suốt TTL dù không có gì đang chạy.
- **`idempotency` từ chối request khi Store lỗi** thay vì chạy tiếp: không biết request đã
  chạy chưa thì chạy tiếp có thể tạo bản ghi trùng — đúng cái mà lớp này tồn tại để ngăn.

**Hai lỗi chỉ chạy service mẫu thật mới phát hiện được** (test unit đều xanh trước đó):

1. **Số thẻ lọt nguyên vẹn vào log ở đường idempotency phát lại.** Request phát lại không
   chạy handler → không có `Decode` → không có bản mask theo tag → rơi về lớp 3, mà
   `card_no` không nằm trong `DefaultMaskFields`. Fallback hoạt động đúng đặc tả, nhưng
   **mặc định chưa an toàn**. Đã bổ sung nhóm thanh toán (`card_no`, `card_number`, `pan`,
   `cvv`, `cvc`) và nhóm xác thực còn thiếu (`old_password`, `id_token`, `client_secret`,
   `private_key`) vào danh sách mặc định của `core/log`.
2. **Log response hiện cả field mà HTTP response đã bỏ** (`code:""`, `data:null`,
   `elapsed_ms:0`), vì walk bằng reflect không tôn trọng `omitempty`. Đã sửa `core/log` để
   tôn trọng nó — dòng log giờ có cùng hình dạng với JSON thật đi ra dây, đúng mục tiêu đã
   ghi trong godoc.

Một **data race thật trong code thư viện** do `-race` bắt: `Server.Addr()` là method công
khai đọc field mà `Run()` ghi từ goroutine khác. Đã đổi sang `atomic.Pointer[string]`.

Một góp ý đúng của linter đã tiếp nhận: `net.Listen` → `(*net.ListenConfig).Listen` để việc
bind cũng tôn trọng context.

### Phase 3 — `db` + `cache` + `testx` `⬜`

- [ ] `db`: `Open`, `Config`, gorm slog logger, đăng ký `RegisterDBStats`
- [ ] `db/model`: base entity ghép được + `AuditPlugin`
- [ ] `db/query`: `Apply`, `Paginate` (có whitelist)
- [ ] `db/query`: cursor pagination
- [ ] `db/migrate`
- [ ] `cache`: `Client` trên `UniversalClient` + các interface tách nhỏ
- [ ] `cache`: `GetOrLoad` + singleflight
- [ ] `cache/lock` (context bị cancel khi mất lock)
- [ ] `cache/leader`
- [ ] `cache/cron`
- [ ] `cache/idemstore` (store Redis cho idempotency)
- [ ] `testx`: container helper, `CaptureLogs`, `Golden`

Phase này CI cần Docker. Test integration để sau build tag `integration`.

### Phase 4 — `queue/kafka` `⬜`

- [ ] `Producer` (sync + async), SASL/TLS
- [ ] Propagate trace qua Kafka header
- [ ] `Consumer` với handler + concurrency
- [ ] Retry + DLQ
- [ ] Metrics qua `kprom`
- [ ] Test bằng `kfake` của franz-go + 1 test integration thật

### Phase 5 — Hoàn thiện `⬜`

- [ ] Godoc mọi symbol export + `Example` function
- [ ] `examples/`: consumer, cron job
- [ ] Tag `core/v0.1.0`, `obs/v0.1.0`, `queue/kafka/v0.1.0`, ... (**prefix là đường dẫn module tính từ gốc repo**, nên module lồng thì tag cũng lồng)
- [ ] `CHANGELOG.md` mỗi module
- [ ] `docs/adr/` — ghi lại lý do các quyết định
- [ ] Giữ ở `v0.x` cho tới khi API ổn định — `v1` là lời hứa tương thích

**Tổng: ~5–6 tuần part-time.** Không có deadline — làm tới đâu tick tới đó.

---

## 6. Chiến lược test

| Loại | Phạm vi | Docker | Chạy khi nào |
|---|---|---|---|
| Unit | toàn bộ `core`, `obs`, `httpx` (qua `httptest`) | Không | Mọi commit |
| Golden | định dạng JSON của log, shape của `Envelope` | Không | Mọi commit |
| Integration | `db`, `cache`, `queue/kafka` (testcontainers) | Có | PR + nightly |
| Race | `go test -race` toàn bộ | Không | Mọi commit |

Ưu tiên test cho những chỗ dễ sai nhất:

**Masking** (quan trọng nhất, vì log body bật cho mọi request):
- Lớp 1: string vượt `MaxLen` → elide đúng metadata; nhận nhãn base64 / data-uri / text
- Lớp 1: **dòng log vượt `MaxLineBytes` → drop body, giữ nguyên trace_id và status**
- Lớp 2: mọi `Rule`; struct lồng nhau; slice của struct; con trỏ nil; field không phải string
- Lớp 2: cache theo `reflect.Type` không bị nhiễm giữa các type khác nhau
- Lớp 3: khớp tên field ở mọi độ sâu, trong slice, trong map lồng map
- `Secret` nằm trong struct phải bị che ở **cả 3 lớp**
- Ưu tiên: bản đăng ký từ `Decode[T]` phải thắng bản `SafeMap(raw)` của middleware
- Handler không gọi `Decode` → vẫn phải có log body (fallback hoạt động)

**Khác:**
- `ParseTraceparent`: input rác, version lạ, sai độ dài
- `config`: env overlay đúng thứ tự ưu tiên; type sai → lỗi rõ ràng
- `errs`: `errors.Is`/`As` xuyên nhiều lớp wrap
- `crypto`: encrypt/decrypt vòng tròn; **key rotation giải mã được blob của key cũ**; `VerifyHMAC` dùng `hmac.Equal`
- `query.Apply`: **field không nằm trong whitelist phải bị từ chối** (test bảo mật)
- `idempotency`: cùng key khác hash → 422; đang xử lý dở → 409; handler panic → nhả khoá
- Consumer: lỗi → retry → vào DLQ; **không commit offset khi handler lỗi**
- Lock: mất lock → context bị cancel

---

## 7. Những gì cố tình KHÔNG làm

| Hạng mục | Lý do |
|---|---|
| DI container | Wiring bằng constructor rõ ràng hơn; DI container che mất đồ thị phụ thuộc và fail lúc runtime thay vì compile time |
| Interface chung cho `queue/*` (`Publisher`, `Subscriber`) | Mô hình của các broker khác nhau ở đúng chỗ quan trọng: partition/offset/commit tay của Kafka, exchange/ack/prefetch của RabbitMQ, visibility timeout của SQS. Một interface phủ hết chúng chỉ còn `Publish(topic, msg)` — bỏ mất lý do người ta chọn broker đó. Thư mục `queue/` là namespace, không phải abstraction |
| Oracle, SQL Server | CGO của `godror` gây khổ cho build/CI; không có nhu cầu |
| `sqlx` | Không cần Oracle/MSSQL nữa; Postgres thì `pgx` trực tiếp tốt hơn |
| Object mapper tự động (`copier`) | Bỏ lỗi im lặng. Map tay hoặc dùng codegen (`goverter`) |
| Package worker pool riêng | `errgroup` + `SetLimit` tốt hơn (có context + lan truyền lỗi); concurrency đặt trong consumer |
| Helper `array`/`string`/`struct` | `slices`/`maps` stdlib đã bao phủ |
| Wrapper `time` dày | Bọc `time.Now().In(loc)` không tạo ra giá trị |
| Interface bọc `*gorm.DB` | Abstraction không abstract được gì |
| OTel SDK (giai đoạn này) | Chưa đủ service để hoàn vốn. Nhưng trace ID đã theo chuẩn W3C nên cắm vào sau là khớp |
| Sampling / chỉ log body khi lỗi | Đã cân nhắc và **bỏ**: yêu cầu vận hành là phải tra được mọi request. Bù lại bằng masking lớp 1 + `MaxLineBytes` |
| gRPC, object storage, i18n, multi-tenancy | Chưa có nhu cầu. Thêm sau được vì `core` không phụ thuộc chúng |
| Notification sender (SMS/email/push) | Đây là một **service riêng**, không phải thư viện |
| Saga / workflow orchestration | Dùng Temporal; tự viết là dự án riêng 3 tháng |
| Feature flags | Dùng Unleash/Flagsmith hoặc config reload |

### Đã cân nhắc, hoãn lại (không phải loại bỏ)

| Hạng mục | Khi nào nên làm |
|---|---|
| `db/outbox` — transactional outbox | Khi có service ghi DB **và** publish Kafka trong cùng một luồng nghiệp vụ. Lúc đó thiếu outbox là để ngỏ lỗ đúng đắn dữ liệu: publish fail sau khi commit DB → mất event |
| Batch job runner có checkpoint | Khi có ETL / đối chiếu số liệu / xử lý file lớn cần chạy lại từ điểm dừng |
| Masking response theo role | Khi API cần trả field khác nhau tuỳ quyền người xem |

---

## 8. Rủi ro

| Rủi ro | Ảnh hưởng | Cách giảm |
|---|---|---|
| Đổi breaking change ở `core` sau Phase 3 | Phải release lại 7 module theo thứ tự | Đóng băng API `core` cuối Phase 1; viết `examples/api` sớm để phát hiện lỗi thiết kế |
| **Dung lượng log** (body bật cho mọi request) | Chi phí log tăng, pipeline chậm, có thể mất dòng log | Lớp 1 `MaxLen` (mặc định 256) + `MaxLineBytes` (32KB) là **bắt buộc, không phải tuỳ chọn**; `SkipPaths` cho health/metrics; theo dõi dung lượng log/RPS ngay từ Phase 2 |
| Buffer response lớn trong RAM | OOM khi trả file | `BodyLogOptions.MaxCapture` 64KB; response nhị phân chỉ log content-type + bytes |
| Lẫn hoa/thường trong module path | 2 module trùng trong build graph, lỗi "type X is not X" | Chỉ dùng `cqt002`; CI có bước grep chặn dạng hoa |
| `go.work` che lỗi version | Test pass local, người dùng thật build vỡ | CI có job `GOWORK=off` |
| Test integration làm CI chậm | Vòng phản hồi dài | Build tag `integration`; PR chạy unit, nightly chạy full |
| Lib phình thành "kitchen sink" | Không ai dám import | Mọi package mới phải trả lời được: "stdlib/thư viện OSS có sẵn không?" Nếu có, đừng bọc |
| Metrics nổ cardinality | Prometheus OOM | Bắt buộc label theo route pattern; review label mỗi khi thêm metric |
| Reflection trong `log.Safe` làm chậm | Tăng latency trên hot path | Cache tag theo `reflect.Type`; benchmark ở Phase 1 với struct 20 field lồng 3 tầng |
