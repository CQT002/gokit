package cache

import (
	"context"
	"fmt"
)

// HGet cài đặt [Hash].
func (c *Client) HGet(ctx context.Context, key, field string, dst any) error {
	raw, err := c.rdb.HGet(ctx, key, field).Bytes()
	if err != nil {
		return wrap("hget "+key+" "+field, err)
	}
	return decode(raw, dst)
}

// HSet cài đặt [Hash].
func (c *Client) HSet(ctx context.Context, key string, values map[string]any) error {
	if len(values) == 0 {
		return nil
	}

	args := make([]any, 0, len(values)*2)
	for field, v := range values {
		raw, err := encode(v)
		if err != nil {
			return fmt.Errorf("cache: field %q: %w", field, err)
		}
		args = append(args, field, raw)
	}
	return wrap("hset "+key, c.rdb.HSet(ctx, key, args...).Err())
}

// HSetNX cài đặt [Hash].
func (c *Client) HSetNX(ctx context.Context, key, field string, v any) (bool, error) {
	raw, err := encode(v)
	if err != nil {
		return false, err
	}
	ok, err := c.rdb.HSetNX(ctx, key, field, raw).Result()
	if err != nil {
		return false, wrap("hsetnx "+key+" "+field, err)
	}
	return ok, nil
}

// HDel cài đặt [Hash].
func (c *Client) HDel(ctx context.Context, key string, fields ...string) error {
	if len(fields) == 0 {
		return nil
	}
	return wrap("hdel "+key, c.rdb.HDel(ctx, key, fields...).Err())
}

// HGetAll cài đặt [Hash].
//
// Hash không tồn tại trả về map rỗng, **không** trả [ErrMiss]: Redis không phân
// biệt "hash rỗng" với "hash không có", nên một lỗi ở đây sẽ là lỗi bịa ra.
func (c *Client) HGetAll(ctx context.Context, key string) (map[string][]byte, error) {
	m, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, wrap("hgetall "+key, err)
	}

	out := make(map[string][]byte, len(m))
	for field, v := range m {
		out[field] = []byte(v)
	}
	return out, nil
}

// HMGet cài đặt [Hash].
func (c *Client) HMGet(ctx context.Context, key string, fields ...string) ([][]byte, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	vals, err := c.rdb.HMGet(ctx, key, fields...).Result()
	if err != nil {
		return nil, wrap("hmget "+key, err)
	}

	out := make([][]byte, len(vals))
	for i, v := range vals {
		switch x := v.(type) {
		case nil:
			// field không tồn tại
		case string:
			out[i] = []byte(x)
		case []byte:
			out[i] = x
		default:
			return nil, fmt.Errorf("cache: hmget trả về kiểu %T không xử lý được", v)
		}
	}
	return out, nil
}

// HIncrBy cài đặt [Hash].
func (c *Client) HIncrBy(ctx context.Context, key, field string, by int64) (int64, error) {
	n, err := c.rdb.HIncrBy(ctx, key, field, by).Result()
	if err != nil {
		return 0, wrap("hincrby "+key+" "+field, err)
	}
	return n, nil
}
