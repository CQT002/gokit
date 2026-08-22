package cache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// GetOrLoad đọc key từ cache, và khi miss thì gọi load rồi ghi kết quả vào cache.
//
// Đây là mẫu cache-aside, cộng thêm chống cache stampede: một trăm request cùng
// miss một key thì chỉ **một** request xuống database, chín mươi chín request
// còn lại chờ và dùng lại kết quả đó. Không có phần chống stampede, một key nóng
// hết hạn là một trăm câu query giống nhau đập vào database cùng lúc — và đó
// thường là nguyên nhân thật của những vụ sập vào giờ cao điểm.
//
// Hai loại lỗi được cố tình **không** trả ra ngoài, chỉ ghi log:
//
//   - Redis lỗi lúc đọc → vẫn gọi load. Cache hỏng không được biến thành API
//     hỏng.
//   - Redis lỗi lúc ghi → vẫn trả dữ liệu. Nó đã lấy được rồi; hệ quả duy nhất
//     là lần sau phải load lại.
//
// Giá trị zero của T được cache như mọi giá trị khác. Muốn cache cả "không tìm
// thấy" (chống stampede cho key không tồn tại) thì cho T là kiểu con trỏ hoặc
// một struct có cờ, và cho load trả giá trị đó kèm err = nil.
//
//	user, err := cache.GetOrLoad(ctx, c, "user:"+id, 5*time.Minute,
//	    func(ctx context.Context) (User, error) { return repo.Find(ctx, id) })
func GetOrLoad[T any](
	ctx context.Context,
	c Loader,
	key string,
	ttl time.Duration,
	load func(context.Context) (T, error),
) (T, error) {
	var zero T
	if load == nil {
		return zero, errors.New("cache: GetOrLoad cần load")
	}

	var out T
	switch err := c.Get(ctx, key, &out); {
	case err == nil:
		return out, nil
	case !errors.Is(err, ErrMiss):
		logWarn(ctx, c, "không đọc được cache, gọi thẳng nguồn dữ liệu",
			slog.String("key", key), slog.String("error", err.Error()))
	}

	// Khoá của singleflight là key của cache, nên chỉ hai lần load **cùng key**
	// bị gom. Nhóm nằm trên Loader chứ không phải biến package: hai Client trỏ
	// tới hai Redis khác nhau thì cùng một tên key vẫn là hai thứ khác nhau.
	v, err, _ := c.Flight().g.Do(key, func() (any, error) {
		// Đọc lại một lần nữa. Request chờ ở Do có thể vừa vào ngay sau khi lần
		// load trước rời ra — lúc đó cache đã có giá trị, và một lần GET rẻ hơn
		// nhiều so với một lần xuống database.
		var again T
		if err := c.Get(ctx, key, &again); err == nil {
			return again, nil
		}

		loaded, err := load(ctx)
		if err != nil {
			return nil, err
		}

		if err := c.Set(ctx, key, loaded, ttl); err != nil {
			logWarn(ctx, c, "không ghi được kết quả vào cache",
				slog.String("key", key), slog.String("error", err.Error()))
		}
		return loaded, nil
	})
	if err != nil {
		return zero, err
	}

	loaded, ok := v.(T)
	if !ok {
		// Chỉ xảy ra khi hai chỗ gọi dùng cùng một key với hai T khác nhau — lỗi
		// lập trình, và im lặng trả giá trị zero sẽ rất khó tìm.
		return zero, fmt.Errorf("cache: key %q đang được load với kiểu khác (%T)", key, v)
	}
	return loaded, nil
}

// logWarn ghi cảnh báo qua logger của c, nếu c có một logger.
//
// Dùng interface tuỳ chọn thay vì thêm Logger() vào [Loader]: một mock chỉ cần
// KV và Flight vẫn phải dùng được với GetOrLoad, còn logger là chi tiết của
// implementation thật.
func logWarn(ctx context.Context, c any, msg string, attrs ...slog.Attr) {
	log := slog.Default()
	if src, ok := c.(interface{ Logger() *slog.Logger }); ok {
		if l := src.Logger(); l != nil {
			log = l
		}
	}
	log.LogAttrs(ctx, slog.LevelWarn, msg, attrs...)
}
