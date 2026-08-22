package model_test

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cqt002/gokit/db/model"
)

// auditUser ghép cả bốn mảnh base entity.
type auditUser struct {
	model.UUIDKey
	model.Audit
	model.SoftDelete
	Name string
}

func (auditUser) TableName() string { return "users" }

// plainUser chỉ có Timestamps — không có cột audit nào.
type plainUser struct {
	model.UUIDKey
	model.Timestamps
	Name string
}

func (plainUser) TableName() string { return "users" }

// patch là struct khác kiểu model, dùng cho Updates. Không có cột audit.
type patch struct {
	Name string
}

type userKey struct{}

// userFn lấy danh tính từ context, giống cách ctxmeta.UserID hoạt động.
func userFn(ctx context.Context) string {
	s, _ := ctx.Value(userKey{}).(string)
	return s
}

func ctxWithUser(user string) context.Context {
	return context.WithValue(context.Background(), userKey{}, user)
}

// newDB dựng *gorm.DB chế độ DryRun đã gắn AuditPlugin.
func newDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb, err := gorm.Open(
		postgres.New(postgres.Config{DSN: "postgres://u:p@127.0.0.1:5432/d?sslmode=disable"}),
		&gorm.Config{
			DryRun:               true,
			DisableAutomaticPing: true,
			// DryRun không chặn BEGIN: callback mở transaction vẫn cần
			// connection thật. Tắt transaction mặc định để test chạy offline.
			SkipDefaultTransaction: true,
			Logger:                 logger.Discard,
		})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	if err := gdb.Use(model.AuditPlugin(userFn)); err != nil {
		t.Fatalf("Use(AuditPlugin): %v", err)
	}
	return gdb
}

// countVar đếm số lần một giá trị xuất hiện trong tham số câu lệnh.
func countVar(vars []any, want string) int {
	n := 0
	for _, v := range vars {
		if s, ok := v.(string); ok && s == want {
			n++
		}
	}
	return n
}

func TestAuditPlugin_CanUserFn(t *testing.T) {
	gdb, err := gorm.Open(
		postgres.New(postgres.Config{DSN: "postgres://u:p@127.0.0.1:5432/d?sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Discard})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}

	if err := gdb.Use(model.AuditPlugin(nil)); err == nil {
		t.Fatal("AuditPlugin(nil) mà Use không báo lỗi")
	}
}

func TestAuditPlugin_Create(t *testing.T) {
	gdb := newDB(t)
	u := auditUser{UUIDKey: model.UUIDKey{ID: "u-1"}, Name: "An"}

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).Create(&u)
	if tx.Error != nil {
		t.Fatalf("Create: %v", tx.Error)
	}

	// Cả hai cột phải có giá trị: một bản ghi có UpdatedAt mà UpdatedBy rỗng
	// buộc mọi báo cáo audit xử lý một trường hợp đặc biệt vô nghĩa.
	if got := countVar(tx.Statement.Vars, "nhanvien-7"); got != 2 {
		t.Errorf("số tham số mang danh tính = %d, muốn 2 (created_by và updated_by)", got)
	}
	if u.CreatedBy != "nhanvien-7" {
		t.Errorf("u.CreatedBy = %q", u.CreatedBy)
	}
	if u.UpdatedBy != "nhanvien-7" {
		t.Errorf("u.UpdatedBy = %q", u.UpdatedBy)
	}
}

func TestAuditPlugin_CreateNhieuBanGhi(t *testing.T) {
	gdb := newDB(t)
	us := []auditUser{
		{UUIDKey: model.UUIDKey{ID: "u-1"}, Name: "An"},
		{UUIDKey: model.UUIDKey{ID: "u-2"}, Name: "Bình"},
	}

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).Create(&us)
	if tx.Error != nil {
		t.Fatalf("Create: %v", tx.Error)
	}

	for i, u := range us {
		if u.CreatedBy != "nhanvien-7" || u.UpdatedBy != "nhanvien-7" {
			t.Errorf("us[%d] chưa được điền: %+v", i, u)
		}
	}
}

// Giá trị chỗ gọi tự đặt bị ghi đè: cột audit phải nói ai thật sự thực hiện.
func TestAuditPlugin_GhiDeGiaTriDaCo(t *testing.T) {
	gdb := newDB(t)
	u := auditUser{
		UUIDKey: model.UUIDKey{ID: "u-1"},
		Audit:   model.Audit{CreatedBy: "kẻ-mạo-danh"},
		Name:    "An",
	}

	if err := gdb.WithContext(ctxWithUser("nhanvien-7")).Create(&u).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.CreatedBy != "nhanvien-7" {
		t.Errorf("u.CreatedBy = %q, muốn giá trị từ context", u.CreatedBy)
	}
}

func TestAuditPlugin_UpdateChiDienUpdatedBy(t *testing.T) {
	gdb := newDB(t)
	u := auditUser{UUIDKey: model.UUIDKey{ID: "u-1"}, Name: "An"}

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).Model(&u).Updates(&u)
	if tx.Error != nil {
		t.Fatalf("Updates: %v", tx.Error)
	}

	sql := tx.Statement.SQL.String()
	if !strings.Contains(sql, `"updated_by"=`) {
		t.Errorf("SQL không cập nhật updated_by: %q", sql)
	}
	if strings.Contains(sql, `"created_by"=`) {
		t.Errorf("update mà sửa cả created_by: %q", sql)
	}
	if u.UpdatedBy != "nhanvien-7" {
		t.Errorf("u.UpdatedBy = %q", u.UpdatedBy)
	}
}

// Dest dạng map dùng tên cột làm khoá, không phải tên field Go.
func TestAuditPlugin_UpdateVoiMap(t *testing.T) {
	gdb := newDB(t)

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).
		Model(&auditUser{}).Where("id = ?", "u-1").
		Updates(map[string]any{"name": "An"})
	if tx.Error != nil {
		t.Fatalf("Updates: %v", tx.Error)
	}

	sql := tx.Statement.SQL.String()
	if !strings.Contains(sql, `"updated_by"=`) {
		t.Errorf("SQL không cập nhật updated_by: %q", sql)
	}
	if countVar(tx.Statement.Vars, "nhanvien-7") != 1 {
		t.Errorf("vars = %v, muốn chứa danh tính", tx.Statement.Vars)
	}
}

// Không có danh tính thì không chạm vào cột audit — tác vụ nền vẫn ghi được.
func TestAuditPlugin_KhongCoDanhTinh(t *testing.T) {
	gdb := newDB(t)
	u := auditUser{UUIDKey: model.UUIDKey{ID: "u-1"}, Name: "An"}

	tx := gdb.WithContext(context.Background()).Create(&u)
	if tx.Error != nil {
		t.Fatalf("Create: %v", tx.Error)
	}
	if u.CreatedBy != "" {
		t.Errorf("u.CreatedBy = %q, muốn rỗng", u.CreatedBy)
	}
}

// Entity không có cột audit vẫn ghi được, plugin chỉ bỏ qua.
func TestAuditPlugin_EntityKhongCoCotAudit(t *testing.T) {
	gdb := newDB(t)
	u := plainUser{UUIDKey: model.UUIDKey{ID: "u-1"}, Name: "An"}

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).Create(&u)
	if tx.Error != nil {
		t.Fatalf("Create: %v", tx.Error)
	}
	if countVar(tx.Statement.Vars, "nhanvien-7") != 0 {
		t.Errorf("plugin ghi danh tính vào entity không có cột audit: %v", tx.Statement.Vars)
	}
}

// Updates với struct khác kiểu model: bỏ qua chứ không ghi vào field sai chỗ.
func TestAuditPlugin_UpdatesVoiStructKhacKieu(t *testing.T) {
	gdb := newDB(t)

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).
		Model(&auditUser{}).Where("id = ?", "u-1").
		Updates(patch{Name: "An"})
	if tx.Error != nil {
		t.Fatalf("Updates: %v", tx.Error)
	}
	if countVar(tx.Statement.Vars, "nhanvien-7") != 0 {
		t.Errorf("plugin ghi vào struct không có cột audit: %v", tx.Statement.Vars)
	}
}

// Câu lệnh không có Model thì không có schema, và đoán tên cột là cách âm thầm
// ghi vào cột sai.
func TestAuditPlugin_KhongCoModel(t *testing.T) {
	gdb := newDB(t)

	tx := gdb.WithContext(ctxWithUser("nhanvien-7")).
		Table("users").Where("id = ?", "u-1").
		Updates(map[string]any{"name": "An"})
	if tx.Error != nil {
		t.Fatalf("Updates: %v", tx.Error)
	}
	if countVar(tx.Statement.Vars, "nhanvien-7") != 0 {
		t.Errorf("plugin ghi danh tính khi không có schema: %v", tx.Statement.Vars)
	}
}

// Nhúng SoftDelete làm mọi truy vấn tự thêm điều kiện, và Delete thành UPDATE.
func TestSoftDelete(t *testing.T) {
	gdb := newDB(t)

	tx := gdb.Model(&auditUser{}).Find(&[]auditUser{})
	if tx.Error != nil {
		t.Fatalf("Find: %v", tx.Error)
	}
	if !strings.Contains(tx.Statement.SQL.String(), `"users"."deleted_at" IS NULL`) {
		t.Errorf("truy vấn không lọc bản ghi đã xoá: %q", tx.Statement.SQL.String())
	}

	tx = gdb.Where("id = ?", "u-1").Delete(&auditUser{})
	if tx.Error != nil {
		t.Fatalf("Delete: %v", tx.Error)
	}
	if !strings.HasPrefix(tx.Statement.SQL.String(), "UPDATE") {
		t.Errorf("Delete không thành UPDATE: %q", tx.Statement.SQL.String())
	}
}

func TestUUIDKey_LaKhoaChinh(t *testing.T) {
	gdb := newDB(t)

	// First sắp xếp theo khoá chính, nên ORDER BY cho biết GORM nhận id là khoá.
	tx := gdb.Model(&auditUser{}).First(&auditUser{})
	if tx.Error != nil {
		t.Fatalf("First: %v", tx.Error)
	}
	if !strings.Contains(tx.Statement.SQL.String(), `ORDER BY "users"."id"`) {
		t.Errorf("id không được dùng làm khoá chính: %q", tx.Statement.SQL.String())
	}
}
