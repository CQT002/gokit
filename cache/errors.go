package cache

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/core/errs"
)

// CodeMiss là mã lỗi khi key không có trong cache.
//
// Có mã riêng thay vì dùng errs.CodeNotFound: "không có trong cache" và "không
// có trong hệ thống" là hai chuyện khác nhau, và gộp lại thì một cache miss có
// thể lặng lẽ biến thành 404 trả cho client.
//
// Mã này **không** được đăng ký vào bảng của errs, nên nếu nó lọt ra tới tầng
// HTTP thì thành 500 — đúng, vì cache miss lọt ra tới đó là lỗi lập trình.
const CodeMiss = errs.Code("cache_miss")

// ErrMiss là lỗi trả về khi key không tồn tại hoặc đã hết hạn.
//
// So khớp bằng errors.Is, không phải so sánh con trỏ:
//
//	if err := c.Get(ctx, key, &u); errors.Is(err, cache.ErrMiss) { ... }
//
// Nhờ nó, code nghiệp vụ không phải import go-redis chỉ để so với redis.Nil.
var ErrMiss = errs.New(CodeMiss, "cache miss")

// wrap đổi lỗi của go-redis thành lỗi của package này.
//
// redis.Nil là "không có giá trị", không phải sự cố — go-redis trả nó cho GET
// trên key không tồn tại. Mọi lỗi khác được bọc kèm tên thao tác để biết chỗ
// nào trong luồng đã lỗi.
func wrap(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, redis.Nil):
		return ErrMiss
	default:
		return fmt.Errorf("cache: %s: %w", op, err)
	}
}
