package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/core/tracectx"
	"github.com/cqt002/gokit/httpx"
)

// decodeEnvelope parse response thành Envelope.
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) httpx.Envelope {
	t.Helper()
	var env httpx.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response không phải Envelope: %v\n%s", err, rec.Body.String())
	}
	return env
}

func TestOK(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.OK(rec, httptest.NewRequest(http.MethodGet, "/", nil), map[string]any{"id": "1"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	env := decodeEnvelope(t, rec)
	if env.Status != httpx.StatusAccept {
		t.Errorf("Status = %q", env.Status)
	}
	data, ok := env.Data.(map[string]any)
	if !ok || data["id"] != "1" {
		t.Errorf("Data = %#v", env.Data)
	}
}

func TestCreatedVaAccepted(t *testing.T) {
	tests := []struct {
		name   string
		fn     func(http.ResponseWriter, *http.Request, any)
		status int
		state  string
	}{
		{"Created", httpx.Created, http.StatusCreated, httpx.StatusAccept},
		{"Accepted", httpx.Accepted, http.StatusAccepted, httpx.StatusProcessing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.fn(rec, httptest.NewRequest(http.MethodPost, "/", nil), "xong")

			if rec.Code != tt.status {
				t.Errorf("status = %d, muốn %d", rec.Code, tt.status)
			}
			if got := decodeEnvelope(t, rec).Status; got != tt.state {
				t.Errorf("Status = %q, muốn %q", got, tt.state)
			}
		})
	}
}

func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.NoContent(rec, httptest.NewRequest(http.MethodDelete, "/", nil))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 phải không có body, được %q", rec.Body.String())
	}
}

// Trace ID trong response là thứ để người dùng báo lỗi kèm ID và tra được ngay.
func TestEnvelope_CoTraceID(t *testing.T) {
	sc := tracectx.NewRoot()
	req := httptest.NewRequest(http.MethodGet, "/", nil).
		WithContext(tracectx.WithSpanContext(context.Background(), sc))

	rec := httptest.NewRecorder()
	httpx.OK(rec, req, nil)

	if got := decodeEnvelope(t, rec).TraceID; got != sc.TraceID {
		t.Errorf("TraceID = %q, muốn %q", got, sc.TraceID)
	}
}

func TestEnvelope_ElapsedMs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = httpx.WithStart(req, time.Now().Add(-50*time.Millisecond))

	rec := httptest.NewRecorder()
	httpx.OK(rec, req, nil)

	if got := decodeEnvelope(t, rec).ElapsedMs; got < 40 {
		t.Errorf("ElapsedMs = %d, muốn khoảng 50", got)
	}

	// Không gắn mốc bắt đầu thì bỏ hẳn field, không trả 0.
	rec2 := httptest.NewRecorder()
	httpx.OK(rec2, httptest.NewRequest(http.MethodGet, "/", nil), nil)
	if strings.Contains(rec2.Body.String(), "elapsed_ms") {
		t.Errorf("có elapsed_ms dù chưa gắn mốc: %s", rec2.Body.String())
	}
}

// ---------- Fail ----------

func TestFail_MapTuErrs(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", errs.New(errs.CodeNotFound, "không thấy user"), 404, "not_found"},
		{"validation", errs.New(errs.CodeValidation, "sai"), 422, "validation_failed"},
		{"unauthorized", errs.New(errs.CodeUnauthorized, ""), 401, "unauthorized"},
		{"forbidden", errs.New(errs.CodeForbidden, ""), 403, "forbidden"},
		{"conflict", errs.New(errs.CodeConflict, ""), 409, "conflict"},
		{"too many", errs.New(errs.CodeTooManyReq, ""), 429, "too_many_requests"},
		{"timeout", errs.New(errs.CodeTimeout, ""), 504, "timeout"},
		{"status ghi đè", errs.New(errs.CodeNotFound, "", errs.WithHTTPStatus(410)), 410, "not_found"},
		{"qua lớp bọc", wrapTwice(errs.New(errs.CodeConflict, "trùng")), 409, "conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpx.Fail(rec, httptest.NewRequest(http.MethodGet, "/", nil), tt.err)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, muốn %d", rec.Code, tt.wantStatus)
			}
			env := decodeEnvelope(t, rec)
			if env.Code != tt.wantCode {
				t.Errorf("Code = %q, muốn %q", env.Code, tt.wantCode)
			}
			if env.Status != httpx.StatusReject {
				t.Errorf("Status = %q, muốn REJECT", env.Status)
			}
		})
	}
}

func wrapTwice(err error) error {
	return errors.Join(errors.New("tầng ngoài"), err)
}

// Nội dung err.Error() thường chứa tên host, câu SQL, đường dẫn file — không được
// đưa ra client.
func TestFail_ErrorThuongKhongLoChiTiet(t *testing.T) {
	const chiTietNoiBo = `pq: relation "users" does not exist (host=db-primary.internal)`

	rec := httptest.NewRecorder()
	httpx.Fail(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New(chiTietNoiBo))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, muốn 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "users") || strings.Contains(rec.Body.String(), "db-primary") {
		t.Fatalf("chi tiết nội bộ lọt ra client: %s", rec.Body.String())
	}
	if decodeEnvelope(t, rec).Code != string(errs.CodeInternal) {
		t.Errorf("Code = %q", decodeEnvelope(t, rec).Code)
	}
}

// Gọi Fail mà không có lỗi là bug ở chỗ gọi; im lặng trả 200 sẽ che mất bug đó.
func TestFail_Nil(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Fail(rec, httptest.NewRequest(http.MethodGet, "/", nil), nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, muốn 500", rec.Code)
	}
}

func TestFail_Fields(t *testing.T) {
	err := errs.New(errs.CodeValidation, "dữ liệu không hợp lệ",
		errs.WithField("email", "sai định dạng"),
		errs.WithField("age", "phải lớn hơn 0"))

	rec := httptest.NewRecorder()
	httpx.Fail(rec, httptest.NewRequest(http.MethodPost, "/", nil), err)

	env := decodeEnvelope(t, rec)
	if len(env.Fields) != 2 {
		t.Fatalf("số Fields = %d, muốn 2: %#v", len(env.Fields), env.Fields)
	}
	if env.Fields[0].Field != "email" || env.Fields[0].Message == "" {
		t.Errorf("Fields[0] = %#v", env.Fields[0])
	}
}

// ---------- Decode ----------

type createUserReq struct {
	Name     string `json:"name" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Age      int    `json:"age" validate:"gte=18"`
	Password string `json:"password" log:"redact"`
}

func postJSON(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestDecode(t *testing.T) {
	got, err := httpx.Decode[createUserReq](postJSON(
		`{"name":"An","email":"an@example.com","age":30,"password":"bí mật"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Name != "An" || got.Email != "an@example.com" || got.Age != 30 {
		t.Errorf("got = %+v", got)
	}
}

func TestDecode_LoiValidate(t *testing.T) {
	_, err := httpx.Decode[createUserReq](postJSON(
		`{"name":"","email":"khong-phai-email","age":10}`))
	if err == nil {
		t.Fatal("muốn lỗi validate")
	}

	e, ok := errs.As(err)
	if !ok {
		t.Fatalf("lỗi = %v, muốn *errs.Error", err)
	}
	if e.Code != errs.CodeValidation {
		t.Errorf("Code = %q, muốn validation_failed", e.Code)
	}
	// Client phải nhận được đầy đủ danh sách field sai trong một lần, không phải
	// sửa một lỗi rồi gửi lại để phát hiện lỗi tiếp theo.
	if len(e.Fields) != 3 {
		t.Errorf("số Fields = %d, muốn 3: %#v", len(e.Fields), e.Fields)
	}
	// Tên field theo JSON, không phải tên field Go.
	for _, f := range e.Fields {
		if strings.ToUpper(f.Field[:1]) == f.Field[:1] {
			t.Errorf("field %q dùng tên Go, phải dùng tên JSON", f.Field)
		}
	}
}

func TestDecode_LoiCuPhap(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"JSON hỏng", `{"name":`, ""},
		{"sai kiểu", `{"name":"An","email":"a@b.com","age":"ba mươi"}`, "age"},
		{"field lạ", `{"name":"An","email":"a@b.com","age":30,"khong_ton_tai":1}`, "khong_ton_tai"},
		{"body rỗng", ``, ""},
		{"không phải object", `[1,2,3]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := httpx.Decode[createUserReq](postJSON(tt.body))
			if err == nil {
				t.Fatal("muốn lỗi")
			}
			if !errs.Is(err, errs.CodeBadRequest) {
				t.Errorf("lỗi = %v, muốn mã bad_request", err)
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("lỗi %q không nhắc tới %q", err, tt.want)
			}
		})
	}
}

// Field lạ là lỗi: client gửi "amout" thay vì "amount" mà server im lặng bỏ qua rồi
// xử lý với số tiền 0 là kiểu sự cố tệ nhất trong nhóm này.
func TestDecode_FieldLaLaLoi(t *testing.T) {
	_, err := httpx.Decode[createUserReq](postJSON(
		`{"name":"An","email":"a@b.com","age":30,"amout":1000}`))
	if err == nil {
		t.Fatal("field gõ sai được bỏ qua im lặng")
	}
	if !strings.Contains(err.Error(), "amout") {
		t.Errorf("lỗi %q không chỉ ra field sai", err)
	}
}

func TestDecode_BodyKhongCo(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/users", nil)
	if _, err := httpx.Decode[createUserReq](req); err == nil {
		t.Error("muốn lỗi khi không có body")
	}
}

// MaxBodySize sinh ra *http.MaxBytesError, và Decode phải biến nó thành 400 kèm
// giới hạn cụ thể, không phải 500.
func TestDecode_VuotMaxBodySize(t *testing.T) {
	req := postJSON(`{"name":"` + strings.Repeat("a", 1000) + `"}`)
	req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, 50)

	_, err := httpx.Decode[createUserReq](req)
	if err == nil {
		t.Fatal("muốn lỗi")
	}
	if !errs.Is(err, errs.CodeBadRequest) {
		t.Errorf("lỗi = %v, muốn bad_request", err)
	}
	if !strings.Contains(err.Error(), "50") {
		t.Errorf("lỗi %q không nói giới hạn cụ thể", err)
	}
}

// ---------- DecodeQuery ----------

type listReq struct {
	Page    int           `query:"page" validate:"gte=1"`
	Size    int           `query:"size" validate:"gte=1,lte=100"`
	Status  []string      `query:"status"`
	Active  bool          `query:"active"`
	Ratio   float64       `query:"ratio"`
	Timeout time.Duration `query:"timeout"`
	Name    string        `json:"name"`
}

func TestDecodeQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/?page=2&size=50&status=new&status=done&active=true&ratio=1.5&timeout=30s&name=an", nil)

	got, err := httpx.DecodeQuery[listReq](req)
	if err != nil {
		t.Fatalf("DecodeQuery: %v", err)
	}
	if got.Page != 2 || got.Size != 50 {
		t.Errorf("Page = %d, Size = %d", got.Page, got.Size)
	}
	if len(got.Status) != 2 || got.Status[0] != "new" {
		t.Errorf("Status = %v", got.Status)
	}
	if !got.Active || got.Ratio != 1.5 {
		t.Errorf("Active = %v, Ratio = %v", got.Active, got.Ratio)
	}
	if got.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v", got.Timeout)
	}
	// Không có tag query thì rơi về tag json.
	if got.Name != "an" {
		t.Errorf("Name = %q", got.Name)
	}
}

// Client nào cũng viết một kiểu, và cả hai đều phổ biến.
func TestDecodeQuery_SliceHaiKieu(t *testing.T) {
	tests := []string{"/?page=1&size=1&status=a&status=b", "/?page=1&size=1&status=a,b"}
	for _, url := range tests {
		got, err := httpx.DecodeQuery[listReq](httptest.NewRequest(http.MethodGet, url, nil))
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if len(got.Status) != 2 {
			t.Errorf("%s: Status = %v, muốn 2 phần tử", url, got.Status)
		}
	}
}

func TestDecodeQuery_GiaTriSai(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"số sai", "/?page=abc&size=10"},
		{"bool sai", "/?page=1&size=10&active=co"},
		{"duration sai", "/?page=1&size=10&timeout=ba-giay"},
		{"float sai", "/?page=1&size=10&ratio=nhieu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := httpx.DecodeQuery[listReq](httptest.NewRequest(http.MethodGet, tt.url, nil))
			if err == nil {
				t.Fatal("muốn lỗi")
			}
			if !errs.Is(err, errs.CodeBadRequest) {
				t.Errorf("lỗi = %v, muốn bad_request", err)
			}
		})
	}
}

func TestDecodeQuery_ViPhamValidate(t *testing.T) {
	_, err := httpx.DecodeQuery[listReq](httptest.NewRequest(http.MethodGet, "/?page=0&size=1000", nil))
	if err == nil {
		t.Fatal("muốn lỗi validate")
	}
	if !errs.Is(err, errs.CodeValidation) {
		t.Errorf("lỗi = %v, muốn validation_failed", err)
	}
}

// ---------- Validator cắm thêm rule ----------

func TestValidator_RuleCamThem(t *testing.T) {
	v := httpx.Validator()
	err := v.Register("ma_chi_nhanh",
		func(s string) bool { return len(s) == 4 },
		func(field, _ string) string { return field + " phải là mã chi nhánh 4 ký tự" })
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	type req struct {
		Branch string `json:"branch" validate:"required,ma_chi_nhanh"`
	}

	if structErr := v.Struct(req{Branch: "0001"}); structErr != nil {
		t.Errorf("mã hợp lệ bị từ chối: %v", structErr)
	}

	err = v.Struct(req{Branch: "1"})
	if err == nil {
		t.Fatal("mã sai không bị từ chối")
	}
	e, ok := errs.As(err)
	if !ok || len(e.Fields) != 1 {
		t.Fatalf("lỗi = %v", err)
	}
	if !strings.Contains(e.Fields[0].Message, "mã chi nhánh") {
		t.Errorf("message = %q, muốn thông báo riêng đã khai", e.Fields[0].Message)
	}
}

func TestValidator_RuleSai(t *testing.T) {
	v := httpx.Validator()
	if err := v.Register("", func(string) bool { return true }, nil); err == nil {
		t.Error("tên rule rỗng không báo lỗi")
	}
	if err := v.Register("thieu_ham", nil, nil); err == nil {
		t.Error("thiếu hàm kiểm tra không báo lỗi")
	}
}
