package errs_test

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cqt002/gokit/core/errs"
)

func TestNew_StatusMacDinh(t *testing.T) {
	tests := []struct {
		code       errs.Code
		wantStatus int
	}{
		{errs.CodeBadRequest, http.StatusBadRequest},
		{errs.CodeValidation, http.StatusUnprocessableEntity},
		{errs.CodeUnauthorized, http.StatusUnauthorized},
		{errs.CodeForbidden, http.StatusForbidden},
		{errs.CodeNotFound, http.StatusNotFound},
		{errs.CodeConflict, http.StatusConflict},
		{errs.CodeTooManyReq, http.StatusTooManyRequests},
		{errs.CodeInternal, http.StatusInternalServerError},
		{errs.CodeUnavailable, http.StatusServiceUnavailable},
		{errs.CodeTimeout, http.StatusGatewayTimeout},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			e := errs.New(tt.code, "")
			if e.HTTPStatus != tt.wantStatus {
				t.Errorf("HTTPStatus = %d, muốn %d", e.HTTPStatus, tt.wantStatus)
			}
			if e.Message == "" {
				t.Error("Message rỗng, muốn message mặc định đã đăng ký")
			}
			if e.Code != tt.code {
				t.Errorf("Code = %q", e.Code)
			}
		})
	}
}

func TestNew_MessageTuKhai(t *testing.T) {
	e := errs.New(errs.CodeNotFound, "không tìm thấy tài khoản")
	if e.Message != "không tìm thấy tài khoản" {
		t.Errorf("Message = %q", e.Message)
	}
}

// Mã lỗi chưa đăng ký không được làm sập request đang xử lý.
func TestNew_MaLa(t *testing.T) {
	e := errs.New(errs.Code("ma_chua_dang_ky"), "")
	if e.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("HTTPStatus = %d, muốn 500", e.HTTPStatus)
	}
	if e.Code != "ma_chua_dang_ky" {
		t.Errorf("Code = %q", e.Code)
	}
}

func TestOptions(t *testing.T) {
	cause := errors.New("chi tiết nội bộ")
	e := errs.New(errs.CodeValidation, "dữ liệu không hợp lệ",
		errs.WithField("email", "sai định dạng"),
		errs.WithFields(
			errs.Field{Field: "age", Message: "phải lớn hơn 0"},
			errs.Field{Field: "name", Message: "bắt buộc"},
		),
		errs.WithData(map[string]int{"retry_after": 3}),
		errs.WithHTTPStatus(http.StatusTeapot),
		errs.WithCause(cause),
	)

	if len(e.Fields) != 3 {
		t.Errorf("số Fields = %d, muốn 3 (WithField và WithFields phải cộng dồn)", len(e.Fields))
	}
	if e.Fields[0].Field != "email" {
		t.Errorf("Fields[0] = %+v, muốn giữ thứ tự khai báo", e.Fields[0])
	}
	if e.HTTPStatus != http.StatusTeapot {
		t.Errorf("HTTPStatus = %d, muốn WithHTTPStatus ghi đè thành 418", e.HTTPStatus)
	}
	if e.Data == nil {
		t.Error("Data = nil")
	}
	if !errors.Is(e, cause) {
		t.Error("WithCause không bọc được error gốc")
	}
}

func TestError_ChuoiHienThi(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"code và message", errs.New(errs.CodeNotFound, "không thấy user"), "not_found: không thấy user"},
		{"chỉ code", errs.New(errs.Code("tu_dinh_nghia"), ""), "tu_dinh_nghia"},
		{
			"có lỗi gốc",
			errs.Wrap(errors.New("connection refused"), errs.CodeUnavailable, "không gọi được dịch vụ"),
			"unavailable: không gọi được dịch vụ: connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, muốn %q", got, tt.want)
			}
		})
	}
}

func TestWrap_ErrNil(t *testing.T) {
	if got := errs.Wrap(nil, errs.CodeInternal, "bỏ qua"); got != nil {
		t.Errorf("Wrap(nil, ...) = %v, muốn nil", got)
	}
}

func TestWrap_GiuOptions(t *testing.T) {
	e := errs.Wrap(sql.ErrNoRows, errs.CodeNotFound, "không thấy user", errs.WithField("id", "không tồn tại"))

	if !errors.Is(e, sql.ErrNoRows) {
		t.Error("mất error gốc")
	}
	if len(e.Fields) != 1 {
		t.Errorf("số Fields = %d, muốn 1", len(e.Fields))
	}
	if e.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus = %d, muốn 404", e.HTTPStatus)
	}
}

func TestUnwrap(t *testing.T) {
	cause := errors.New("gốc")
	e := errs.Wrap(cause, errs.CodeInternal, "")

	// So sánh danh tính là đúng chủ ý ở đây: đang kiểm tra Unwrap trả về đúng
	// error gốc đó, không phải kiểm tra "có phải loại lỗi này".
	//nolint:errorlint // kiểm tra danh tính của giá trị Unwrap trả về
	if got := errors.Unwrap(e); got != cause {
		t.Errorf("Unwrap = %v, muốn %v", got, cause)
	}
	if got := errors.Unwrap(errs.New(errs.CodeInternal, "")); got != nil {
		t.Errorf("Unwrap của error không bọc gì = %v, muốn nil", got)
	}
}

// Yêu cầu cốt lõi: phân loại lỗi phải xuyên qua nhiều lớp bọc, kể cả lớp bọc
// bằng fmt.Errorf của code không biết gì về package này.
func TestIs_XuyenNhieuLop(t *testing.T) {
	base := errs.New(errs.CodeNotFound, "không thấy user")
	wrapped := fmt.Errorf("tầng service: %w", base)
	rewrapped := errs.Wrap(wrapped, errs.CodeInternal, "xử lý thất bại")
	deepest := fmt.Errorf("tầng handler: %w", rewrapped)

	if !errs.Is(deepest, errs.CodeNotFound) {
		t.Error("Is không tìm được mã ở lớp trong cùng")
	}
	if !errs.Is(deepest, errs.CodeInternal) {
		t.Error("Is không tìm được mã ở lớp ngoài")
	}
	if errs.Is(deepest, errs.CodeForbidden) {
		t.Error("Is báo có mã không hề tồn tại trong chuỗi")
	}
	if errs.Is(nil, errs.CodeNotFound) {
		t.Error("Is(nil, ...) = true")
	}
	if errs.Is(errors.New("error thường"), errs.CodeNotFound) {
		t.Error("Is báo true cho error không phải *Error")
	}
}

// errors.Join dùng Unwrap() []error — nhánh mà errors.Unwrap thủ công sẽ bỏ sót.
func TestIs_QuaErrorsJoin(t *testing.T) {
	joined := errors.Join(errors.New("lỗi khác"), errs.New(errs.CodeConflict, ""))
	if !errs.Is(joined, errs.CodeConflict) {
		t.Error("Is không đi qua được errors.Join")
	}
}

func TestErrorsIs_VoiSentinel(t *testing.T) {
	err := errs.Wrap(errors.New("gốc"), errs.CodeTimeout, "quá hạn")

	// So khớp theo mã: message và lỗi gốc khác nhau vẫn coi là cùng loại.
	if !errors.Is(err, errs.New(errs.CodeTimeout, "message hoàn toàn khác")) {
		t.Error("errors.Is không so khớp theo mã lỗi")
	}
	if errors.Is(err, errs.New(errs.CodeNotFound, "")) {
		t.Error("errors.Is so khớp sai mã")
	}
	if errors.Is(err, errors.New("gốc")) {
		t.Error("errors.Is so khớp với error thường cùng nội dung")
	}
}

func TestAs(t *testing.T) {
	inner := errs.New(errs.CodeNotFound, "trong")
	outer := errs.Wrap(inner, errs.CodeInternal, "ngoài")
	wrapped := fmt.Errorf("thêm ngữ cảnh: %w", outer)

	got, ok := errs.As(wrapped)
	if !ok {
		t.Fatal("As trả ok = false")
	}
	// Lớp ngoài cùng thắng: nó là phân loại gần chỗ trả response nhất.
	if got.Code != errs.CodeInternal {
		t.Errorf("Code = %q, muốn lớp ngoài cùng internal_error", got.Code)
	}

	if _, ok := errs.As(errors.New("error thường")); ok {
		t.Error("As trả ok = true cho error thường")
	}
	if _, ok := errs.As(nil); ok {
		t.Error("As(nil) trả ok = true")
	}

	// errors.As của stdlib cũng phải hoạt động, không cần helper của package.
	var target *errs.Error
	if !errors.As(wrapped, &target) || target.Code != errs.CodeInternal {
		t.Error("errors.As không lấy được *errs.Error")
	}
}

func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"theo mã lỗi", errs.New(errs.CodeForbidden, ""), http.StatusForbidden},
		{"bị ghi đè", errs.New(errs.CodeNotFound, "", errs.WithHTTPStatus(http.StatusGone)), http.StatusGone},
		{"qua lớp bọc", fmt.Errorf("ngữ cảnh: %w", errs.New(errs.CodeConflict, "")), http.StatusConflict},
		{"error thường", errors.New("bùm"), http.StatusInternalServerError},
		{"nil", nil, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errs.HTTPStatus(tt.err); got != tt.want {
				t.Errorf("HTTPStatus = %d, muốn %d", got, tt.want)
			}
		})
	}
}

func TestRegister(t *testing.T) {
	const code = errs.Code("tai_khoan_bi_khoa")
	errs.Register(code, http.StatusLocked, "tài khoản đang bị khoá")

	e := errs.New(code, "")
	if e.HTTPStatus != http.StatusLocked {
		t.Errorf("HTTPStatus = %d, muốn 423", e.HTTPStatus)
	}
	if e.Message != "tài khoản đang bị khoá" {
		t.Errorf("Message = %q", e.Message)
	}
	if !errs.Is(e, code) {
		t.Error("Is không nhận mã vừa đăng ký")
	}
}

func TestRegister_GhiDeMaSanCo(t *testing.T) {
	// Trả về giá trị gốc sau khi test xong: registry là state toàn cục.
	t.Cleanup(func() {
		errs.Register(errs.CodeTimeout, http.StatusGatewayTimeout, "timeout")
	})

	errs.Register(errs.CodeTimeout, http.StatusRequestTimeout, "hết thời gian chờ")

	e := errs.New(errs.CodeTimeout, "")
	if e.HTTPStatus != http.StatusRequestTimeout {
		t.Errorf("HTTPStatus = %d, muốn 408 sau khi ghi đè", e.HTTPStatus)
	}
	if e.Message != "hết thời gian chờ" {
		t.Errorf("Message = %q", e.Message)
	}
}

func TestRegister_ThamSoSai(t *testing.T) {
	tests := []struct {
		name   string
		code   errs.Code
		status int
	}{
		{"mã rỗng", "", http.StatusBadRequest},
		{"status quá nhỏ", "ma_x", 99},
		{"status quá lớn", "ma_x", 600},
		{"status bằng 0", "ma_x", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("muốn panic, không thấy panic")
				}
			}()
			errs.Register(tt.code, tt.status, "")
		})
	}
}

// Register và New chạy song song được: service có thể vừa đăng ký mã trong init
// của package này vừa xử lý lỗi ở package khác. Test này chỉ có nghĩa với -race.
func TestRegister_SongSong(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			errs.Register(errs.Code(fmt.Sprintf("ma_song_song_%d", i)), http.StatusBadRequest, "x")
		}
	}()
	for range 200 {
		_ = errs.New(errs.CodeNotFound, "")
	}
	<-done
}
