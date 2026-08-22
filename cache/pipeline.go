package cache

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// Pipelined cài đặt [Pipeline].
//
// Lỗi của từng lệnh nằm trên từng redis.Cmder trong slice trả về, không phải ở
// error trả về — error chỉ báo lỗi ở tầng vận chuyển. Nghĩa là phải kiểm cả hai:
//
//	cmds, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
//	    p.Incr(ctx, "a")
//	    p.Expire(ctx, "a", time.Minute)
//	    return nil
//	})
//	if err != nil { return err }
//	for _, cmd := range cmds {
//	    if cmd.Err() != nil { ... }
//	}
func (c *Client) Pipelined(ctx context.Context, fn func(p redis.Pipeliner) error) ([]redis.Cmder, error) {
	cmds, err := c.rdb.Pipelined(ctx, fn)
	if err != nil && !errors.Is(err, redis.Nil) {
		return cmds, wrap("pipelined", err)
	}
	return cmds, nil
}

// TxPipelined cài đặt [Pipeline].
func (c *Client) TxPipelined(ctx context.Context, fn func(p redis.Pipeliner) error) ([]redis.Cmder, error) {
	cmds, err := c.rdb.TxPipelined(ctx, fn)
	if err != nil && !errors.Is(err, redis.Nil) {
		return cmds, wrap("tx_pipelined", err)
	}
	return cmds, nil
}

// Vì sao redis.Nil được lọc riêng: go-redis trả lỗi **đầu tiên trong lô** ở giá
// trị error, và một GET không tìm thấy trong pipeline là chuyện bình thường.
// Biến nó thành lỗi của cả pipeline sẽ khiến chỗ gọi tưởng cả lô đã thất bại
// trong khi các lệnh khác đều xong. Lỗi thật của từng lệnh vẫn nằm trên Cmder
// tương ứng.
