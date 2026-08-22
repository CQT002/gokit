// Package model cung cấp các mảnh base entity ghép được và plugin tự điền
// người tạo/người sửa.
//
// Cách tiếp cận là **ghép mảnh**, không phải kế thừa một BaseModel to. Bốn
// mảnh dưới đây tổ hợp ra đủ mọi biến thể thường dùng:
//
//	type User struct {
//	    model.UUIDKey
//	    model.Audit
//	    model.SoftDelete
//	    Name string
//	}
//
// Một BaseModel gộp sẵn cả bốn thứ sẽ buộc mọi bảng phải có soft delete và cột
// audit, kể cả bảng tra cứu tĩnh — và cái giá của cột thừa là index thừa. Mảnh
// rời thì mỗi bảng khai đúng những gì nó cần, và không có combo nào phải đặt
// tên.
//
// Các mảnh này **không** import gì ngoài GORM: model là thứ nằm ở tầng thấp
// nhất của app, cho nó phụ thuộc vào core là mở đường cho chu trình phụ thuộc.
package model

import (
	"time"

	"gorm.io/gorm"
)

// Timestamps là cặp thời điểm tạo/sửa.
//
// Giá trị do GORM điền qua tag autoCreateTime/autoUpdateTime, tức là lấy giờ
// của **process**, không phải của database. Đánh đổi có ý thức: lấy giờ database
// cần thêm một vòng round-trip hoặc DEFAULT ở tầng DDL, còn lệch giờ giữa các
// pod là thứ NTP đã giải quyết.
type Timestamps struct {
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// Audit là Timestamps kèm người thực hiện.
//
// Hai cột CreatedBy/UpdatedBy được AuditPlugin tự điền, nên entity không cần
// hook nào và cũng không cần biết "người dùng hiện tại" được lấy từ đâu.
//
// Không nhúng Timestamps vào đây: nhúng struct lồng nhau làm GORM phải đi thêm
// một tầng khi resolve field, và quan trọng hơn là một entity nhúng cả
// Timestamps lẫn Audit sẽ có hai cột CreatedAt trùng tên mà lỗi chỉ hiện ra lúc
// chạy. Bốn dòng lặp lại rẻ hơn cái bẫy đó.
type Audit struct {
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	CreatedBy string    `gorm:"size:64" json:"created_by"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
	UpdatedBy string    `gorm:"size:64" json:"updated_by"`
}

// SoftDelete thêm cột đánh dấu đã xoá.
//
// Nhúng nó vào entity làm **mọi** truy vấn của GORM trên entity đó tự thêm
// `deleted_at IS NULL`, và Delete chuyển thành UPDATE. Muốn lấy cả bản đã xoá
// thì dùng Unscoped.
//
// Cột có index vì điều kiện `deleted_at IS NULL` xuất hiện trong mọi câu query
// của bảng; thiếu index ở đây là một quyết định thiết kế chứ không phải tối ưu
// sau.
type SoftDelete struct {
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitzero"`
}

// UUIDKey là khoá chính dạng UUID ở kiểu string.
//
// Không tự sinh giá trị: sinh ID là việc của tầng nghiệp vụ (dùng core/idx),
// và một plugin âm thầm điền khoá chính làm mọi lỗi "quên set ID" biến thành
// dữ liệu rác thay vì thành lỗi.
//
// Kiểu string thay vì [16]byte hay uuid.UUID để mảnh này không kéo theo phụ
// thuộc nào; tag `type:uuid` vẫn cho Postgres cột uuid thật. Với MySQL hãy ghi
// đè tag thành `type:char(36)`.
type UUIDKey struct {
	ID string `gorm:"primaryKey;type:uuid" json:"id"`
}
