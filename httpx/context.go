package httpx

import (
	"context"
	"time"
)

// contextWithStart gắn mốc bắt đầu xử lý request.
func contextWithStart(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, startKey{}, t)
}

// startFrom lấy mốc bắt đầu xử lý, ok = false nếu chưa ai gắn.
func startFrom(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(startKey{}).(time.Time)
	return t, ok
}
