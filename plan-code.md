# gokit — Plan phát triển

> Module: `github.com/cqt002/gokit`
> Cập nhật: 2026-08-20

---

## 0. Bảng theo dõi tiến độ

Ký hiệu: `⬜` chưa làm · `🔄` đang làm · `✅` xong

| Phase | Nội dung | Trạng thái |
|---|---|---|
| 0 | Bộ khung repo, go.mod, go.work, CI | ✅ |
| 1 | `core` — log/errs/config/trace/crypto... | 🔄 |
| 2 | `obs` + `httpx` — middleware, server, client | ⬜ |
| 3 | `db` + `cache` + `testx` | ⬜ |
| 4 | `kafka` | ⬜ |
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
├── kafka/         go.mod → .../kafka
│   └── deps: core, obs, franz-go, franz-go/plugin/kprom
│
├── testx/         go.mod → .../testx
│   └── deps: testcontainers-go, testify
│
└── examples/
    └── api/                   # service mẫu — nơi kiểm chứng thiết kế
```

**Đồ thị phụ thuộc** (một chiều, không có cycle):

```
core ← httpx ← ─┐
core ← db   ← ─ ┤
core ← cache ←  ├─ examples/api
core ← kafka ←  ┘
obs  ← {httpx, db, cache, kafka}
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

### 4.16 `kafka`

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

Chốt trong lúc làm:

| Hạng mục | Giá trị | Ghi chú |
|---|---|---|
| Go tối thiểu | `go 1.25.0` ở cả 8 `go.mod` | CI test matrix `1.25.x` × `stable` |
| golangci-lint | `v2.13.0`, pin ở `Makefile` + CI | v2.6.x không đọc được export data của Go 1.27 |
| CI | GitHub Actions — job `guard`, `test`, `lint`, `nowork`, `tidy`, `integration` | `integration` chỉ chạy nightly/dispatch |
| Build khi `GOWORK=off` | `go build -o /dev/null ./...` | `-o dir/` đòi main package; `./...` trần thì đụng thư mục `examples/api` |

> Còn một việc bằng tay: remote URL của `origin` vẫn đang viết hoa tên user. Chạy
> `git remote set-url origin https://github.com/cqt002/gokit.git` cho khớp module path.
> (Guard `check-module-path` quét cả `*.md`, nên đừng viết dạng viết hoa vào file này.)

### Phase 1 — `core` `🔄`

Thứ tự bắt buộc (do phụ thuộc):

- [x] `idx`
- [x] `secret`
- [x] `tracectx` (phụ thuộc `idx`)
- [x] `ctxmeta`
- [x] `errs`
- [ ] `log` — handler chain + **masking lớp 1** (elide theo kích thước)
- [ ] `log` — **masking lớp 2** (`Safe` đọc tag `log:`, cache theo `reflect.Type`)
- [ ] `log` — **masking lớp 3** (`SafeMap` theo tên field) + trần `MaxLineBytes`
- [ ] `config` (phụ thuộc `secret`)
- [ ] `crypto` — AES-GCM + key rotation, HMAC, argon2id
- [ ] `retry`
- [ ] `tlsx`
- [ ] `timex`

**Định nghĩa "xong":** coverage ≥ 80%, không cần Docker, godoc đầy đủ cho mọi symbol export.

Đã thêm ngoài đặc tả ở mục 4 (đều là bổ sung, không phá vỡ API đã chốt) — cần soi
lại khi đóng băng API cuối Phase 1:

| Package | Thêm | Lý do |
|---|---|---|
| `secret` | `MarshalYAML`, `IsZero`, `Equal` | Dump config ra YAML là đường lộ ngang hàng với JSON. `Equal` dùng `subtle.ConstantTimeCompare` — `httpx/auth` so khớp API key cần đúng thứ này, để mỗi service tự viết `==` là hở kênh phụ |
| `tracectx` | `Valid`, `String`, `TraceIDFrom`, `HeaderTraceparent`, `ErrInvalidTraceparent` | `Valid` là điều kiện dùng chung của `Traceparent`/`NewChild`; `HeaderTraceparent` để `httpx` và `kafka` không tự viết lại tên header |
| `errs` | `HTTPStatus(err)`, `WithCause`, `opts` cho `Wrap` | `httpx.Fail` cần lấy status từ error bất kỳ, kể cả error thường (→ 500) |

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

> **Cổng chặn:** API của `core` phải **đóng băng** trước khi sang Phase 3. Trong thiết kế multi-module, sửa breaking change ở `core` nghĩa là release `core`, rồi bump require + release lần lượt 6 module còn lại.

### Phase 2 — `obs` + `httpx` `⬜`

- [ ] `obs`: registry, `/metrics`, `HTTPMetrics`, `RegisterDBStats`, `RegisterRuntime`
- [ ] `httpx/middleware`: `Trace`
- [ ] `httpx/middleware`: `AccessLog`, `Recover`, `Timeout`, `CORS`, `MaxBodySize`
- [ ] `httpx/middleware`: `BodyLog` — holder trong context + fallback `SafeMap`
- [ ] `httpx/middleware`: `RateLimit` (in-process)
- [ ] `httpx`: `Envelope`, `Decode[T]` (+ đăng ký body đã mask), `OK`/`Created`/`Fail`
- [ ] `httpx`: error mapper từ `errs`
- [ ] `httpx/validate`: rule cắm thêm được
- [ ] `httpx/auth`: JWT, APIKey, BasicAuth
- [ ] `httpx/health`: `/healthz`, `/readyz`
- [ ] `httpx`: `Server` + `App` lifecycle (graceful shutdown)
- [ ] `httpx/client`: retry + breaker + propagate trace + mask log
- [ ] `httpx/idempotency`: middleware + `Store` interface + store in-memory
- [ ] `examples/api`: service chạy được thật — **nơi kiểm chứng thiết kế**

Test bằng `httptest`, không cần Docker.

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

### Phase 4 — `kafka` `⬜`

- [ ] `Producer` (sync + async), SASL/TLS
- [ ] Propagate trace qua Kafka header
- [ ] `Consumer` với handler + concurrency
- [ ] Retry + DLQ
- [ ] Metrics qua `kprom`
- [ ] Test bằng `kfake` của franz-go + 1 test integration thật

### Phase 5 — Hoàn thiện `⬜`

- [ ] Godoc mọi symbol export + `Example` function
- [ ] `examples/`: consumer, cron job
- [ ] Tag `core/v0.1.0`, `obs/v0.1.0`, ... (**tag có prefix theo tên module**)
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
| Integration | `db`, `cache`, `kafka` (testcontainers) | Có | PR + nightly |
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
