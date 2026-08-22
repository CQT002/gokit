package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// KV là các thao tác trên key đơn.
//
// Đây là interface được dùng nhiều nhất, và cũng là lý do package này không có
// một interface to: một repository chỉ cache theo key thì khai phụ thuộc vào
// KV và mock được trong vài dòng.
type KV interface {
	// Get đọc giá trị của key vào dst.
	//
	// Trả [ErrMiss] khi key không tồn tại hoặc đã hết hạn — dùng errors.Is để
	// so khớp. dst phải là con trỏ.
	Get(ctx context.Context, key string, dst any) error

	// Set ghi giá trị. ttl <= 0 nghĩa là không hết hạn.
	Set(ctx context.Context, key string, v any, ttl time.Duration) error

	// SetNX ghi giá trị **chỉ khi** key chưa tồn tại. Trả về true nếu đã ghi.
	//
	// Đây là thao tác nguyên tử của Redis, nên nó dùng được làm khoá đơn giản.
	// Cần khoá có gia hạn và có context bị cancel khi mất khoá thì dùng
	// [github.com/cqt002/gokit/cache/lock].
	SetNX(ctx context.Context, key string, v any, ttl time.Duration) (bool, error)

	// Del xoá các key. Key không tồn tại không phải lỗi.
	Del(ctx context.Context, keys ...string) error

	// Exists trả về số key trong danh sách đang tồn tại.
	Exists(ctx context.Context, keys ...string) (int64, error)

	// TTL trả về thời gian còn lại của key.
	//
	// Trả [ErrMiss] nếu key không tồn tại, và 0 nếu key tồn tại nhưng không có
	// hạn. Redis phân biệt hai trường hợp đó bằng hai giá trị âm khác nhau, một
	// chi tiết rất dễ đọc sai — nên nó được dịch ra ở đây.
	TTL(ctx context.Context, key string) (time.Duration, error)

	// Expire đặt hạn cho key đang tồn tại.
	Expire(ctx context.Context, key string, ttl time.Duration) error

	// Incr cộng by vào giá trị số của key và trả về giá trị mới.
	//
	// Key chưa tồn tại được coi là 0. by âm nghĩa là trừ.
	Incr(ctx context.Context, key string, by int64) (int64, error)

	// MGet đọc nhiều key trong một lần round-trip.
	//
	// dst phải là con trỏ tới slice; slice nhận đúng len(keys) phần tử, **theo
	// thứ tự của keys**, và phần tử ứng với key không tồn tại giữ giá trị zero.
	MGet(ctx context.Context, keys []string, dst any) error

	// Scan quét key theo pattern, một trang mỗi lần gọi.
	//
	// Trả về cursor cho lần gọi sau; cursor 0 nghĩa là đã quét hết. cursor 0 ở
	// lần gọi đầu nghĩa là bắt đầu từ đầu.
	//
	// Chỉ dùng được ở chế độ standalone. Xem godoc của [Client.Scan].
	Scan(ctx context.Context, pattern string, cursor uint64, count int64) ([]string, uint64, error)
}

// Hash là các thao tác trên hash.
//
// Hash đáng dùng khi một entity có nhiều field và chỉ vài field bị đọc/ghi mỗi
// lần: đọc một field của hash rẻ hơn đọc rồi giải mã cả struct JSON.
type Hash interface {
	// HGet đọc một field vào dst. Trả [ErrMiss] nếu key hoặc field không có.
	HGet(ctx context.Context, key, field string, dst any) error

	// HSet ghi một hoặc nhiều field. values là các cặp field, giá trị.
	HSet(ctx context.Context, key string, values map[string]any) error

	// HSetNX ghi field **chỉ khi** field chưa tồn tại. Trả về true nếu đã ghi.
	HSetNX(ctx context.Context, key, field string, v any) (bool, error)

	// HDel xoá các field.
	HDel(ctx context.Context, key string, fields ...string) error

	// HGetAll đọc toàn bộ hash thành map từ field sang bytes thô.
	//
	// Trả bytes chứ không giải mã sẵn: các field của một hash thường khác kiểu
	// nhau, nên không có một dst nào đúng cho cả hash. Giải mã từng field bằng
	// [Decode].
	HGetAll(ctx context.Context, key string) (map[string][]byte, error)

	// HMGet đọc nhiều field trong một lần round-trip, theo thứ tự của fields.
	// Field không tồn tại cho phần tử nil.
	HMGet(ctx context.Context, key string, fields ...string) ([][]byte, error)

	// HIncrBy cộng by vào field và trả về giá trị mới.
	HIncrBy(ctx context.Context, key, field string, by int64) (int64, error)
}

// PubSub là phát và nhận thông điệp.
//
// Pub/Sub của Redis là **fire and forget**: thông điệp gửi lúc không có ai
// subscribe thì mất hẳn, và không có ack. Dùng nó cho việc vô hại khi mất —
// xoá cache local, đẩy thông báo realtime. Việc không được mất thì dùng Stream
// hoặc Kafka.
type PubSub interface {
	// Publish gửi một thông điệp tới channel.
	Publish(ctx context.Context, channel string, v any) error

	// Subscribe đăng ký nhận thông điệp từ các channel.
	//
	// Chỗ gọi phải Close *redis.PubSub khi xong. Trả type của go-redis chứ
	// không bọc lại: Channel() của nó đã là một <-chan có buffer và có xử lý
	// reconnect, và bọc lại chỉ làm mất những thứ đó.
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// Stream là các thao tác trên Redis Stream.
//
// Khác Pub/Sub ở chỗ thông điệp được lưu lại và có ack theo consumer group, nên
// một consumer chết đi rồi sống lại vẫn đọc tiếp được từ chỗ dừng.
type Stream interface {
	// XAdd thêm một entry vào stream, trả về ID do Redis sinh.
	//
	// maxLen > 0 thì cắt stream về khoảng maxLen entry (cắt xấp xỉ, dùng `~`
	// của Redis — rẻ hơn nhiều so với cắt chính xác). Stream không cắt sẽ lớn
	// mãi, đó là cách hết bộ nhớ Redis phổ biến nhất.
	XAdd(ctx context.Context, stream string, values map[string]any, maxLen int64) (string, error)

	// XCreateGroup tạo consumer group. Group đã tồn tại không phải lỗi.
	//
	// start là ID bắt đầu đọc: "0" để đọc từ đầu stream, "$" để chỉ đọc entry
	// mới.
	XCreateGroup(ctx context.Context, stream, group, start string) error

	// XReadGroup đọc entry chưa xử lý cho một consumer trong group.
	//
	// block là thời gian chờ khi stream rỗng; 0 nghĩa là trả về ngay. Trả
	// [ErrMiss] khi hết thời gian chờ mà không có entry nào.
	XReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]redis.XMessage, error)

	// XAck báo đã xử lý xong các entry.
	XAck(ctx context.Context, stream, group string, ids ...string) error
}

// Pipeline gom nhiều lệnh vào một lần round-trip.
//
// Interface này cố tình để lộ redis.Pipeliner của go-redis. Bọc lại toàn bộ bề
// mặt Pipeliner chính là cái sai mà package này tránh — một interface ba mươi
// method không ai mock nổi. Chỗ nào cần pipeline là chỗ đã chấp nhận nói chuyện
// trực tiếp với Redis; interface tồn tại chỉ để khai phụ thuộc cho gọn.
type Pipeline interface {
	// Pipelined gửi các lệnh trong fn thành một lần round-trip.
	//
	// Các lệnh **không** nguyên tử với nhau: lệnh khác từ client khác chen vào
	// giữa được. Cần nguyên tử thì dùng TxPipelined hoặc một Lua script.
	Pipelined(ctx context.Context, fn func(p redis.Pipeliner) error) ([]redis.Cmder, error)

	// TxPipelined gửi các lệnh trong một MULTI/EXEC.
	//
	// Ở chế độ cluster, mọi key trong transaction phải cùng một hash slot —
	// dùng hash tag `{...}` để đảm bảo điều đó.
	TxPipelined(ctx context.Context, fn func(p redis.Pipeliner) error) ([]redis.Cmder, error)
}

// Loader là [KV] kèm nhóm chống stampede, dùng cho [GetOrLoad].
type Loader interface {
	KV

	// Flight trả về nhóm gom các lần load trùng key.
	Flight() *Flight
}

// Kiểm tra lúc compile: *Client cài đặt mọi interface của package.
var (
	_ KV       = (*Client)(nil)
	_ Hash     = (*Client)(nil)
	_ PubSub   = (*Client)(nil)
	_ Stream   = (*Client)(nil)
	_ Pipeline = (*Client)(nil)
	_ Loader   = (*Client)(nil)
)
