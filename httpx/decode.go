package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/httpx/middleware"
	"github.com/cqt002/gokit/httpx/validate"
)

// defaultValidator là validator dùng bởi Decode và DecodeQuery.
//
// Dựng một lần, lazily: việc dựng có chi phí (build cache của reflect) và không
// phải service nào cũng dùng Decode.
var (
	validatorOnce sync.Once
	validatorVal  *validate.Validator
)

// Validator trả về validator mà Decode dùng.
//
// Đây là chỗ để đăng ký rule riêng của app lúc khởi động:
//
//	httpx.Validator().Register("ma_chi_nhanh", isBranchCode, branchMessage)
func Validator() *validate.Validator {
	validatorOnce.Do(func() { validatorVal = validate.New() })
	return validatorVal
}

// Decode đọc JSON body thành T, validate, và đăng ký bản đã mask cho log.
//
// Ba việc trong một lần gọi, và thứ tự có lý do:
//
//  1. Parse JSON. Lỗi cú pháp thành 400 với thông báo chỉ ra vị trí, không phải
//     500 hay một thông báo chung vô dụng.
//  2. Validate theo tag `validate:`. Lỗi thành 422 kèm danh sách field sai.
//  3. Đăng ký giá trị đã parse cho middleware.BodyLog. Nhờ đó log dùng được masking
//     lớp 2 (theo tag `log:`) — thứ mà middleware không làm được vì nó không biết
//     type. Và vì đã có struct, không phải parse JSON lần thứ hai chỉ để mask.
//
// Đăng ký log diễn ra **trước** khi validate trả lỗi: một request bị từ chối vì
// dữ liệu sai lại càng là request cần đọc lại body trong log.
func Decode[T any](r *http.Request) (T, error) {
	var v T

	if r.Body == nil || r.Body == http.NoBody {
		return v, errs.New(errs.CodeBadRequest, "request thiếu body")
	}

	dec := json.NewDecoder(r.Body)
	// Field lạ là lỗi: client gửi "amout" thay vì "amount" mà server im lặng bỏ qua
	// rồi xử lý với số tiền 0 là kiểu sự cố tệ nhất trong nhóm này.
	dec.DisallowUnknownFields()

	if err := dec.Decode(&v); err != nil {
		return v, decodeError(err)
	}

	// Đăng ký ngay sau khi parse được, trước cả validate.
	middleware.SetRequestBody(r.Context(), log.Safe(v))

	if err := Validator().Struct(v); err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

// decodeError biến lỗi của json.Decoder thành lỗi 400 nói được chỗ sai.
func decodeError(err error) error {
	var (
		syntaxErr *json.SyntaxError
		typeErr   *json.UnmarshalTypeError
		maxBytes  *http.MaxBytesError
	)
	switch {
	case errors.As(err, &maxBytes):
		return errs.New(errs.CodeBadRequest,
			fmt.Sprintf("body vượt giới hạn %d byte", maxBytes.Limit))

	case errors.As(err, &syntaxErr):
		return errs.New(errs.CodeBadRequest,
			fmt.Sprintf("JSON sai cú pháp tại byte %d", syntaxErr.Offset),
			errs.WithCause(err))

	case errors.As(err, &typeErr):
		// Nói rõ field nào và kiểu nào: đây là lỗi hay gặp nhất, thường do client
		// gửi số dưới dạng chuỗi hoặc ngược lại.
		return errs.New(errs.CodeBadRequest,
			fmt.Sprintf("field %q phải là kiểu %s", typeErr.Field, typeErr.Type),
			errs.WithField(typeErr.Field, "kiểu dữ liệu không đúng"),
			errs.WithCause(err))

	case errors.Is(err, io.EOF):
		return errs.New(errs.CodeBadRequest, "request thiếu body")

	case errors.Is(err, io.ErrUnexpectedEOF):
		return errs.New(errs.CodeBadRequest, "JSON bị cắt giữa dòng", errs.WithCause(err))

	default:
		// json.Decoder trả lỗi dạng chuỗi cho field lạ, không có type riêng.
		if field, ok := unknownField(err); ok {
			return errs.New(errs.CodeBadRequest,
				fmt.Sprintf("field %q không được chấp nhận", field),
				errs.WithField(field, "field không tồn tại"),
				errs.WithCause(err))
		}
		return errs.New(errs.CodeBadRequest, "không đọc được JSON", errs.WithCause(err))
	}
}

// unknownField bóc tên field từ thông báo lỗi của DisallowUnknownFields.
//
// encoding/json không có type lỗi riêng cho trường hợp này nên phải so khớp chuỗi.
// Nếu định dạng thông báo đổi thì hàm trả về false và chỗ gọi rơi về thông báo
// chung — mất chi tiết chứ không sai.
func unknownField(err error) (string, bool) {
	const prefix = `json: unknown field `
	msg := err.Error()
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	field, unquoteErr := strconv.Unquote(strings.TrimPrefix(msg, prefix))
	if unquoteErr != nil {
		return "", false
	}
	return field, true
}

// DecodeQuery đọc query string thành T rồi validate.
//
// Đọc theo tag `query:`, hoặc tag `json:` nếu không có `query:`. Hỗ trợ string, các
// kiểu số, bool, time.Duration, và slice (giá trị lặp lại hoặc phân tách bằng dấu
// phẩy).
//
//	type ListReq struct {
//	    Page   int      `query:"page"   validate:"gte=1"`
//	    Size   int      `query:"size"   validate:"gte=1,lte=100"`
//	    Status []string `query:"status"`
//	}
func DecodeQuery[T any](r *http.Request) (T, error) {
	var v T

	rv := reflect.ValueOf(&v).Elem()
	if rv.Kind() != reflect.Struct {
		return v, fmt.Errorf("httpx: DecodeQuery cần struct, nhận %s", rv.Kind())
	}

	q := r.URL.Query()
	rt := rv.Type()

	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}

		name := queryName(f)
		if name == "" {
			continue
		}
		values, ok := q[name]
		if !ok || len(values) == 0 {
			continue
		}

		if err := setQueryField(rv.Field(i), name, values); err != nil {
			return v, err
		}
	}

	middleware.SetRequestBody(r.Context(), log.Safe(v))

	if err := Validator().Struct(v); err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

func queryName(f reflect.StructField) string {
	if tag, ok := f.Tag.Lookup("query"); ok {
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			return ""
		}
		if name != "" {
			return name
		}
	}
	if tag, ok := f.Tag.Lookup("json"); ok {
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			return ""
		}
		if name != "" {
			return name
		}
	}
	return f.Name
}

// setQueryField gán giá trị từ query string vào một field.
func setQueryField(fv reflect.Value, name string, values []string) error {
	if fv.Kind() == reflect.Slice {
		// Nhận cả ?status=a&status=b và ?status=a,b — client nào cũng viết một kiểu
		// khác nhau và cả hai đều phổ biến.
		var items []string
		for _, v := range values {
			items = append(items, strings.Split(v, ",")...)
		}

		out := reflect.MakeSlice(fv.Type(), len(items), len(items))
		for i, item := range items {
			if err := setScalar(out.Index(i), name, item); err != nil {
				return err
			}
		}
		fv.Set(out)
		return nil
	}
	return setScalar(fv, name, values[0])
}

func setScalar(fv reflect.Value, name, raw string) error {
	badValue := func(want string) error {
		return errs.New(errs.CodeBadRequest,
			fmt.Sprintf("tham số %q phải là %s", name, want),
			errs.WithField(name, "giá trị không đúng kiểu"))
	}

	// time.Duration là int64 nhưng "30s" mới là cách viết người ta dùng.
	if fv.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return badValue("khoảng thời gian, ví dụ 30s")
		}
		fv.SetInt(int64(d))
		return nil
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)

	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return badValue("true hoặc false")
		}
		fv.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return badValue("số nguyên")
		}
		fv.SetInt(n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return badValue("số nguyên không âm")
		}
		fv.SetUint(n)

	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return badValue("số")
		}
		fv.SetFloat(n)

	default:
		return fmt.Errorf("httpx: DecodeQuery không hỗ trợ kiểu %s (tham số %q)", fv.Kind(), name)
	}
	return nil
}
