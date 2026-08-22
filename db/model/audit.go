package model

import (
	"context"
	"errors"
	"reflect"

	"gorm.io/gorm"
)

// Tên hai field audit mà plugin điền. Trùng với field của [Audit].
const (
	fieldCreatedBy = "CreatedBy"
	fieldUpdatedBy = "UpdatedBy"
)

// AuditPlugin trả về plugin GORM tự điền CreatedBy và UpdatedBy.
//
// userFn lấy danh tính người thực hiện từ context — thường là
// ctxmeta.UserID(ctx). Trả về chuỗi rỗng thì plugin không chạm vào hai cột đó,
// nên tác vụ nền không có người dùng vẫn ghi được; muốn phân biệt thì cho userFn
// trả về một giá trị như "system" thay vì rỗng.
//
// Dùng plugin thay vì hook BeforeCreate/BeforeUpdate trên entity vì ba lý do:
//
//   - Áp dụng cho **mọi** entity có hai cột đó, không phải khai lại từng model.
//   - Entity không phải import gì để lấy "người dùng hiện tại", nên tầng model
//     không phụ thuộc tầng transport.
//   - Hook trên entity ghi đè được bằng cách quên gọi, plugin thì không.
//
// Plugin chạy **sau** các hook BeforeCreate/BeforeUpdate của entity, nên giá trị
// nó điền thắng giá trị hook đặt. Đó là chủ ý: cột audit phải nói ai thật sự
// thực hiện, không phải ai được khai là thực hiện. Cùng lý do đó, plugin ghi đè
// cả khi field đã có giá trị sẵn từ chỗ gọi.
//
// Cách dùng:
//
//	gdb, err := db.Open(ctx, cfg)
//	...
//	err = gdb.Use(model.AuditPlugin(ctxmeta.UserID))
//
// Giới hạn cần biết:
//
//   - Câu lệnh không có Model (db.Table("users").Updates(...)) bị bỏ qua: không
//     có schema thì không biết bảng có cột nào, và đoán tên cột là cách âm thầm
//     ghi vào cột sai.
//   - Updates với struct khác kiểu model (Model(&User{}).Updates(patch{}))
//     cũng bị bỏ qua, vì cột audit không nằm trong struct đó. Những chỗ như vậy
//     hãy dùng map hoặc chính kiểu model.
func AuditPlugin(userFn func(context.Context) string) gorm.Plugin {
	return auditPlugin{userFn: userFn}
}

// auditPlugin cài đặt gorm.Plugin.
type auditPlugin struct {
	userFn func(context.Context) string
}

// Name là tên plugin. GORM dùng nó để chặn việc Use hai lần.
func (p auditPlugin) Name() string { return "gokit:audit" }

// Initialize đăng ký callback vào chuỗi create và update.
func (p auditPlugin) Initialize(db *gorm.DB) error {
	if p.userFn == nil {
		return errors.New("model: AuditPlugin cần userFn")
	}
	if err := db.Callback().Create().Before("gorm:create").
		Register("gokit:audit_create", p.onCreate); err != nil {
		return err
	}
	return db.Callback().Update().Before("gorm:update").
		Register("gokit:audit_update", p.onUpdate)
}

// onCreate điền cả CreatedBy và UpdatedBy.
//
// UpdatedBy được điền ngay lúc tạo để hai cột luôn đọc được cùng nhau: một bản
// ghi có UpdatedAt (do autoUpdateTime điền lúc INSERT) mà UpdatedBy rỗng sẽ làm
// mọi báo cáo audit phải xử lý một trường hợp đặc biệt vô nghĩa.
func (p auditPlugin) onCreate(tx *gorm.DB) {
	p.set(tx, fieldCreatedBy, fieldUpdatedBy)
}

// onUpdate chỉ điền UpdatedBy. CreatedBy thuộc về lần tạo.
func (p auditPlugin) onUpdate(tx *gorm.DB) {
	p.set(tx, fieldUpdatedBy)
}

// set ghi user vào các field được nêu, chọn cách ghi theo dạng của Dest.
func (p auditPlugin) set(tx *gorm.DB, fields ...string) {
	if tx.Error != nil || tx.Statement.Schema == nil {
		return
	}
	user := p.userFn(tx.Statement.Context)
	if user == "" {
		return
	}

	for _, name := range fields {
		field := tx.Statement.Schema.LookUpField(name)
		if field == nil {
			continue // entity không có cột này — bình thường, Timestamps thì không có
		}

		switch dest := tx.Statement.Dest.(type) {
		case map[string]any:
			// Dest dạng map dùng **tên cột** làm khoá, không phải tên field Go.
			dest[field.DBName] = user
		case []map[string]any:
			for _, m := range dest {
				m[field.DBName] = user
			}
		default:
			if !destMatchesSchema(tx.Statement) {
				continue
			}
			tx.Statement.SetColumn(name, user, true)
		}
	}
}

// destMatchesSchema cho biết Dest có đúng kiểu của model trong Schema không.
//
// Cần kiểm tra vì Statement.SetColumn ghi giá trị vào Dest theo *chỉ số field*
// lấy từ schema. Khi Dest là một kiểu khác — Model(&User{}).Updates(patch{}) —
// chỉ số đó trỏ vào field khác, hoặc ra ngoài phạm vi struct. Bỏ qua thì mất
// cột audit; không bỏ qua thì ghi sai dữ liệu.
func destMatchesSchema(stmt *gorm.Statement) bool {
	if stmt.Dest == nil {
		return false
	}
	t := reflect.TypeOf(stmt.Dest)
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	return t == stmt.Schema.ModelType
}
