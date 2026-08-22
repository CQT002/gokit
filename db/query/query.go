// Package query dựng điều kiện WHERE, ORDER BY và phân trang từ tham số của
// API, có whitelist cột.
//
// Vấn đề nó giải quyết: mọi API danh sách đều cần filter và sort theo tên cột
// mà client gửi lên, và cách viết nhanh nhất — nối tên cột vào câu SQL — là một
// lỗ SQL injection. Ở đây tên field client gửi lên **không bao giờ** đi vào câu
// SQL: nó chỉ dùng để tra một map do server khai, và tên không có trong map thì
// trả lỗi. Cùng lý do đó, [Op] cố tình không có giá trị nào cho SQL thô.
//
// Kiểu lỗi trả về là *errs.Error mã errs.CodeBadRequest, nên tầng HTTP đổi nó
// thành 400 mà không cần biết gì về package này.
//
//	allowed := map[string]string{"name": "name", "createdAt": "created_at"}
//
//	tx, err := query.Apply(gdb.Model(&User{}), filters, allowed)
//	if err != nil { return err }
//	scope, err := query.Paginate(page, allowed)
//	if err != nil { return err }
//	err = tx.Scopes(scope).Find(&users).Error
package query

import (
	"fmt"
	"reflect"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/cqt002/gokit/core/errs"
)

// Op là phép so sánh của một điều kiện filter.
type Op string

// Các phép so sánh được hỗ trợ.
//
// Danh sách này là **đóng**: không có Op nào nhận SQL thô, và cũng không có
// cách nào thêm từ ngoài package. Một Op "raw" sẽ vô hiệu hoá toàn bộ whitelist
// cột, vì lúc đó chuỗi của client lại đi thẳng vào câu lệnh.
const (
	Eq    Op = "eq"      // =
	Ne    Op = "ne"      // <>
	Gt    Op = "gt"      // >
	Gte   Op = "gte"     // >=
	Lt    Op = "lt"      // <
	Lte   Op = "lte"     // <=
	Like  Op = "like"    // chứa chuỗi, phân biệt chữ hoa/thường
	ILike Op = "ilike"   // chứa chuỗi, không phân biệt chữ hoa/thường
	In    Op = "in"      // thuộc danh sách
	NotIn Op = "not_in"  // không thuộc danh sách
	Null  Op = "is_null" // IS NULL khi Value là true, IS NOT NULL khi false
)

// Filter là một điều kiện lọc.
type Filter struct {
	// Field là tên field theo cách client gửi lên. Phải có trong map allowed.
	Field string `json:"field"`
	// Op là phép so sánh.
	Op Op `json:"op"`
	// Value là giá trị so sánh. In/NotIn cần slice, Null cần bool.
	Value any `json:"value"`
}

// Sort là một tiêu chí sắp xếp.
type Sort struct {
	// Field là tên field theo cách client gửi lên. Phải có trong map allowed.
	Field string `json:"field"`
	// Desc bật sắp xếp giảm dần.
	Desc bool `json:"desc"`
}

// Page là tham số phân trang theo offset.
type Page struct {
	// Limit là số bản ghi tối đa. <= 0 thì dùng DefaultLimit, lớn hơn
	// MaxLimit thì bị hạ xuống MaxLimit.
	Limit int `json:"limit"`
	// Offset là số bản ghi bỏ qua. Giá trị âm được coi là 0.
	Offset int `json:"offset"`
	// Sort là thứ tự sắp xếp, áp dụng theo đúng thứ tự trong slice.
	Sort []Sort `json:"sort"`
}

// Giới hạn của phân trang theo offset.
const (
	// DefaultLimit là số bản ghi trả về khi client không khai Limit.
	DefaultLimit = 20

	// MaxLimit là trần cứng cho Limit.
	//
	// Có trần vì Limit đến từ client: `?limit=1000000` trên một bảng lớn là một
	// câu query đủ để làm chậm cả database, và không cần ý đồ xấu — chỉ cần một
	// script gõ sai số. Trần được **hạ âm thầm** chứ không trả lỗi, để client cũ
	// không vỡ khi thư viện siết giới hạn.
	MaxLimit = 200
)

// escapeChar là ký tự escape cho pattern của LIKE.
//
// Dùng '!' chứ không phải '\' vì backslash trong chuỗi SQL có nghĩa khác nhau
// giữa Postgres (standard_conforming_strings) và MySQL, nên `ESCAPE '\\'` chạy
// ở một bên là lỗi ở bên kia. '!' không phải ký tự escape ở đâu cả.
const escapeChar = "!"

// Apply thêm các điều kiện filter vào truy vấn.
//
// allowed map từ tên field client gửi lên sang tên cột thật. Field không có
// trong map làm hàm trả lỗi ngay và **không** áp dụng điều kiện nào — không có
// chế độ "bỏ qua field lạ", vì bỏ qua âm thầm nghĩa là client tưởng đã lọc
// trong khi API trả về cả bảng.
//
// Các điều kiện nối với nhau bằng AND. Cần OR thì gọi Apply cho từng nhánh rồi
// tự ghép — một cây điều kiện lồng nhau đến từ client là thứ không giới hạn
// được độ phức tạp, và đó là một cách làm database quá tải.
func Apply(db *gorm.DB, fs []Filter, allowed map[string]string) (*gorm.DB, error) {
	for _, f := range fs {
		col, err := resolve(f.Field, allowed)
		if err != nil {
			return nil, err
		}
		expr, err := condition(db, col, f)
		if err != nil {
			return nil, err
		}
		db = db.Where(expr)
	}
	return db, nil
}

// Paginate trả về scope áp LIMIT, OFFSET và ORDER BY.
//
// Trả về scope thay vì *gorm.DB đã sửa để chỗ gọi ghép được vào đúng chỗ mình
// muốn — quan trọng khi cần đếm tổng số bản ghi: Count phải chạy trên truy vấn
// **chưa** có LIMIT.
//
//	tx, err := query.Apply(gdb.Model(&User{}), filters, allowed)
//	tx.Count(&total)
//	tx.Scopes(scope).Find(&users)
func Paginate(p Page, allowed map[string]string) (func(*gorm.DB) *gorm.DB, error) {
	limit := p.Limit
	switch {
	case limit <= 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	offset := max(p.Offset, 0)

	orderBy, err := orderClause(p.Sort, allowed)
	if err != nil {
		return nil, err
	}

	return func(db *gorm.DB) *gorm.DB {
		if len(orderBy.Columns) > 0 {
			db = db.Clauses(orderBy)
		}
		return db.Limit(limit).Offset(offset)
	}, nil
}

// orderClause dựng ORDER BY từ danh sách Sort.
func orderClause(sorts []Sort, allowed map[string]string) (clause.OrderBy, error) {
	var out clause.OrderBy
	for _, s := range sorts {
		col, err := resolve(s.Field, allowed)
		if err != nil {
			return out, err
		}
		out.Columns = append(out.Columns, clause.OrderByColumn{
			Column: clause.Column{Name: col},
			Desc:   s.Desc,
		})
	}
	return out, nil
}

// resolve tra tên field của client ra tên cột thật.
//
// Đây là toàn bộ lớp chặn SQL injection của package: giá trị trả về luôn là một
// chuỗi do server khai trong allowed, không phải chuỗi client gửi lên.
func resolve(field string, allowed map[string]string) (string, error) {
	col, ok := allowed[field]
	if !ok || col == "" {
		// Thông báo không liệt kê các field hợp lệ: danh sách cột là thông tin
		// về schema, và chỗ gọi mới biết bao nhiêu trong đó nên cho client thấy.
		return "", errs.New(errs.CodeBadRequest,
			fmt.Sprintf("field %q không dùng được để lọc hoặc sắp xếp", field),
			errs.WithField(field, "field không được hỗ trợ"))
	}
	return col, nil
}

// condition dựng biểu thức cho một filter.
//
// Tên cột luôn đi qua clause.Column nên nó được quote theo đúng dialect; giá
// trị luôn đi qua tham số nên nó không bao giờ nằm trong text câu lệnh.
func condition(db *gorm.DB, col string, f Filter) (clause.Expression, error) {
	column := clause.Column{Name: col}

	switch f.Op {
	case Eq, Ne, Gt, Gte, Lt, Lte:
		v, err := scalarValue(f)
		if err != nil {
			return nil, err
		}
		return comparison(f.Op, column, v), nil

	case Like, ILike:
		return likeCondition(db, column, f)

	case In, NotIn:
		return inCondition(column, f)

	case Null:
		want, ok := f.Value.(bool)
		if !ok {
			return nil, badValue(f, "cần giá trị true hoặc false")
		}
		if want {
			return clause.Eq{Column: column, Value: nil}, nil
		}
		return clause.Neq{Column: column, Value: nil}, nil

	default:
		return nil, errs.New(errs.CodeBadRequest,
			fmt.Sprintf("phép so sánh %q không được hỗ trợ", f.Op),
			errs.WithField(f.Field, "phép so sánh không được hỗ trợ"))
	}
}

// comparison dựng biểu thức cho một phép so sánh hai toán hạng.
func comparison(op Op, column clause.Column, v any) clause.Expression {
	switch op {
	case Ne:
		return clause.Neq{Column: column, Value: v}
	case Gt:
		return clause.Gt{Column: column, Value: v}
	case Gte:
		return clause.Gte{Column: column, Value: v}
	case Lt:
		return clause.Lt{Column: column, Value: v}
	case Lte:
		return clause.Lte{Column: column, Value: v}
	default:
		return clause.Eq{Column: column, Value: v}
	}
}

// scalarValue kiểm tra giá trị của một phép so sánh hai toán hạng.
//
// Chặn hai thứ mà GORM sẽ diễn giải lại trong im lặng: nil biến `= ?` thành
// `IS NULL`, và slice biến nó thành `IN (...)`. Cả hai đều có Op riêng
// (Null, In), nên để chúng lọt qua đây chỉ tạo ra hai đường làm cùng một việc —
// và một trong hai đường thì client không hề biết mình đang đi.
func scalarValue(f Filter) (any, error) {
	if f.Value == nil {
		return nil, badValue(f, "cần giá trị khác null, dùng op "+string(Null)+" để so với null")
	}
	switch rv := reflect.ValueOf(f.Value); rv.Kind() {
	case reflect.Slice, reflect.Array:
		// []byte là chuỗi byte, không phải danh sách giá trị.
		if rv.Type().Elem().Kind() != reflect.Uint8 {
			return nil, badValue(f, "cần một giá trị đơn, dùng op "+string(In)+" cho danh sách")
		}
	}
	return f.Value, nil
}

// likeCondition dựng điều kiện "chứa chuỗi".
//
// Giá trị được escape và bọc `%...%`, tức là ngữ nghĩa luôn là *chứa*. Cố ý
// không cho client tự khai pattern: `%` và `_` do client kiểm soát biến một
// câu query có index thành full scan, và một pattern như `%a%a%a%a%a%b` là đủ
// để đốt CPU của database.
//
// ILike dùng ILIKE trên Postgres và LOWER(...) LIKE LOWER(...) ở chỗ khác. Hai
// dạng cho cùng kết quả; chỉ Postgres có toán tử riêng.
func likeCondition(db *gorm.DB, column clause.Column, f Filter) (clause.Expression, error) {
	s, ok := f.Value.(string)
	if !ok {
		return nil, badValue(f, "cần giá trị chuỗi")
	}
	pattern := "%" + escapeLike(s) + "%"

	if d := db.Dialector; f.Op == ILike && d != nil && d.Name() != "postgres" {
		return clause.Expr{
			SQL:  "LOWER(?) LIKE LOWER(?) ESCAPE '" + escapeChar + "'",
			Vars: []any{column, pattern},
		}, nil
	}

	op := "LIKE"
	if f.Op == ILike {
		op = "ILIKE"
	}
	return clause.Expr{
		SQL:  "? " + op + " ? ESCAPE '" + escapeChar + "'",
		Vars: []any{column, pattern},
	}, nil
}

// escapeLike vô hiệu hoá các ký tự đại diện trong giá trị client gửi lên.
//
// escapeChar phải được escape **trước**, nếu không thì các dấu escape do hàm
// này thêm vào lại bị escape lần nữa.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, escapeChar, escapeChar+escapeChar)
	s = strings.ReplaceAll(s, "%", escapeChar+"%")
	return strings.ReplaceAll(s, "_", escapeChar+"_")
}

// inCondition dựng điều kiện IN / NOT IN.
//
// Danh sách rỗng được xử lý tường minh vì SQL `IN ()` là lỗi cú pháp ở cả
// Postgres và MySQL: IN rỗng khớp 0 dòng, NOT IN rỗng khớp mọi dòng.
func inCondition(column clause.Column, f Filter) (clause.Expression, error) {
	values, err := toSlice(f)
	if err != nil {
		return nil, err
	}

	if len(values) == 0 {
		if f.Op == In {
			return alwaysFalse(), nil
		}
		return alwaysTrue(), nil
	}

	if f.Op == In {
		return clause.IN{Column: column, Values: values}, nil
	}
	return clause.Not(clause.IN{Column: column, Values: values}), nil
}

// toSlice đổi Value thành []any, nhận mọi kiểu slice hoặc array.
//
// Dùng reflect vì Value đến từ JSON thì là []any, còn đến từ code Go thì là
// []string hay []int — và ép chỗ gọi phải tự chuyển sang []any là bắt họ viết
// một vòng for cho mỗi lần lọc.
func toSlice(f Filter) ([]any, error) {
	rv := reflect.ValueOf(f.Value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, badValue(f, "cần một danh sách giá trị")
	}
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil, nil
	}

	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, nil
}

// alwaysFalse là điều kiện không khớp dòng nào.
func alwaysFalse() clause.Expression {
	return clause.Expr{SQL: "1 = 0"}
}

// alwaysTrue là điều kiện khớp mọi dòng.
func alwaysTrue() clause.Expression {
	return clause.Expr{SQL: "1 = 1"}
}

// badValue dựng lỗi 400 cho một filter có giá trị sai kiểu.
func badValue(f Filter, why string) error {
	return errs.New(errs.CodeBadRequest,
		fmt.Sprintf("giá trị của filter %q (%s) không hợp lệ: %s", f.Field, f.Op, why),
		errs.WithField(f.Field, why))
}
