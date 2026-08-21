package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/cqt002/gokit/core/ctxmeta"
	"github.com/cqt002/gokit/httpx/auth"
)

const hmacSecret = "khoa-hmac-du-dai-cho-hs256-32-byte!!"

func mustJWT(t *testing.T, cfg auth.JWTConfig) *auth.JWT {
	t.Helper()
	j, err := auth.NewJWT(cfg)
	if err != nil {
		t.Fatalf("NewJWT: %v", err)
	}
	return j
}

func TestJWT_VongTronKyVaVerify(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{
		Secret:   hmacSecret,
		Issuer:   "gokit-test",
		Audience: []string{"api"},
	})

	token, err := j.Sign(map[string]any{"sub": "user-1", "role": "admin"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := j.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q", claims.Subject)
	}
	if claims.Issuer != "gokit-test" {
		t.Errorf("Issuer = %q", claims.Issuer)
	}
	if role, ok := claims.String("role"); !ok || role != "admin" {
		t.Errorf("claim role = %q, ok = %v", role, ok)
	}
	// jti tự sinh: không có ID thì không có cách nào thu hồi riêng một token.
	if claims.ID == "" {
		t.Error("thiếu jti")
	}
	if claims.ExpiresAt.IsZero() || claims.IssuedAt.IsZero() {
		t.Error("thiếu exp hoặc iat")
	}
}

func TestJWT_TuChoiTokenSai(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret})
	valid, err := j.Sign(map[string]any{"sub": "u"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	other := mustJWT(t, auth.JWTConfig{Secret: "mot-khoa-hoan-toan-khac-du-32-byte!!!"})
	otherToken, err := other.Sign(map[string]any{"sub": "u"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	tests := []struct {
		name  string
		token string
	}{
		{"rỗng", ""},
		{"rác", "khong-phai-jwt"},
		{"thiếu phần", strings.Join(strings.Split(valid, ".")[:2], ".")},
		{"signature bị sửa", valid[:len(valid)-4] + "AAAA"},
		{"ký bằng khoá khác", otherToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := j.Verify(tt.token); err == nil {
				t.Error("token sai được chấp nhận")
			}
		})
	}
}

func TestJWT_TokenHetHan(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret, Leeway: time.Nanosecond})

	token, err := j.Sign(map[string]any{"sub": "u", "exp": time.Now().Add(-time.Hour).Unix()}, 0)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := j.Verify(token); err == nil {
		t.Error("token đã hết hạn được chấp nhận")
	}
}

// Token không có exp phải bị từ chối: một token vĩnh viễn là một token không thu
// hồi được.
func TestJWT_TokenKhongCoExp(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret})

	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "u"})
	token, err := raw.SignedString([]byte(hmacSecret))
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	if _, err := j.Verify(token); err == nil {
		t.Error("token không có exp được chấp nhận")
	}
}

// Lỗ hổng algorithm confusion: token ký HMAC với khoá là public key của server phải
// bị từ chối khi server cấu hình RSA/ECDSA.
func TestJWT_ChanAlgorithmConfusion(t *testing.T) {
	privPEM, pubPEM := ecKeyPair(t)

	j := mustJWT(t, auth.JWTConfig{
		Algorithm:     auth.AlgES256,
		PrivateKeyPEM: privPEM,
		PublicKeyPEM:  pubPEM,
	})

	// Kẻ tấn công ký bằng HS256 với khoá chính là public key dạng PEM.
	raw := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "ke-tan-cong",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	forged, err := raw.SignedString(pubPEM)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	if _, err := j.Verify(forged); err == nil {
		t.Fatal("token HS256 được chấp nhận khi server cấu hình ES256 — lỗ hổng algorithm confusion")
	}
}

// Thuật toán none là lỗ hổng kinh điển: nó biến JWT thành một chuỗi ai cũng viết được.
func TestJWT_TuChoiThuatToanNone(t *testing.T) {
	if _, err := auth.NewJWT(auth.JWTConfig{Algorithm: "none"}); err == nil {
		t.Error("thuật toán none được chấp nhận")
	}
}

func TestJWT_CauHinhSai(t *testing.T) {
	tests := []struct {
		name string
		cfg  auth.JWTConfig
	}{
		{"HMAC thiếu secret", auth.JWTConfig{Algorithm: auth.AlgHS256}},
		{"HMAC secret quá ngắn", auth.JWTConfig{Algorithm: auth.AlgHS256, Secret: "ngan"}},
		{"thuật toán lạ", auth.JWTConfig{Algorithm: "MAGIC512", Secret: hmacSecret}},
		{"ES256 thiếu khoá", auth.JWTConfig{Algorithm: auth.AlgES256}},
		{"khoá PEM rác", auth.JWTConfig{Algorithm: auth.AlgES256, PublicKeyPEM: []byte("khong phai PEM")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := auth.NewJWT(tt.cfg); err == nil {
				t.Error("muốn lỗi, không có lỗi")
			}
		})
	}
}

// Service chỉ verify token của hệ thống khác thì không cần khoá riêng, và không nên có.
func TestJWT_ChiVerifyKhongKy(t *testing.T) {
	privPEM, pubPEM := ecKeyPair(t)

	signer := mustJWT(t, auth.JWTConfig{Algorithm: auth.AlgES256, PrivateKeyPEM: privPEM})
	verifier := mustJWT(t, auth.JWTConfig{Algorithm: auth.AlgES256, PublicKeyPEM: pubPEM})

	token, err := signer.Sign(map[string]any{"sub": "u"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := verifier.Verify(token); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if _, err := verifier.Sign(map[string]any{"sub": "u"}, time.Hour); err == nil {
		t.Error("ký được dù chỉ có khoá công khai")
	}
}

func TestJWT_KiemTraIssuerVaAudience(t *testing.T) {
	signer := mustJWT(t, auth.JWTConfig{Secret: hmacSecret, Issuer: "dung", Audience: []string{"api"}})
	token, err := signer.Sign(map[string]any{"sub": "u"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	strictIssuer := mustJWT(t, auth.JWTConfig{Secret: hmacSecret, Issuer: "khac"})
	if _, err := strictIssuer.Verify(token); err == nil {
		t.Error("issuer sai được chấp nhận")
	}

	strictAud := mustJWT(t, auth.JWTConfig{Secret: hmacSecret, Audience: []string{"khac"}})
	if _, err := strictAud.Verify(token); err == nil {
		t.Error("audience sai được chấp nhận")
	}
}

// Đổi khoá: khai khoá mới cùng khoá cũ thì token đã phát vẫn dùng được tới khi hết hạn.
func TestJWT_DoiKhoa(t *testing.T) {
	privOld, pubOld := ecKeyPair(t)
	privNew, pubNew := ecKeyPair(t)

	oldSigner := mustJWT(t, auth.JWTConfig{Algorithm: auth.AlgES256, PrivateKeyPEM: privOld})
	oldToken, err := oldSigner.Sign(map[string]any{"sub": "u"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Sau khi đổi: khoá mới để ký, khoá cũ vẫn verify được.
	rotated := mustJWT(t, auth.JWTConfig{
		Algorithm:     auth.AlgES256,
		PrivateKeyPEM: privNew,
		PublicKeyPEM:  pubOld,
	})
	if _, verifyErr := rotated.Verify(oldToken); verifyErr != nil {
		t.Errorf("token của khoá cũ không verify được sau khi đổi: %v", verifyErr)
	}

	newToken, err := rotated.Sign(map[string]any{"sub": "u"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	newOnly := mustJWT(t, auth.JWTConfig{Algorithm: auth.AlgES256, PublicKeyPEM: pubNew})
	if _, err := newOnly.Verify(newToken); err != nil {
		t.Errorf("token mới không verify được bằng khoá mới: %v", err)
	}
}

// ---------- Middleware ----------

func TestJWT_Middleware(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret})
	token, err := j.Sign(map[string]any{"sub": "user-1", "typ": "employee"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var gotClaims auth.Claims
	var gotUserID, gotUserType string
	h := j.Middleware(auth.WithUserType(func(c auth.Claims) string {
		v, _ := c.String("typ")
		return v
	}))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClaims, _ = auth.ClaimsFrom(r.Context())
		gotUserID = ctxmeta.UserID(r.Context())
		gotUserType = ctxmeta.UserType(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotClaims.Subject != "user-1" {
		t.Errorf("claims.Subject = %q", gotClaims.Subject)
	}
	if gotUserID != "user-1" {
		t.Errorf("ctxmeta.UserID = %q — log sẽ không có user_id", gotUserID)
	}
	if gotUserType != "employee" {
		t.Errorf("ctxmeta.UserType = %q", gotUserType)
	}
}

func TestJWT_Middleware_TuChoi(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret})
	h := j.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler được gọi dù token sai")
	}))

	tests := []struct {
		name   string
		header string
	}{
		{"không có header", ""},
		{"thiếu tiền tố Bearer", "abc.def.ghi"},
		{"Bearer rỗng", "Bearer "},
		{"token rác", "Bearer khong-phai-jwt"},
		{"scheme khác", "Basic YWJjOmRlZg=="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, muốn 401", rec.Code)
			}
			// Thông báo phải chung chung: phân biệt "hết hạn" với "signature sai" là
			// thông tin có giá trị với người đang thử tấn công.
			var env map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &env)
			if msg, _ := env["message"].(string); strings.Contains(msg, "signature") {
				t.Errorf("message = %q, lộ lý do cụ thể", msg)
			}
		})
	}
}

// Bearer viết hoa thường khác nhau vẫn phải nhận: đặc tả HTTP nói scheme không phân
// biệt hoa thường.
func TestJWT_Middleware_BearerKhongPhanBietHoaThuong(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret})
	token, _ := j.Sign(map[string]any{"sub": "u"}, time.Hour)

	for _, prefix := range []string{"Bearer ", "bearer ", "BEARER "} {
		h := j.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", prefix+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("prefix %q: status = %d", prefix, rec.Code)
		}
	}
}

// Optional: không có token thì cho qua, nhưng token SAI thì vẫn từ chối — nếu không,
// client sẽ không bao giờ biết token của mình đã hết hạn.
func TestJWT_Middleware_Optional(t *testing.T) {
	j := mustJWT(t, auth.JWTConfig{Secret: hmacSecret})

	reached := false
	h := j.Middleware(auth.Optional())(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !reached || rec.Code != http.StatusOK {
		t.Errorf("không có token phải được đi qua, status = %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer token-sai")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("token sai với Optional: status = %d, muốn 401", rec2.Code)
	}
}

func TestClaimsFrom_KhongCoMiddleware(t *testing.T) {
	if _, ok := auth.ClaimsFrom(context.Background()); ok {
		t.Error("ClaimsFrom trả ok = true khi chưa qua middleware")
	}
}

// ---------- APIKey ----------

func TestAPIKey(t *testing.T) {
	h := auth.APIKey("", func(_ context.Context, key string) bool {
		return key == "key-dung"
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name   string
		key    string
		status int
	}{
		{"key đúng", "key-dung", http.StatusOK},
		{"key sai", "key-sai", http.StatusUnauthorized},
		{"không có key", "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.key != "" {
				req.Header.Set("X-API-Key", tt.key)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Errorf("status = %d, muốn %d", rec.Code, tt.status)
			}
		})
	}
}

func TestAPIKey_HeaderTuKhai(t *testing.T) {
	h := auth.APIKey("X-Partner-Token", func(_ context.Context, key string) bool {
		return key == "abc"
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Partner-Token", "abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestAPIKey_ValidateNil(t *testing.T) {
	h := auth.APIKey("", nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("validate nil mà vẫn cho qua")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "bat-ky")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, muốn 401", rec.Code)
	}
}

// ---------- BasicAuth ----------

func TestBasicAuth(t *testing.T) {
	h := auth.BasicAuth("khu-vuc-noi-bo", func(_ context.Context, user, pass string) bool {
		return user == "admin" && pass == "mat-khau"
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	t.Run("đúng", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "mat-khau")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d", rec.Code)
		}
	})

	t.Run("sai", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("admin", "sai")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, muốn 401", rec.Code)
		}
		// Thiếu WWW-Authenticate thì client HTTP thông thường không biết phải gửi
		// lại kèm thông tin đăng nhập.
		challenge := rec.Header().Get("WWW-Authenticate")
		if !strings.Contains(challenge, "khu-vuc-noi-bo") {
			t.Errorf("WWW-Authenticate = %q", challenge)
		}
	})

	t.Run("không có header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d", rec.Code)
		}
	})
}

// ecKeyPair sinh cặp khoá ECDSA P-256 dạng PEM.
func ecKeyPair(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("sinh khoá: %v", err)
	}
	privDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal khoá riêng: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal khoá công khai: %v", err)
	}

	privPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return privPEM, pubPEM
}

// RS256 là họ thuật toán được hỗ trợ nhưng đi qua đường code khác hẳn ES256, nên
// phải có test riêng — không thì một nửa số nhánh loadKeys chưa từng chạy.
func TestJWT_RSA(t *testing.T) {
	privPEM, pubPEM := rsaKeyPair(t)

	signer := mustJWT(t, auth.JWTConfig{Algorithm: auth.AlgRS256, PrivateKeyPEM: privPEM})
	verifier := mustJWT(t, auth.JWTConfig{Algorithm: auth.AlgRS256, PublicKeyPEM: pubPEM})

	token, err := signer.Sign(map[string]any{"sub": "user-rsa"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	claims, err := verifier.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-rsa" {
		t.Errorf("Subject = %q", claims.Subject)
	}

	// Khoá riêng cũng đủ để verify: publicOf lấy được public key từ nó.
	if _, selfErr := signer.Verify(token); selfErr != nil {
		t.Errorf("verify bằng chính khoá riêng: %v", selfErr)
	}

	// Và cùng lỗ hổng algorithm confusion phải bị chặn ở nhánh RSA.
	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "ke-tan-cong",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	forgedToken, err := forged.SignedString(pubPEM)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	if _, err := verifier.Verify(forgedToken); err == nil {
		t.Fatal("token HS256 được chấp nhận khi server cấu hình RS256")
	}
}

func TestJWT_RSAKhoaSai(t *testing.T) {
	_, ecPub := ecKeyPair(t)

	// Khoá ECDSA khai cho thuật toán RSA: phải lỗi lúc dựng, không phải lúc verify.
	if _, err := auth.NewJWT(auth.JWTConfig{
		Algorithm:    auth.AlgRS256,
		PublicKeyPEM: ecPub,
	}); err == nil {
		t.Error("khoá ECDSA được nhận cho thuật toán RS256")
	}
}

// rsaKeyPair sinh cặp khoá RSA 2048 bit dạng PEM.
func rsaKeyPair(t *testing.T) (privPEM, pubPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("sinh khoá RSA: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal khoá công khai: %v", err)
	}

	privPEM = pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pubPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return privPEM, pubPEM
}
