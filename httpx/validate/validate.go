// Package validate kiểm tra dữ liệu vào theo tag `validate:` và biến lỗi thành
// chi tiết theo từng field.
//
// Bọc go-playground/validator, và phần bọc chỉ làm đúng hai việc mà thư viện gốc
// không làm: dịch lỗi sang thông báo tiếng Việt đọc được, và dùng **tên field theo
// JSON** thay vì tên field Go.
//
// Việc thứ hai quan trọng hơn nó trông: client gửi "user_id" và cần biết field
// "user_id" sai, không phải "UserID" — một tên chỉ tồn tại trong code Go của server.
package validate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/cqt002/gokit/core/errs"
)

// Validator kiểm tra struct theo tag `validate:`.
//
// An toàn khi dùng từ nhiều goroutine. Dựng một lần lúc khởi động rồi dùng chung.
type Validator struct {
	v *validator.Validate

	mu       sync.RWMutex
	messages map[string]MessageFunc
}

// MessageFunc dựng thông báo lỗi cho một rule.
//
// field là tên field theo JSON, param là tham số của rule (ví dụ "10" trong
// `validate:"max=10"`).
type MessageFunc func(field, param string) string

// New tạo Validator với các rule sẵn có của go-playground và thông báo tiếng Việt.
func New() *Validator {
	v := validator.New(validator.WithRequiredStructEnabled())

	// Lấy tên field từ tag json: client chỉ biết tên đó.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "", "-":
			return f.Name
		default:
			return name
		}
	})

	return &Validator{v: v, messages: defaultMessages()}
}

// Register thêm một rule mới.
//
// Đây là phần "cắm thêm được": nghiệp vụ có những luật mà thư viện chung không thể
// biết — mã chi nhánh, định dạng số tài khoản, mã tỉnh. Khai một lần lúc khởi động
// rồi dùng bằng tag như mọi rule khác:
//
//	v.Register("ma_chi_nhanh", func(s string) bool { return len(s) == 4 },
//	    func(field, _ string) string { return field + " phải là mã chi nhánh 4 ký tự" })
//
//	type Req struct {
//	    Branch string `json:"branch" validate:"required,ma_chi_nhanh"`
//	}
func (val *Validator) Register(name string, fn func(string) bool, msg MessageFunc) error {
	if name == "" {
		return errors.New("validate: tên rule không được rỗng")
	}
	if fn == nil {
		return errors.New("validate: rule " + name + " thiếu hàm kiểm tra")
	}

	if err := val.v.RegisterValidation(name, func(fl validator.FieldLevel) bool {
		return fn(fl.Field().String())
	}); err != nil {
		return fmt.Errorf("validate: đăng ký rule %q: %w", name, err)
	}

	if msg != nil {
		val.mu.Lock()
		val.messages[name] = msg
		val.mu.Unlock()
	}
	return nil
}

// RegisterMessage đổi thông báo của một rule có sẵn.
func (val *Validator) RegisterMessage(rule string, msg MessageFunc) {
	val.mu.Lock()
	defer val.mu.Unlock()
	val.messages[rule] = msg
}

// Struct kiểm tra v và trả về *errs.Error mã validation_failed nếu có lỗi.
//
// Lỗi trả về đã ở dạng dùng được cho httpx.Fail: mỗi field sai là một errs.Field,
// nên client nhận được danh sách đầy đủ trong một lần thay vì sửa một lỗi rồi gửi
// lại để phát hiện lỗi tiếp theo.
func (val *Validator) Struct(v any) error {
	err := val.v.Struct(v)
	if err == nil {
		return nil
	}

	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		// Truyền vào thứ không phải struct: lỗi lập trình, không phải lỗi dữ liệu.
		return fmt.Errorf("validate: %w", err)
	}

	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return fmt.Errorf("validate: %w", err)
	}

	fields := make([]errs.Field, 0, len(verrs))
	for _, fe := range verrs {
		fields = append(fields, errs.Field{
			Field:   fe.Field(),
			Message: val.message(fe),
		})
	}
	return errs.New(errs.CodeValidation, "dữ liệu không hợp lệ", errs.WithFields(fields...))
}

func (val *Validator) message(fe validator.FieldError) string {
	val.mu.RLock()
	fn, ok := val.messages[fe.Tag()]
	val.mu.RUnlock()

	if ok {
		return fn(fe.Field(), fe.Param())
	}
	// Rule chưa có thông báo riêng: vẫn phải nói được field nào và rule nào, để
	// người gọi API không phải đoán.
	if fe.Param() != "" {
		return fmt.Sprintf("%s không thoả điều kiện %s=%s", fe.Field(), fe.Tag(), fe.Param())
	}
	return fmt.Sprintf("%s không thoả điều kiện %s", fe.Field(), fe.Tag())
}

func defaultMessages() map[string]MessageFunc {
	return map[string]MessageFunc{
		"required": func(f, _ string) string { return f + " là bắt buộc" },
		"email":    func(f, _ string) string { return f + " phải là email hợp lệ" },
		"url":      func(f, _ string) string { return f + " phải là URL hợp lệ" },
		"uuid":     func(f, _ string) string { return f + " phải là UUID hợp lệ" },
		"numeric":  func(f, _ string) string { return f + " chỉ được chứa chữ số" },
		"alpha":    func(f, _ string) string { return f + " chỉ được chứa chữ cái" },
		"alphanum": func(f, _ string) string { return f + " chỉ được chứa chữ và số" },
		"min":      func(f, p string) string { return f + " phải từ " + p + " trở lên" },
		"max":      func(f, p string) string { return f + " không được vượt " + p },
		"len":      func(f, p string) string { return f + " phải có đúng độ dài " + p },
		"gt":       func(f, p string) string { return f + " phải lớn hơn " + p },
		"gte":      func(f, p string) string { return f + " phải lớn hơn hoặc bằng " + p },
		"lt":       func(f, p string) string { return f + " phải nhỏ hơn " + p },
		"lte":      func(f, p string) string { return f + " phải nhỏ hơn hoặc bằng " + p },
		"oneof":    func(f, p string) string { return f + " phải là một trong: " + p },
		"eqfield":  func(f, p string) string { return f + " phải trùng với " + p },
		"e164":     func(f, _ string) string { return f + " phải là số điện thoại dạng E.164" },
	}
}
