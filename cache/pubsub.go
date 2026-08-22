package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Publish cài đặt [PubSub].
//
// Không trả về số subscriber đã nhận: con số đó gợi ý sai rằng có thể dựa vào
// nó để biết thông điệp đã tới nơi. Pub/Sub của Redis không có ack, và thông
// điệp gửi lúc không ai lắng nghe thì mất hẳn.
func (c *Client) Publish(ctx context.Context, channel string, v any) error {
	raw, err := encode(v)
	if err != nil {
		return err
	}
	return wrap("publish "+channel, c.rdb.Publish(ctx, channel, raw).Err())
}

// Subscribe cài đặt [PubSub].
//
// Chỗ gọi phải Close giá trị trả về khi xong, nếu không thì connection và
// goroutine của nó bị giữ mãi:
//
//	sub := c.Subscribe(ctx, "cache-invalidate")
//	defer sub.Close()
//	for msg := range sub.Channel() {
//	    var ev Event
//	    if err := cache.Decode([]byte(msg.Payload), &ev); err != nil { ... }
//	}
func (c *Client) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.rdb.Subscribe(ctx, channels...)
}
