package middleware_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/httpx/middleware"
)

// bodyLine tìm dòng log của BodyLog.
func bodyLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	for _, m := range lines(t, buf) {
		if m["msg"] == "body" {
			return m
		}
	}
	t.Fatalf("không thấy dòng log body:\n%s", buf.String())
	return nil
}

// Tầng bảo đảm: handler không làm gì đặc biệt thì vẫn phải có log body.
func TestBodyLog_LuonCoLogDuHandlerKhongDangKy(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"result":"xong"}`))
		}))

	req := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader(`{"name":"an"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)

	m := bodyLine(t, buf)
	reqBody, ok := m["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v, muốn object", m["request"])
	}
	if reqBody["name"] != "an" {
		t.Errorf("request.name = %#v", reqBody["name"])
	}
	respBody, ok := m["response"].(map[string]any)
	if !ok || respBody["result"] != "xong" {
		t.Errorf("response = %#v", m["response"])
	}
}

// Handler phải nhận được body nguyên vẹn sau khi middleware đã đọc để log.
func TestBodyLog_HandlerVanDocDuocBody(t *testing.T) {
	const payload = `{"name":"an","note":"còn nguyên"}`

	l, _ := newLogger(t)
	var got string
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			got = string(b)
		}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != payload {
		t.Errorf("handler nhận %q, muốn %q", got, payload)
	}
}

// Body dài hơn MaxCapture: log bị cắt nhưng handler vẫn nhận đủ.
func TestBodyLog_HandlerNhanDuBodyKhiVuotMaxCapture(t *testing.T) {
	payload := strings.Repeat("x", 5000)

	l, buf := newLogger(t)
	var gotLen int
	h := middleware.BodyLog(l, middleware.BodyLogOptions{MaxCapture: 100})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			gotLen = len(b)
		}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotLen != len(payload) {
		t.Errorf("handler nhận %d byte, muốn %d — middleware đã ăn mất phần body", gotLen, len(payload))
	}

	m := bodyLine(t, buf)
	reqField, ok := m["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v", m["request"])
	}
	if reqField["truncated"] != true {
		t.Errorf("thiếu cờ truncated: %#v — JSON thiếu nửa cuối trông y như JSON hỏng", reqField)
	}
}

// Tầng chất lượng: bản đăng ký theo tag phải thắng bản mask từ raw.
func TestBodyLog_BanDangKyThangBanRaw(t *testing.T) {
	type req struct {
		Name   string `json:"name"`
		Note   string `json:"note" log:"redact"`
		CardNo string `json:"card_no" log:"edges=6,4"`
	}

	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			// Giả lập việc httpx.Decode làm.
			middleware.SetRequestBody(r.Context(), log.Safe(req{
				Name:   "an",
				Note:   "thông tin nội bộ",
				CardNo: "4111111111111111",
			}))
			w.WriteHeader(http.StatusOK)
		}))

	body := `{"name":"an","note":"thông tin nội bộ","card_no":"4111111111111111"}`
	httpReq := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), httpReq)

	out := buf.String()
	if strings.Contains(out, "thông tin nội bộ") {
		t.Fatalf("tag log:\"redact\" không được áp — bản raw đã thắng:\n%s", out)
	}
	if strings.Contains(out, "4111111111111111") {
		t.Fatalf("số thẻ lọt nguyên vẹn:\n%s", out)
	}
	if !strings.Contains(out, "411111******1111") {
		t.Errorf("thiếu bản đã che theo edges:\n%s", out)
	}
}

// Không đăng ký gì thì lớp 3 (theo tên field) phải bắt được.
func TestBodyLog_FallbackCheTheoTenField(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	body := `{"user":"an","password":"mat-khau-that","token":"token-that"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	for _, leaked := range []string{"mat-khau-that", "token-that"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("%q lọt vào log:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, `"user":"an"`) {
		t.Errorf("field không nhạy cảm bị che oan:\n%s", out)
	}
}

// Số lớn trong JSON không được biến thành số thực: mất mấy chữ số cuối là mất đúng
// những chữ số cần để tra cứu.
func TestBodyLog_GiuNguyenSoLon(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	const id = "1234567890123456789"
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":`+id+`}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), id) {
		t.Errorf("ID %s bị đổi dạng trong log:\n%s", id, buf.String())
	}
}

func TestBodyLog_NoiDungNhiPhanChiGhiKichThuoc(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(bytes.Repeat([]byte{0x89, 0x50}, 500))
		}))

	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(bytes.Repeat([]byte{0xff}, 300)))
	req.Header.Set("Content-Type", "application/octet-stream")
	h.ServeHTTP(httptest.NewRecorder(), req)

	m := bodyLine(t, buf)
	for _, key := range []string{"request", "response"} {
		field, ok := m[key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %#v, muốn object metadata", key, m[key])
		}
		if field["content_type"] == nil || field["bytes"] == nil {
			t.Errorf("%s = %#v, thiếu content_type hoặc bytes", key, field)
		}
	}
}

// Upload file là chỗ hay lỗi nhất mà thường không có log nào.
func TestBodyLog_MultipartGhiMetadata(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("image", "anh.jpg")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte{0xff}, 2048)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := mw.WriteField("user_id", "u_123"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	for _, want := range []string{"anh.jpg", "image", "user_id", "u_123"} {
		if !strings.Contains(out, want) {
			t.Errorf("thiếu %q trong log multipart:\n%s", want, out)
		}
	}
	// Nội dung file không được vào log.
	if strings.Count(out, `\ufffd`) > 10 || strings.Contains(out, strings.Repeat("\xff", 10)) {
		t.Errorf("nội dung file lọt vào log:\n%s", out)
	}
}

// Handler không parse form thì vẫn phải có dòng log, kèm ghi chú vì sao thiếu metadata.
func TestBodyLog_MultipartKhongParse(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("a", "b")
	_ = mw.Close()

	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	h.ServeHTTP(httptest.NewRecorder(), req)

	m := bodyLine(t, buf)
	field, ok := m["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v", m["request"])
	}
	if field["note"] == nil {
		t.Errorf("thiếu ghi chú giải thích vì sao không có metadata: %#v", field)
	}
}

func TestBodyLog_SkipPaths(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{
		SkipPaths: []string{"/healthz", "/metrics"},
	})(okHandler())

	for _, p := range []string{"/healthz", "/metrics"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	if buf.Len() != 0 {
		t.Errorf("path bị skip vẫn ghi log: %s", buf.String())
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api", nil))
	if buf.Len() == 0 {
		t.Error("path không skip lại không ghi log")
	}
}

// Không có BodyLog trong chain thì SetRequestBody phải không làm gì, không panic.
func TestSetBody_KhongCoMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	middleware.SetRequestBody(req.Context(), map[string]any{"a": 1})
	middleware.SetResponseBody(req.Context(), map[string]any{"b": 2})
}

func TestBodyLog_BodyRong(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.BodyLog(l, middleware.BodyLogOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	m := bodyLine(t, buf)
	if _, ok := m["request"]; ok {
		t.Errorf("body rỗng vẫn có field request: %#v", m["request"])
	}
	if m["status"] != float64(http.StatusNoContent) {
		t.Errorf("status = %#v", m["status"])
	}
}
