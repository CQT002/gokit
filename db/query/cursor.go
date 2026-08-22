package query

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/cqt002/gokit/core/errs"
)

// Cursor là con trỏ trang, dạng chuỗi an toàn để đặt trong URL.
//
// Nó mã hoá giá trị các cột sắp xếp của **bản ghi cuối** trang trước. Nội dung
// chỉ được encode base64, **không** được mã hoá hay ký: coi nó là thứ client
// đọc và sửa được. Đừng bao giờ nhét thông tin phải giữ kín — hay điều kiện lọc
// mang tính phân quyền — vào cursor; những thứ đó thuộc về server.
type Cursor string

// MaxCursorBytes là giới hạn kích thước cursor lúc giải mã.
//
// Cursor đến từ client nên nó là input không tin được: không có trần thì một
// chuỗi vài megabyte biến mỗi request thành một lần cấp phát vài megabyte.
const MaxCursorBytes = 4 << 10

// EncodeCursor mã hoá giá trị các cột sắp xếp thành Cursor.
//
// vals là map từ **tên field client dùng** (khoá của map allowed) sang giá trị
// của bản ghi cuối trang. Dùng tên field chứ không phải tên cột để cursor không
// phơi ra schema database, và để đổi tên cột không làm hỏng cursor đang lưu ở
// client.
//
// map rỗng hoặc nil cho ra cursor rỗng — nghĩa là "trang đầu".
func EncodeCursor(vals map[string]any) Cursor {
	if len(vals) == 0 {
		return ""
	}
	b, err := json.Marshal(vals)
	if err != nil {
		// Chỉ xảy ra khi vals chứa kiểu không marshal được (channel, func).
		// Đó là lỗi lập trình, và cursor rỗng làm nó lộ ra ngay ở trang sau.
		return ""
	}
	// RawURLEncoding: không có padding '=' nên cursor đặt được thẳng vào query
	// string mà không phải escape lần nữa.
	return Cursor(base64.RawURLEncoding.EncodeToString(b))
}

// DecodeCursor giải mã Cursor về map giá trị.
//
// Cursor rỗng trả về map rỗng và không có lỗi: đó là trang đầu, không phải lỗi
// của client.
func DecodeCursor(c Cursor) (map[string]any, error) {
	if c == "" {
		return map[string]any{}, nil
	}
	if len(c) > MaxCursorBytes {
		return nil, errs.New(errs.CodeBadRequest, "cursor quá dài")
	}

	// Nhận cả dạng có padding: cursor đi qua nhiều tầng công cụ và không phải
	// tầng nào cũng giữ nguyên chuỗi.
	raw, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(string(c))
	}
	if err != nil {
		return nil, errs.New(errs.CodeBadRequest, "cursor không hợp lệ")
	}

	var vals map[string]any
	if err := json.Unmarshal(raw, &vals); err != nil {
		// Không bọc err: thông báo của json chứa nội dung cursor, tức là chứa
		// chuỗi do client gửi lên. Chi tiết thật nằm ở lỗi được bọc bên trong.
		return nil, errs.New(errs.CodeBadRequest, "cursor không hợp lệ",
			errs.WithCause(err))
	}
	if vals == nil {
		// JSON "null" giải mã ra map nil mà không lỗi.
		return nil, errs.New(errs.CodeBadRequest, "cursor không hợp lệ")
	}
	return vals, nil
}

// ApplyCursor áp phân trang keyset: ORDER BY theo sort, cộng điều kiện "sau
// bản ghi mà cursor trỏ tới".
//
// So với LIMIT/OFFSET, keyset không chậm dần theo số trang: OFFSET 100000 buộc
// database đọc và bỏ đi 100000 dòng, còn keyset đi thẳng vào index. Đổi lại,
// không nhảy được tới trang số N — chỉ có "trang tiếp theo". Với bảng lớn hoặc
// scroll vô hạn thì đó là đánh đổi đúng.
//
// Yêu cầu để kết quả đúng:
//
//   - sort phải không rỗng, và **tổ hợp các field trong sort phải là duy nhất**
//     cho mỗi dòng. Thường là thêm khoá chính vào cuối: sort theo created_at rồi
//     tới id. Thiếu điều kiện này thì các dòng trùng giá trị sắp xếp sẽ bị lặp
//     hoặc bị bỏ qua giữa hai trang.
//   - Cursor phải chứa đủ giá trị của mọi field trong sort.
//   - Thứ tự sort giữa các trang phải giống nhau. Đổi thứ tự thì phải bắt đầu
//     lại từ cursor rỗng.
//
// Hàm này **không** áp LIMIT: chỗ gọi tự quyết bằng db.Limit, thường là
// limit+1 để biết có còn trang sau hay không.
func ApplyCursor(db *gorm.DB, c Cursor, sort []Sort, allowed map[string]string) (*gorm.DB, error) {
	if len(sort) == 0 {
		return nil, errs.New(errs.CodeBadRequest, "phân trang bằng cursor cần ít nhất một tiêu chí sắp xếp")
	}

	orderBy, err := orderClause(sort, allowed)
	if err != nil {
		return nil, err
	}

	vals, err := DecodeCursor(c)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return db.Clauses(orderBy), nil
	}

	// Điều kiện keyset là dạng khai triển của so sánh từ điển
	// (a, b, c) > (a0, b0, c0), viết ra thành OR các nhánh AND:
	//
	//	a > a0
	//	OR (a = a0 AND b < b0)          -- b sắp giảm dần
	//	OR (a = a0 AND b = b0 AND c > c0)
	//
	// Không dùng cú pháp row-value `(a, b) > (?, ?)` của SQL chuẩn vì nó chỉ
	// đúng khi mọi cột cùng chiều sắp xếp, và MySQL không dùng được index cho
	// dạng đó.
	branches := make([]clause.Expression, 0, len(sort))
	for i, s := range sort {
		branch := make([]clause.Expression, 0, i+1)

		for j := range i {
			col, val, err := cursorValue(sort[j], allowed, vals)
			if err != nil {
				return nil, err
			}
			branch = append(branch, clause.Eq{Column: col, Value: val})
		}

		col, val, err := cursorValue(s, allowed, vals)
		if err != nil {
			return nil, err
		}
		if s.Desc {
			branch = append(branch, clause.Lt{Column: col, Value: val})
		} else {
			branch = append(branch, clause.Gt{Column: col, Value: val})
		}

		branches = append(branches, clause.And(branch...))
	}

	// Một nhánh thì đưa thẳng biểu thức vào Where. Không bọc clause.Or: GORM
	// nối một OrConditions chỉ có một phần tử vào các điều kiện khác bằng OR
	// chứ không phải AND, nên bọc lại sẽ làm mọi filter khác mất tác dụng.
	if len(branches) == 1 {
		return db.Where(branches[0]).Clauses(orderBy), nil
	}
	return db.Where(clause.Or(branches...)).Clauses(orderBy), nil
}

// cursorValue tra tên cột và giá trị trong cursor cho một tiêu chí sắp xếp.
func cursorValue(s Sort, allowed map[string]string, vals map[string]any) (clause.Column, any, error) {
	col, err := resolve(s.Field, allowed)
	if err != nil {
		return clause.Column{}, nil, err
	}
	val, ok := vals[s.Field]
	if !ok {
		return clause.Column{}, nil, errs.New(errs.CodeBadRequest,
			fmt.Sprintf("cursor thiếu giá trị cho field %q", s.Field))
	}
	if val == nil {
		// NULL không so sánh được bằng >, <, = — mọi phép so với NULL đều cho
		// UNKNOWN, nên nhánh chứa nó lặng lẽ không khớp dòng nào và trang sau
		// trả về rỗng. Chặn ở đây để lỗi lộ ra thay vì mất dữ liệu.
		return clause.Column{}, nil, errs.New(errs.CodeBadRequest,
			fmt.Sprintf("cursor có giá trị null cho field %q", s.Field))
	}
	return clause.Column{Name: col}, val, nil
}
