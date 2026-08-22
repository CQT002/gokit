// Command api là service mẫu dùng gokit, và là nơi kiểm chứng thiết kế.
//
// Mục đích của nó không phải để chạy production mà để trả lời câu hỏi: ghép các
// package của gokit lại thành một service thật thì có chỗ nào khó dùng không. Lỗi
// thiết kế API chỉ lộ ra khi có người dùng thật, và ở giai đoạn này người dùng thật
// đầu tiên là file này.
//
// Chạy:
//
//	go run ./api
//	curl -i localhost:8080/healthz
//	curl -i -X POST localhost:8080/v1/orders -H 'Idempotency-Key: k1' \
//	  -H 'Content-Type: application/json' -d '{"amount":1000,"note":"thử"}'
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/core/secret"
	"github.com/cqt002/gokit/httpx"
	"github.com/cqt002/gokit/httpx/auth"
	"github.com/cqt002/gokit/httpx/health"
	"github.com/cqt002/gokit/httpx/idempotency"
	"github.com/cqt002/gokit/httpx/middleware"
	"github.com/cqt002/gokit/obs"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("service dừng vì lỗi", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	logger := log.New(log.Options{
		AppName: "example-api",
		Level:   slog.LevelInfo,
	})

	registry := obs.NewRegistry()
	if err := obs.RegisterRuntime(registry, obs.RuntimeOptions{}); err != nil {
		return err
	}

	jwt, err := auth.NewJWT(auth.JWTConfig{
		// Trong service thật, khoá này đến từ config hoặc vault, không hardcode.
		Secret: secret.Secret("khoa-hmac-chi-dung-cho-vi-du-32-byte"),
		Issuer: "example-api",
		TTL:    15 * time.Minute,
	})
	if err != nil {
		return err
	}

	hc := health.NewHealth()
	hc.Register("phu-thuoc-gia-lap", func(context.Context) error { return nil })

	idemMW, err := idempotency.Middleware(idempotency.Config{
		Store:    idempotency.NewMemoryStore(),
		Required: true,
		Logger:   logger,
	})
	if err != nil {
		return err
	}

	corsMW, err := middleware.CORS(middleware.CORSConfig{
		AllowedOrigins: []string{"http://localhost:3000"},
		MaxAge:         time.Hour,
	})
	if err != nil {
		return err
	}

	rateMW, err := middleware.RateLimit(middleware.RateLimitConfig{
		Requests: 100,
		Window:   time.Minute,
	})
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	hc.Handle(mux)
	mux.Handle("GET /metrics", obs.Handler(registry))

	mux.HandleFunc("POST /v1/tokens", handleIssueToken(jwt))
	mux.Handle("POST /v1/orders", idemMW(http.HandlerFunc(handleCreateOrder)))
	mux.HandleFunc("GET /v1/orders", handleListOrders)
	mux.Handle("GET /v1/me", jwt.Middleware()(http.HandlerFunc(handleMe)))

	// Thứ tự này là thứ tự khuyến nghị ở godoc của package middleware.
	handler := middleware.Chain(
		middleware.Recover(logger),
		startClock,
		middleware.Trace(middleware.TraceOptions{}),
		middleware.AccessLog(logger, middleware.LogOptions{
			SkipPaths:     []string{"/healthz", "/readyz", "/metrics"},
			SlowThreshold: time.Second,
		}),
		obs.HTTPMetrics(registry, obs.HTTPOptions{RoutePattern: obs.ServeMuxRoute}),
		corsMW,
		rateMW,
		middleware.MaxBodySize(1<<20),
		middleware.BodyLog(logger, middleware.BodyLogOptions{
			SkipPaths: []string{"/healthz", "/readyz", "/metrics"},
			// Field riêng của nghiệp vụ, thêm vào danh sách mặc định (không thay
			// thế). Cần thiết cho những đường không đi qua Decode — request bị
			// middleware từ chối, hoặc request được idempotency phát lại — vì ở đó
			// không có type nào để đọc tag `log:`.
			Mask: log.MaskConfig{
				Fields: map[string]log.Rule{
					"so_tai_khoan": log.RuleRedact,
					"cccd":         log.RuleRedact,
				},
			},
		}),
		middleware.Timeout(10*time.Second),
	)(mux)

	srv, err := httpx.NewServer(httpx.ServerConfig{
		Addr:    addrFromEnv(),
		Handler: handler,
		Logger:  logger,
		BeforeShutdown: func(ctx context.Context) {
			// Rút khỏi load balancer trước, rồi chờ nó nhận ra, rồi mới đóng server.
			hc.SetNotReady()
			logger.Info("đã đánh dấu không sẵn sàng, chờ load balancer rút traffic")
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
		},
	})
	if err != nil {
		return err
	}

	app := httpx.NewApp(logger)
	app.Add("http", srv.Run, nil)
	return app.RunWithSignals(context.Background())
}

func addrFromEnv() string {
	if addr := os.Getenv("ADDR"); addr != "" {
		return addr
	}
	return ":8080"
}

// startClock gắn mốc bắt đầu để Envelope điền được elapsed_ms.
func startClock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, httpx.WithStart(r, time.Now()))
	})
}

// ---------- handler ----------

type loginRequest struct {
	Username string        `json:"username" validate:"required"`
	Password secret.Secret `json:"password" validate:"required"`
}

func handleIssueToken(jwt *auth.JWT) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := httpx.Decode[loginRequest](r)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}

		// Service thật tra người dùng trong DB rồi crypto.VerifyPassword.
		if req.Username != "demo" {
			httpx.Fail(w, r, errs.New(errs.CodeUnauthorized, "sai tên đăng nhập hoặc mật khẩu"))
			return
		}

		token, err := jwt.Sign(map[string]any{"sub": req.Username, "typ": "customer"}, 0)
		if err != nil {
			httpx.Fail(w, r, errs.Wrap(err, errs.CodeInternal, ""))
			return
		}
		httpx.OK(w, r, map[string]any{"access_token": token, "token_type": "Bearer"})
	}
}

type createOrderRequest struct {
	Amount int64  `json:"amount" validate:"required,gt=0"`
	Note   string `json:"note" log:"truncate=64"`
	// Số thẻ chỉ để minh hoạ masking; service thật không nhận số thẻ trực tiếp.
	CardNo string `json:"card_no,omitempty" log:"edges=6,4"`
}

func handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	req, err := httpx.Decode[createOrderRequest](r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if req.Amount > 1_000_000_000 {
		httpx.Fail(w, r, errs.New(errs.CodeValidation, "số tiền vượt hạn mức một giao dịch",
			errs.WithField("amount", "tối đa 1.000.000.000")))
		return
	}

	httpx.Created(w, r, map[string]any{
		"order_id": "od-" + time.Now().Format("150405"),
		"amount":   req.Amount,
	})
}

type listOrdersRequest struct {
	Page int `query:"page" validate:"gte=1"`
	Size int `query:"size" validate:"gte=1,lte=100"`
}

func handleListOrders(w http.ResponseWriter, r *http.Request) {
	req, err := httpx.DecodeQuery[listOrdersRequest](r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, r, map[string]any{"page": req.Page, "size": req.Size, "items": []any{}})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		// Không xảy ra khi handler nằm sau middleware JWT, nhưng để lộ ra rõ ràng
		// còn hơn trả về dữ liệu rỗng.
		httpx.Fail(w, r, errors.New("thiếu claims trong context"))
		return
	}
	httpx.OK(w, r, map[string]any{"user_id": claims.Subject, "expires_at": claims.ExpiresAt})
}
