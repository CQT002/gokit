package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Decode giải mã một giá trị thô đọc từ Redis vào dst.
//
// Cần khi dùng [Hash.HGetAll], [Hash.HMGet] hoặc [Client.Redis] trực tiếp: các
// đường đó trả bytes thô, và hàm này áp đúng quy tắc mã hoá của package (string
// và []byte thô, còn lại JSON) nên giá trị đọc ra khớp với giá trị đã ghi.
func Decode(raw []byte, dst any) error { return decode(raw, dst) }

// Get cài đặt [KV].
func (c *Client) Get(ctx context.Context, key string, dst any) error {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return wrap("get "+key, err)
	}
	return decode(raw, dst)
}

// Set cài đặt [KV].
func (c *Client) Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	raw, err := encode(v)
	if err != nil {
		return err
	}
	// ttl <= 0 truyền xuống thành 0, đúng nghĩa "không hết hạn" của go-redis.
	return wrap("set "+key, c.rdb.Set(ctx, key, raw, max(ttl, 0)).Err())
}

// SetNX cài đặt [KV].
func (c *Client) SetNX(ctx context.Context, key string, v any, ttl time.Duration) (bool, error) {
	raw, err := encode(v)
	if err != nil {
		return false, err
	}
	ok, err := c.rdb.SetNX(ctx, key, raw, max(ttl, 0)).Result()
	if err != nil {
		return false, wrap("setnx "+key, err)
	}
	return ok, nil
}

// Del cài đặt [KV].
func (c *Client) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return wrap("del", c.rdb.Del(ctx, keys...).Err())
}

// Exists cài đặt [KV].
func (c *Client) Exists(ctx context.Context, keys ...string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	n, err := c.rdb.Exists(ctx, keys...).Result()
	if err != nil {
		return 0, wrap("exists", err)
	}
	return n, nil
}

// TTL cài đặt [KV].
//
// Redis nhồi hai tình huống khác nhau vào hai giá trị âm của cùng một lệnh: -2
// là key không tồn tại, -1 là key tồn tại nhưng không có hạn. Chỗ này dịch
// chúng ra: -2 thành [ErrMiss], -1 thành 0. Trả nguyên giá trị âm ra ngoài là
// cách chắc chắn có người đem nó đi cộng vào một mốc thời gian.
func (c *Client) TTL(ctx context.Context, key string) (time.Duration, error) {
	d, err := c.rdb.TTL(ctx, key).Result()
	if err != nil {
		return 0, wrap("ttl "+key, err)
	}
	switch d {
	case -2:
		return 0, ErrMiss
	case -1:
		return 0, nil
	}
	return d, nil
}

// Expire cài đặt [KV]. Trả [ErrMiss] nếu key không tồn tại.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	ok, err := c.rdb.Expire(ctx, key, ttl).Result()
	if err != nil {
		return wrap("expire "+key, err)
	}
	if !ok {
		return ErrMiss
	}
	return nil
}

// Incr cài đặt [KV].
func (c *Client) Incr(ctx context.Context, key string, by int64) (int64, error) {
	n, err := c.rdb.IncrBy(ctx, key, by).Result()
	if err != nil {
		return 0, wrap("incr "+key, err)
	}
	return n, nil
}

// MGet cài đặt [KV].
//
// Key không tồn tại **không** làm hàm trả [ErrMiss]: một lần MGet mười key mà
// thiếu một key là chuyện bình thường, và trả lỗi thì mất luôn chín key kia.
// Phần tử tương ứng giữ giá trị zero.
func (c *Client) MGet(ctx context.Context, keys []string, dst any) error {
	if len(keys) == 0 {
		return decodeInto(nil, dst)
	}

	vals, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return wrap("mget", err)
	}

	raws := make([][]byte, len(vals))
	for i, v := range vals {
		switch x := v.(type) {
		case nil:
			// key không tồn tại
		case string:
			raws[i] = []byte(x)
		case []byte:
			raws[i] = x
		default:
			return fmt.Errorf("cache: mget trả về kiểu %T không xử lý được", v)
		}
	}
	return decodeInto(raws, dst)
}

// Scan cài đặt [KV].
//
// **Chỉ dùng được ở chế độ standalone.** Ở cluster, SCAN không có key nên
// go-redis gửi nó tới một node bất kỳ, và kết quả là danh sách key của đúng
// node đó — thiếu phần còn lại mà không có lỗi nào. Hàm này trả lỗi thay vì
// trả kết quả thiếu: một danh sách thiếu âm thầm tệ hơn nhiều so với một lỗi.
//
// Cần quét trên cluster thì đi qua [Client.Redis] và ForEachMaster, hoặc tốt
// hơn là tự giữ danh sách key trong một SET — KEYS/SCAN trên tập key lớn là
// thao tác O(N) mà production không nên phụ thuộc vào.
func (c *Client) Scan(ctx context.Context, pattern string, cursor uint64, count int64) ([]string, uint64, error) {
	if _, ok := c.rdb.(*redis.ClusterClient); ok {
		return nil, 0, errors.New(
			"cache: Scan không dùng được ở chế độ cluster — dùng Redis().(*redis.ClusterClient).ForEachMaster")
	}

	keys, next, err := c.rdb.Scan(ctx, cursor, pattern, count).Result()
	if err != nil {
		return nil, 0, wrap("scan", err)
	}
	return keys, next, nil
}
