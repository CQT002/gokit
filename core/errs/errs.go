// Package errs định nghĩa error có mã lỗi phân loại được, HTTP status kèm theo,
// và chi tiết lỗi theo từng field.
//
// Khác biệt then chốt so với cách thường thấy: mã lỗi là hằng có type, không phải
// chuỗi bóc ra từ err.Error(). Nhờ đó phân loại lỗi là so sánh giá trị chứ không
// phải so khớp chuỗi — và mọi thứ hoạt động qua errors.Is, errors.As, Unwrap của
// stdlib thay vì một cơ chế riêng.
//
// Ranh giới trách nhiệm: Message ở đây là chuỗi an toàn để trả cho client. Chi
// tiết nội bộ (câu lệnh SQL, tên host, stack) thuộc về error được bọc bên trong,
// nơi chỉ log đọc tới.
package errs

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Code là mã lỗi phân loại được, dùng cho cả client và code xử lý lỗi.
type Code string

// Các mã lỗi dựng sẵn. App tự thêm mã riêng bằng Register.
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

// Field là một lỗi gắn với một field cụ thể của dữ liệu vào.
type Field struct {
	// Field là tên field theo cách client gửi lên (tên JSON, không phải tên field Go).
	Field string `json:"field"`
	// Message là mô tả an toàn để trả cho client.
	Message string `json:"message"`
}

// Error là error có mã lỗi.
//
// Các field đều export để chỗ hiển thị (ví dụ httpx) đọc trực tiếp, nhưng nên
// coi như chỉ đọc sau khi tạo: sửa một *Error đang được nhiều nơi giữ tham chiếu
// là nguồn lỗi khó tìm.
type Error struct {
	// Code phân loại lỗi.
	Code Code
	// Message an toàn để trả cho client.
	Message string
	// HTTPStatus là status trả về. New tự điền theo Code, WithHTTPStatus ghi đè.
	HTTPStatus int
	// Fields là chi tiết lỗi theo từng field, dùng cho lỗi validate.
	Fields []Field
	// Data là dữ liệu kèm theo tuỳ ngữ cảnh (ví dụ số giây phải chờ khi bị chặn
	// vì quá nhiều request). Nội dung sẽ tới tay client, đừng để thông tin nội bộ.
	Data any

	// err là error gốc bị bọc. Không export: nó chứa chi tiết nội bộ, chỉ đi ra
	// qua Unwrap và Error() để log dùng.
	err error
}

// Option tinh chỉnh Error lúc tạo.
type Option func(*Error)

// WithHTTPStatus ghi đè status mặc định của mã lỗi.
func WithHTTPStatus(status int) Option {
	return func(e *Error) { e.HTTPStatus = status }
}

// WithFields thêm chi tiết lỗi theo field. Gọi nhiều lần thì cộng dồn.
func WithFields(fields ...Field) Option {
	return func(e *Error) { e.Fields = append(e.Fields, fields...) }
}

// WithField thêm một chi tiết lỗi theo field.
func WithField(name, message string) Option {
	return WithFields(Field{Field: name, Message: message})
}

// WithData gắn dữ liệu kèm theo. Nội dung này tới tay client.
func WithData(data any) Option {
	return func(e *Error) { e.Data = data }
}

// WithCause bọc error gốc, để errors.Is và errors.As xuyên qua được.
//
// Dùng khi đã có sẵn Option khác; nếu chỉ cần bọc thì Wrap gọn hơn.
func WithCause(err error) Option {
	return func(e *Error) { e.err = err }
}

// New tạo error mới với mã code.
//
// msg rỗng thì lấy message mặc định đã đăng ký cho mã đó. Mã lạ (chưa Register)
// vẫn dùng được và nhận HTTP 500 — thà trả 500 còn hơn panic giữa đường xử lý
// request vì một mã lỗi viết sai.
func New(code Code, msg string, opts ...Option) *Error {
	status, defaultMsg := lookup(code)
	if msg == "" {
		msg = defaultMsg
	}
	e := &Error{Code: code, Message: msg, HTTPStatus: status}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Wrap bọc err lại thành *Error với mã code.
//
// Trả về nil khi err là nil, nên luôn gọi trong nhánh `if err != nil`. Gán trực
// tiếp kết quả vào biến kiểu error sẽ tạo ra interface non-nil chứa con trỏ nil —
// cái bẫy kinh điển của Go:
//
//	// SAI: err2 != nil kể cả khi err là nil
//	var err2 error = errs.Wrap(err, errs.CodeInternal, "")
//
//	// ĐÚNG
//	if err != nil {
//		return errs.Wrap(err, errs.CodeInternal, "")
//	}
func Wrap(err error, code Code, msg string, opts ...Option) *Error {
	if err == nil {
		return nil
	}
	return New(code, msg, append([]Option{WithCause(err)}, opts...)...)
}

// Error cài đặt error: "<code>: <message>: <lỗi gốc>", bỏ phần rỗng.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.err != nil {
		b.WriteString(": ")
		b.WriteString(e.err.Error())
	}
	return b.String()
}

// Unwrap trả về error gốc, cho errors.Is và errors.As đi tiếp xuống dưới.
func (e *Error) Unwrap() error { return e.err }

// Is cho phép errors.Is so khớp theo mã lỗi: hai *Error cùng Code được coi là
// khớp, bất kể message hay error gốc khác nhau. Đây là điều kiện để hàm Is của
// package này hoạt động xuyên nhiều lớp bọc.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Is cho biết trong chuỗi lỗi của err có mã code hay không.
//
// Quét toàn bộ chuỗi, không chỉ lớp ngoài cùng: một lỗi not_found bị bọc lại
// thành internal_error vẫn tìm ra được bằng Is(err, CodeNotFound). Hàm cũng đi
// qua được errors.Join vì dựa trên errors.Is của stdlib.
func Is(err error, code Code) bool {
	return errors.Is(err, &Error{Code: code})
}

// As lấy *Error ngoài cùng trong chuỗi lỗi của err.
//
// Lớp ngoài cùng là lớp quyết định HTTP status: nó là phân loại gần nhất với chỗ
// phát hiện lỗi, nên cũng là phân loại đúng nhất để trả cho client.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// HTTPStatus trả về HTTP status nên dùng cho err.
//
// Trả 500 cho error không phải *Error: một lỗi chưa được phân loại là lỗi chưa
// ai lường tới, và đó đúng nghĩa là lỗi phía server.
func HTTPStatus(err error) int {
	if e, ok := As(err); ok && e.HTTPStatus != 0 {
		return e.HTTPStatus
	}
	return http.StatusInternalServerError
}

// definition là status và message mặc định của một mã lỗi.
type definition struct {
	status  int
	message string
}

var (
	registryMu sync.RWMutex
	registry   = map[Code]definition{
		CodeBadRequest:   {http.StatusBadRequest, "bad request"},
		CodeValidation:   {http.StatusUnprocessableEntity, "validation failed"},
		CodeUnauthorized: {http.StatusUnauthorized, "unauthorized"},
		CodeForbidden:    {http.StatusForbidden, "forbidden"},
		CodeNotFound:     {http.StatusNotFound, "not found"},
		CodeConflict:     {http.StatusConflict, "conflict"},
		CodeTooManyReq:   {http.StatusTooManyRequests, "too many requests"},
		CodeInternal:     {http.StatusInternalServerError, "internal error"},
		CodeUnavailable:  {http.StatusServiceUnavailable, "service unavailable"},
		CodeTimeout:      {http.StatusGatewayTimeout, "timeout"},
	}
)

// Register khai báo mã lỗi riêng của app, hoặc đổi status và message mặc định
// của một mã đã có.
//
// Đây là chỗ duy nhất trong gokit có state toàn cục thay đổi được, và nó có lý
// do: bảng mã lỗi là thuộc tính của cả process, một *Error tạo ở tầng repository
// không có đường nào cầm theo registry. Bù lại, hàm này chỉ dành cho lúc khởi
// tạo (init hoặc đầu main), trước khi service nhận request.
//
// Panic nếu code rỗng hoặc httpStatus ngoài khoảng 100–599: sai bảng mã lỗi là
// lỗi lập trình, và lộ ra lúc khởi động thì rẻ hơn nhiều so với lộ ra lúc chạy.
func Register(code Code, httpStatus int, defaultMsg string) {
	if code == "" {
		panic("errs: Register với mã lỗi rỗng")
	}
	if httpStatus < 100 || httpStatus > 599 {
		panic("errs: Register với HTTP status không hợp lệ: " + strconv.Itoa(httpStatus))
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	registry[code] = definition{status: httpStatus, message: defaultMsg}
}

// lookup tra status và message mặc định của một mã lỗi. Mã lạ nhận 500.
func lookup(code Code) (status int, message string) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	if d, ok := registry[code]; ok {
		return d.status, d.message
	}
	return http.StatusInternalServerError, ""
}
