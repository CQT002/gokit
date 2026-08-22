// Package auth cung cấp xác thực cho HTTP: JWT, API key và Basic auth.
//
// Mọi hàm dựng đều trả về error thay vì panic. Cấu hình xác thực đến từ file config
// và biến môi trường, tức là từ ngoài code — và một service panic lúc khởi động vì
// khai sai một dòng cấu hình thì khó chẩn đoán hơn nhiều so với một thông báo lỗi.
package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/cqt002/gokit/core/ctxmeta"
	"github.com/cqt002/gokit/core/idx"
	"github.com/cqt002/gokit/core/secret"
)

// Các thuật toán được hỗ trợ.
//
// Cố tình không hỗ trợ "none" và không cho phép chọn thuật toán từ header của token:
// đó là hai lỗ hổng kinh điển của JWT. Thuật toán do server khai, và token khai
// thuật toán khác sẽ bị từ chối.
const (
	AlgHS256 = "HS256"
	AlgHS384 = "HS384"
	AlgHS512 = "HS512"
	AlgRS256 = "RS256"
	AlgRS384 = "RS384"
	AlgRS512 = "RS512"
	AlgES256 = "ES256"
	AlgES384 = "ES384"
	AlgES512 = "ES512"
)

// JWTConfig cấu hình JWT.
type JWTConfig struct {
	// Algorithm là thuật toán ký. Rỗng thì dùng AlgHS256.
	Algorithm string

	// Secret là khoá HMAC, dùng với các thuật toán HS*.
	Secret secret.Secret

	// PrivateKeyPEM là khoá riêng dạng PEM, dùng để ký với RS*/ES*.
	PrivateKeyPEM []byte

	// PublicKeyPEM là khoá công khai dạng PEM, dùng để verify với RS*/ES*.
	PublicKeyPEM []byte

	// Issuer là giá trị claim iss. Khác rỗng thì verify sẽ kiểm tra.
	Issuer string

	// Audience là các giá trị claim aud được chấp nhận.
	Audience []string

	// TTL là thời hạn mặc định của token do Sign tạo. <= 0 thì dùng 15 phút.
	TTL time.Duration

	// Leeway là biên độ lệch đồng hồ cho phép khi kiểm tra exp và nbf.
	// <= 0 thì dùng DefaultLeeway.
	//
	// Cần thiết vì đồng hồ của các máy không bao giờ khớp tuyệt đối, và một token
	// bị từ chối vì lệch 200ms là lỗi rất khó tin khi gặp.
	Leeway time.Duration
}

// Giá trị mặc định của JWTConfig.
const (
	DefaultTTL     = 15 * time.Minute
	DefaultLeeway  = 30 * time.Second
	headerBearer   = "Bearer "
	headerAuthName = "Authorization"
)

// Claims là nội dung đã verify của một token.
type Claims struct {
	// Subject là claim sub, thường là user ID.
	Subject string
	// Issuer là claim iss.
	Issuer string
	// Audience là claim aud.
	Audience []string
	// ID là claim jti.
	ID string
	// ExpiresAt là claim exp.
	ExpiresAt time.Time
	// IssuedAt là claim iat.
	IssuedAt time.Time
	// Raw là toàn bộ claim, gồm cả claim riêng của app.
	Raw map[string]any
}

// String lấy một claim dạng chuỗi. ok = false nếu không có hoặc không phải chuỗi.
func (c Claims) String(name string) (string, bool) {
	v, ok := c.Raw[name].(string)
	return v, ok
}

// JWT ký và verify token.
//
// An toàn khi dùng từ nhiều goroutine.
type JWT struct {
	method     jwt.SigningMethod
	signKey    any
	verifyKeys []any
	cfg        JWTConfig
	ttl        time.Duration
	leeway     time.Duration
}

// NewJWT dựng JWT từ cấu hình.
//
// Với HS*: cần Secret. Với RS*/ES*: cần PublicKeyPEM để verify, và PrivateKeyPEM
// nếu muốn ký. Service chỉ verify token do hệ thống khác phát thì không cần khoá
// riêng — và không nên có.
func NewJWT(cfg JWTConfig) (*JWT, error) {
	alg := cfg.Algorithm
	if alg == "" {
		alg = AlgHS256
	}

	method := jwt.GetSigningMethod(alg)
	if method == nil {
		return nil, fmt.Errorf("auth: thuật toán %q không được hỗ trợ", alg)
	}
	if alg == "none" {
		return nil, errors.New("auth: thuật toán none không bao giờ được chấp nhận")
	}

	j := &JWT{
		method: method,
		cfg:    cfg,
		ttl:    cfg.TTL,
		leeway: cfg.Leeway,
	}
	if j.ttl <= 0 {
		j.ttl = DefaultTTL
	}
	if j.leeway <= 0 {
		j.leeway = DefaultLeeway
	}

	if err := j.loadKeys(alg); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *JWT) loadKeys(alg string) error {
	switch {
	case strings.HasPrefix(alg, "HS"):
		if j.cfg.Secret.IsZero() {
			return errors.New("auth: thuật toán HMAC cần Secret")
		}
		key := []byte(j.cfg.Secret.Reveal())
		// 32 byte là mức tối thiểu hợp lý cho HS256: khoá ngắn làm HMAC bị brute
		// force, và đây là loại sai không có triệu chứng nào cho tới khi bị khai thác.
		if len(key) < 32 {
			return fmt.Errorf("auth: Secret cho HMAC dài %d byte, cần ít nhất 32", len(key))
		}
		j.signKey = key
		j.verifyKeys = []any{key}
		return nil

	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "ES"):
		if len(j.cfg.PublicKeyPEM) == 0 && len(j.cfg.PrivateKeyPEM) == 0 {
			return fmt.Errorf("auth: thuật toán %s cần PublicKeyPEM hoặc PrivateKeyPEM", alg)
		}
		if len(j.cfg.PrivateKeyPEM) > 0 {
			priv, err := parsePrivateKey(alg, j.cfg.PrivateKeyPEM)
			if err != nil {
				return err
			}
			j.signKey = priv
			j.verifyKeys = append(j.verifyKeys, publicOf(priv))
		}
		if len(j.cfg.PublicKeyPEM) > 0 {
			pub, err := parsePublicKey(alg, j.cfg.PublicKeyPEM)
			if err != nil {
				return err
			}
			j.verifyKeys = append(j.verifyKeys, pub)
		}
		return nil

	default:
		return fmt.Errorf("auth: thuật toán %q không được hỗ trợ", alg)
	}
}

func parsePrivateKey(alg string, pem []byte) (any, error) {
	if strings.HasPrefix(alg, "RS") {
		key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
		if err != nil {
			return nil, fmt.Errorf("auth: đọc khoá riêng RSA: %w", err)
		}
		return key, nil
	}
	key, err := jwt.ParseECPrivateKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("auth: đọc khoá riêng ECDSA: %w", err)
	}
	return key, nil
}

func parsePublicKey(alg string, pem []byte) (any, error) {
	if strings.HasPrefix(alg, "RS") {
		key, err := jwt.ParseRSAPublicKeyFromPEM(pem)
		if err != nil {
			return nil, fmt.Errorf("auth: đọc khoá công khai RSA: %w", err)
		}
		return key, nil
	}
	key, err := jwt.ParseECPublicKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("auth: đọc khoá công khai ECDSA: %w", err)
	}
	return key, nil
}

func publicOf(priv any) any {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}

// Sign tạo token với claims cho trước.
//
// Tự điền iat, exp và jti nếu chưa có; iss và aud lấy từ cấu hình. ttl <= 0 thì
// dùng TTL trong cấu hình.
//
// jti được sinh tự động vì nó là điều kiện để thu hồi token — không có ID thì không
// có cách nào nói "riêng token này không còn hiệu lực".
func (j *JWT) Sign(claims map[string]any, ttl time.Duration) (string, error) {
	if j.signKey == nil {
		return "", errors.New("auth: không có khoá để ký, cấu hình chỉ đủ để verify")
	}
	if ttl <= 0 {
		ttl = j.ttl
	}

	now := time.Now()
	out := jwt.MapClaims{}
	for k, v := range claims {
		out[k] = v
	}

	setIfAbsent(out, "iat", now.Unix())
	setIfAbsent(out, "exp", now.Add(ttl).Unix())
	setIfAbsent(out, "jti", idx.NewUUIDv7())
	if j.cfg.Issuer != "" {
		setIfAbsent(out, "iss", j.cfg.Issuer)
	}
	if len(j.cfg.Audience) > 0 {
		setIfAbsent(out, "aud", j.cfg.Audience)
	}

	token, err := jwt.NewWithClaims(j.method, out).SignedString(j.signKey)
	if err != nil {
		return "", fmt.Errorf("auth: ký token: %w", err)
	}
	return token, nil
}

func setIfAbsent(m jwt.MapClaims, key string, value any) {
	if _, ok := m[key]; !ok {
		m[key] = value
	}
}

// ErrInvalidToken là lỗi gốc của mọi lỗi verify. Chi tiết cụ thể không đưa ra client.
var ErrInvalidToken = errors.New("auth: token không hợp lệ")

// Verify kiểm tra token và trả về claims.
//
// Bắt buộc token dùng đúng thuật toán đã khai. Không làm vậy thì một token ký bằng
// HMAC với khoá là chính public key của server sẽ được chấp nhận khi server cấu hình
// RSA — lỗ hổng "algorithm confusion" kinh điển của JWT.
//
// Thử lần lượt các khoá verify, nên đổi khoá được: khai khoá mới cùng khoá cũ, token
// đã phát vẫn dùng được tới khi hết hạn.
func (j *JWT) Verify(token string) (Claims, error) {
	var lastErr error

	for _, key := range j.verifyKeys {
		if key == nil {
			continue
		}

		opts := []jwt.ParserOption{
			jwt.WithValidMethods([]string{j.method.Alg()}),
			jwt.WithLeeway(j.leeway),
			jwt.WithExpirationRequired(),
		}
		if j.cfg.Issuer != "" {
			opts = append(opts, jwt.WithIssuer(j.cfg.Issuer))
		}
		for _, aud := range j.cfg.Audience {
			opts = append(opts, jwt.WithAudience(aud))
		}

		parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return key, nil }, opts...)
		if err != nil {
			lastErr = err
			continue
		}
		if mapClaims, ok := parsed.Claims.(jwt.MapClaims); ok {
			return claimsFrom(mapClaims), nil
		}
		lastErr = errors.New("claims không đúng định dạng")
	}

	if lastErr == nil {
		lastErr = errors.New("không có khoá nào để verify")
	}
	return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, lastErr)
}

func claimsFrom(m jwt.MapClaims) Claims {
	c := Claims{Raw: map[string]any(m)}
	c.Subject, _ = m["sub"].(string)
	c.Issuer, _ = m["iss"].(string)
	c.ID, _ = m["jti"].(string)

	switch aud := m["aud"].(type) {
	case string:
		c.Audience = []string{aud}
	case []any:
		for _, v := range aud {
			if s, ok := v.(string); ok {
				c.Audience = append(c.Audience, s)
			}
		}
	}
	c.ExpiresAt = unixTime(m["exp"])
	c.IssuedAt = unixTime(m["iat"])
	return c
}

func unixTime(v any) time.Time {
	switch n := v.(type) {
	case float64:
		return time.Unix(int64(n), 0)
	case int64:
		return time.Unix(n, 0)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return time.Unix(i, 0)
		}
	}
	return time.Time{}
}

type claimsKey struct{}

// ClaimsFrom lấy claims đã verify trong context, ok = false nếu request chưa qua
// middleware JWT.
func ClaimsFrom(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(Claims)
	return c, ok
}

// MWOption tinh chỉnh middleware.
type MWOption func(*mwConfig)

type mwConfig struct {
	optional   bool
	userIDFrom func(Claims) string
	userType   func(Claims) string
}

// Optional cho phép request không có token đi qua.
//
// Dùng cho endpoint mà người đã đăng nhập thấy nhiều hơn: có token thì verify và
// gắn claims, không có thì vẫn cho qua. Token **có nhưng sai** thì vẫn bị từ chối —
// nếu không, client sẽ không bao giờ biết token của mình đã hết hạn.
func Optional() MWOption {
	return func(c *mwConfig) { c.optional = true }
}

// WithUserID đổi cách lấy user ID từ claims để gắn vào ctxmeta. Mặc định dùng sub.
func WithUserID(fn func(Claims) string) MWOption {
	return func(c *mwConfig) { c.userIDFrom = fn }
}

// WithUserType đặt cách lấy loại người dùng từ claims để gắn vào ctxmeta.
func WithUserType(fn func(Claims) string) MWOption {
	return func(c *mwConfig) { c.userType = fn }
}

// Middleware verify Bearer token và gắn claims cùng user ID vào context.
//
// Trả 401 khi thiếu hoặc sai token. Không bao giờ trả về lý do cụ thể cho client:
// "token hết hạn" với "signature sai" là hai thông tin khác nhau đối với người đang
// thử tấn công. Lý do thật đi vào log.
func (j *JWT) Middleware(opts ...MWOption) func(http.Handler) http.Handler {
	cfg := mwConfig{userIDFrom: func(c Claims) string { return c.Subject }}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				if cfg.optional {
					next.ServeHTTP(w, r)
					return
				}
				writeUnauthorized(w, r, "thiếu token xác thực")
				return
			}

			claims, err := j.Verify(raw)
			if err != nil {
				writeUnauthorized(w, r, "token không hợp lệ hoặc đã hết hạn")
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey{}, claims)
			meta := ctxmeta.From(ctx)
			meta.UserID = cfg.userIDFrom(claims)
			if cfg.userType != nil {
				meta.UserType = cfg.userType(claims)
			}
			ctx = ctxmeta.With(ctx, meta)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken lấy token từ header Authorization.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get(headerAuthName)
	if len(h) <= len(headerBearer) || !strings.EqualFold(h[:len(headerBearer)], headerBearer) {
		return "", false
	}
	token := strings.TrimSpace(h[len(headerBearer):])
	return token, token != ""
}
