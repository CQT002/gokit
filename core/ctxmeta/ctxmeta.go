// Package ctxmeta mang metadata phạm vi request qua context.Context.
//
// Toàn bộ metadata nằm trong một struct dưới một context key duy nhất, chứ không
// phải mỗi field một key. Một dòng access log cần cả 4 field: nếu tách key thì
// đó là 4 lần đi ngược chuỗi context lồng nhau cho mỗi dòng log, còn gộp lại thì
// chỉ 1 lần.
//
// Metadata ở đây là thứ nhận diện request và người gọi. Thông tin trace nằm ở
// package tracectx — tách ra vì trace có chuẩn riêng (W3C) và vòng đời riêng.
package ctxmeta

import "context"

// Meta là metadata của một request.
//
// Mọi field đều là string và có thể rỗng: giá trị rỗng nghĩa là "không biết",
// không phải lỗi. Chỗ ghi log cứ ghi những gì có.
type Meta struct {
	// RequestID nhận diện đúng một request vào service này.
	RequestID string
	// CorrelationID do client hoặc service gọi trước đặt, dùng để nối các
	// request thuộc cùng một nghiệp vụ.
	CorrelationID string
	// UserID là chủ thể đã xác thực của request.
	UserID string
	// UserType phân loại chủ thể (ví dụ khách hàng, nhân viên, service).
	UserType string
}

type ctxKey struct{}

// With gắn m vào ctx, thay thế toàn bộ Meta cũ nếu có.
//
// Muốn sửa một field mà giữ phần còn lại thì dùng các hàm WithX bên dưới.
func With(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, ctxKey{}, m)
}

// From lấy Meta trong ctx, trả về Meta rỗng nếu chưa có gì.
//
// Không trả về cờ ok: chỗ gọi hầu hết là code ghi log, và ở đó "chưa có metadata"
// với "có nhưng rỗng" xử lý y như nhau.
func From(ctx context.Context) Meta {
	m, _ := ctx.Value(ctxKey{}).(Meta)
	return m
}

// WithRequestID đặt RequestID, giữ nguyên các field khác.
func WithRequestID(ctx context.Context, id string) context.Context {
	m := From(ctx)
	m.RequestID = id
	return With(ctx, m)
}

// WithCorrelationID đặt CorrelationID, giữ nguyên các field khác.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	m := From(ctx)
	m.CorrelationID = id
	return With(ctx, m)
}

// WithUserID đặt UserID, giữ nguyên các field khác.
func WithUserID(ctx context.Context, id string) context.Context {
	m := From(ctx)
	m.UserID = id
	return With(ctx, m)
}

// WithUserType đặt UserType, giữ nguyên các field khác.
func WithUserType(ctx context.Context, t string) context.Context {
	m := From(ctx)
	m.UserType = t
	return With(ctx, m)
}

// RequestID trả về RequestID trong ctx, rỗng nếu chưa có.
func RequestID(ctx context.Context) string { return From(ctx).RequestID }

// CorrelationID trả về CorrelationID trong ctx, rỗng nếu chưa có.
func CorrelationID(ctx context.Context) string { return From(ctx).CorrelationID }

// UserID trả về UserID trong ctx, rỗng nếu chưa có.
func UserID(ctx context.Context) string { return From(ctx).UserID }

// UserType trả về UserType trong ctx, rỗng nếu chưa có.
func UserType(ctx context.Context) string { return From(ctx).UserType }
