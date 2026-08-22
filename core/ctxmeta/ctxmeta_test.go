package ctxmeta_test

import (
	"context"
	"testing"

	"github.com/cqt002/gokit/core/ctxmeta"
)

func TestFrom_ContextRong(t *testing.T) {
	ctx := context.Background()
	if got := ctxmeta.From(ctx); got != (ctxmeta.Meta{}) {
		t.Errorf("From(rỗng) = %+v, muốn Meta rỗng", got)
	}
	for name, fn := range map[string]func(context.Context) string{
		"RequestID":     ctxmeta.RequestID,
		"CorrelationID": ctxmeta.CorrelationID,
		"UserID":        ctxmeta.UserID,
		"UserType":      ctxmeta.UserType,
	} {
		if got := fn(ctx); got != "" {
			t.Errorf("%s(rỗng) = %q, muốn rỗng", name, got)
		}
	}
}

func TestWithVaFrom(t *testing.T) {
	want := ctxmeta.Meta{
		RequestID:     "req-1",
		CorrelationID: "corr-1",
		UserID:        "user-1",
		UserType:      "employee",
	}
	ctx := ctxmeta.With(context.Background(), want)

	if got := ctxmeta.From(ctx); got != want {
		t.Errorf("From = %+v, muốn %+v", got, want)
	}
	if got := ctxmeta.RequestID(ctx); got != want.RequestID {
		t.Errorf("RequestID = %q", got)
	}
	if got := ctxmeta.CorrelationID(ctx); got != want.CorrelationID {
		t.Errorf("CorrelationID = %q", got)
	}
	if got := ctxmeta.UserID(ctx); got != want.UserID {
		t.Errorf("UserID = %q", got)
	}
	if got := ctxmeta.UserType(ctx); got != want.UserType {
		t.Errorf("UserType = %q", got)
	}
}

// With thay cả struct — hợp đồng này phải rõ, vì nó khác hẳn các hàm WithX.
func TestWith_ThayToanBo(t *testing.T) {
	ctx := ctxmeta.With(context.Background(), ctxmeta.Meta{RequestID: "req-1", UserID: "user-1"})
	ctx = ctxmeta.With(ctx, ctxmeta.Meta{RequestID: "req-2"})

	if got := ctxmeta.From(ctx); got != (ctxmeta.Meta{RequestID: "req-2"}) {
		t.Errorf("From = %+v, muốn chỉ còn req-2", got)
	}
}

// Ngược lại, WithX phải cộng dồn: middleware đặt request ID trước, tầng auth đặt
// user ID sau, và không tầng nào được xoá dữ liệu của tầng kia.
func TestWithX_CongDon(t *testing.T) {
	ctx := context.Background()
	ctx = ctxmeta.WithRequestID(ctx, "req-1")
	ctx = ctxmeta.WithCorrelationID(ctx, "corr-1")
	ctx = ctxmeta.WithUserID(ctx, "user-1")
	ctx = ctxmeta.WithUserType(ctx, "customer")

	want := ctxmeta.Meta{
		RequestID:     "req-1",
		CorrelationID: "corr-1",
		UserID:        "user-1",
		UserType:      "customer",
	}
	if got := ctxmeta.From(ctx); got != want {
		t.Errorf("From = %+v, muốn %+v", got, want)
	}
}

func TestWithX_GhiDeCungField(t *testing.T) {
	ctx := ctxmeta.WithRequestID(context.Background(), "req-1")
	ctx = ctxmeta.WithRequestID(ctx, "req-2")

	if got := ctxmeta.RequestID(ctx); got != "req-2" {
		t.Errorf("RequestID = %q, muốn req-2", got)
	}
}

// Context là immutable: sửa ở nhánh con không được ảnh hưởng context cha, nếu
// không thì hai request xử lý song song có thể đọc metadata của nhau.
func TestContextCha_KhongBiAnhHuong(t *testing.T) {
	parent := ctxmeta.WithRequestID(context.Background(), "req-1")
	child := ctxmeta.WithUserID(parent, "user-1")

	if got := ctxmeta.UserID(parent); got != "" {
		t.Errorf("context cha bị sửa: UserID = %q", got)
	}
	if got := ctxmeta.RequestID(child); got != "req-1" {
		t.Errorf("context con mất RequestID của cha: %q", got)
	}
}
