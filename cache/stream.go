package cache

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// XAdd cài đặt [Stream].
func (c *Client) XAdd(ctx context.Context, stream string, values map[string]any, maxLen int64) (string, error) {
	encoded := make(map[string]any, len(values))
	for field, v := range values {
		raw, err := encode(v)
		if err != nil {
			return "", err
		}
		encoded[field] = raw
	}

	args := &redis.XAddArgs{Stream: stream, Values: encoded}
	if maxLen > 0 {
		args.MaxLen = maxLen
		// Cắt xấp xỉ (`MAXLEN ~`): Redis chỉ bỏ cả một node của radix tree khi
		// node đó đã nằm ngoài ngưỡng. Rẻ hơn nhiều so với cắt chính xác, và
		// "khoảng maxLen entry" là điều duy nhất ai cũng thật sự cần.
		args.Approx = true
	}

	id, err := c.rdb.XAdd(ctx, args).Result()
	if err != nil {
		return "", wrap("xadd "+stream, err)
	}
	return id, nil
}

// XCreateGroup cài đặt [Stream].
//
// Dùng MKSTREAM nên gọi được trước khi có entry nào — không có nó thì thứ tự
// khởi động của producer và consumer trở thành điều kiện để service chạy được.
//
// Group đã tồn tại không phải lỗi: mọi instance của consumer đều gọi hàm này
// lúc khởi động, nên "đã có" là trạng thái bình thường chứ không phải sự cố.
func (c *Client) XCreateGroup(ctx context.Context, stream, group, start string) error {
	if start == "" {
		start = "$"
	}

	err := c.rdb.XGroupCreateMkStream(ctx, stream, group, start).Err()
	if err != nil && strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return wrap("xgroupcreate "+stream+" "+group, err)
}

// XReadGroup cài đặt [Stream].
func (c *Client) XReadGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]redis.XMessage, error) {
	if count <= 0 {
		count = 1
	}
	if block <= 0 {
		// go-redis coi 0 là "chờ vô hạn" còn số âm là "không chờ". Đảo lại cho
		// khớp godoc của interface: 0 nghĩa là trả về ngay.
		block = -1
	}

	streams, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err != nil {
		// redis.Nil ở đây nghĩa là hết thời gian chờ mà không có entry mới, và
		// wrap đổi nó thành ErrMiss — đúng như godoc của interface đã khai.
		return nil, wrap("xreadgroup "+stream, err)
	}

	// Chỉ đọc một stream nên chỉ có tối đa một phần tử.
	if len(streams) == 0 {
		return nil, ErrMiss
	}
	return streams[0].Messages, nil
}

// XAck cài đặt [Stream].
func (c *Client) XAck(ctx context.Context, stream, group string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return wrap("xack "+stream+" "+group, c.rdb.XAck(ctx, stream, group, ids...).Err())
}
