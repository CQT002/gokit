// Package idemstore cài đặt idempotency.Store của httpx bằng Redis.
//
// Bản in-memory trong httpx/idempotency chỉ đúng khi service chạy **một**
// instance: mỗi replica có bảng riêng, nên hai lần gửi của cùng một client vào
// hai replica khác nhau đều chạy handler. Store này đưa bảng đó ra Redis, nên
// mọi replica thấy cùng một trạng thái.
//
//	store, err := idemstore.New(idemstore.Config{Redis: c.Redis()})
//	if err != nil { return err }
//
//	mw, err := idempotency.Middleware(idempotency.Config{Store: store})
//
// Vì sao interface nằm ở httpx mà implementation nằm ở đây: chiều phụ thuộc là
// cache → httpx. Đặt ngược lại thì mọi service chỉ cần một HTTP server cũng phải
// kéo về go-redis.
package idemstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/httpx/idempotency"
)

// DefaultPrefix là tiền tố key mặc định.
const DefaultPrefix = "idem:"

// Byte đầu của giá trị lưu trong Redis cho biết trạng thái.
//
// Dùng một byte tiền tố thay vì một field trong JSON để script Lua đọc được
// trạng thái bằng string.sub, không cần cjson — thư viện đó không có ở mọi bản
// Redis và cũng không có ở mọi server giả dùng trong test.
const (
	stateInFlight = 'i'
	stateDone     = 'd'
)

// Config cấu hình Store.
type Config struct {
	// Redis là client Redis. Bắt buộc.
	Redis redis.UniversalClient

	// Prefix là tiền tố cho mọi key. Rỗng → DefaultPrefix.
	//
	// Có tiền tố để key của idempotency không lẫn với key nghiệp vụ — quan
	// trọng khi cần xoá hàng loạt hoặc đọc thống kê theo nhóm key.
	Prefix string
}

// Store lưu kết quả idempotency trong Redis.
//
// Cài đặt idempotency.Store.
type Store struct {
	rdb    redis.UniversalClient
	prefix string
}

// Kiểm tra lúc compile.
var _ idempotency.Store = (*Store)(nil)

// New dựng Store từ cấu hình.
func New(cfg Config) (*Store, error) {
	if cfg.Redis == nil {
		return nil, errors.New("idemstore: Config thiếu Redis")
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = DefaultPrefix
	}
	return &Store{rdb: cfg.Redis, prefix: prefix}, nil
}

// entry là giá trị lưu trong Redis, sau byte trạng thái.
type entry struct {
	ReqHash string            `json:"req_hash"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

// reserveScript đọc giá trị hiện có, và chỉ khi chưa có thì đặt cờ đang xử lý.
//
// Phải là một script chứ không phải GET rồi SET: giữa hai lệnh đó, một request
// khác chen vào được và cả hai đều thấy "chưa có" rồi cùng chạy handler — đúng
// cái mà idempotency tồn tại để ngăn. SET NX một mình cũng không đủ, vì nó chỉ
// cho biết "ghi được hay không" mà không trả về giá trị đang có.
var reserveScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur then
	return cur
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return nil
`)

// releaseScript chỉ xoá key khi nó còn đang ở trạng thái đang xử lý.
//
// Không xoá vô điều kiện: nếu Commit đã chạy thì key đang giữ **kết quả**, và
// xoá nó nghĩa là lần gửi lại tiếp theo sẽ chạy handler lần nữa.
var releaseScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if not cur then
	return 0
end
if string.sub(cur, 1, 1) ~= ARGV[1] then
	return 0
end
redis.call('DEL', KEYS[1])
return 1
`)

// Reserve cài đặt idempotency.Store.
func (s *Store) Reserve(ctx context.Context, key, reqHash string, ttl time.Duration) (*idempotency.Record, bool, error) {
	marker, err := encode(stateInFlight, entry{ReqHash: reqHash})
	if err != nil {
		return nil, false, err
	}

	raw, err := reserveScript.Run(ctx, s.rdb,
		[]string{s.key(key)}, marker, ttl.Milliseconds()).Text()
	if errors.Is(err, redis.Nil) {
		// Script trả nil: chưa có gì, và cờ đang xử lý đã được đặt.
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("idemstore: reserve %q: %w", key, err)
	}

	state, e, err := decode(raw)
	if err != nil {
		return nil, false, fmt.Errorf("idemstore: reserve %q: %w", key, err)
	}
	if state == stateInFlight {
		return nil, false, idempotency.ErrInFlight
	}

	return &idempotency.Record{
		Status:  e.Status,
		Headers: e.Headers,
		Body:    e.Body,
		ReqHash: e.ReqHash,
	}, true, nil
}

// Commit cài đặt idempotency.Store.
func (s *Store) Commit(ctx context.Context, key string, rec idempotency.Record, ttl time.Duration) error {
	value, err := encode(stateDone, entry{
		ReqHash: rec.ReqHash,
		Status:  rec.Status,
		Headers: rec.Headers,
		Body:    rec.Body,
	})
	if err != nil {
		return err
	}

	// SET thẳng, không cần so sánh với cờ đang xử lý: chỉ request đã Reserve
	// thành công mới tới được đây, và TTL được đặt lại từ lúc có kết quả — đó
	// mới là mốc mà 24 giờ giữ kết quả nên tính từ.
	if err := s.rdb.Set(ctx, s.key(key), value, ttl).Err(); err != nil {
		return fmt.Errorf("idemstore: commit %q: %w", key, err)
	}
	return nil
}

// Release cài đặt idempotency.Store.
func (s *Store) Release(ctx context.Context, key string) error {
	err := releaseScript.Run(ctx, s.rdb,
		[]string{s.key(key)}, string(rune(stateInFlight))).Err()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("idemstore: release %q: %w", key, err)
	}
	return nil
}

// key ghép tiền tố vào khoá idempotency.
func (s *Store) key(k string) string { return s.prefix + k }

// encode ghép byte trạng thái với JSON của entry.
func encode(state byte, e entry) (string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("idemstore: mã hoá bản ghi: %w", err)
	}
	return string(state) + string(b), nil
}

// decode tách byte trạng thái và giải mã phần còn lại.
func decode(raw string) (byte, entry, error) {
	if raw == "" {
		return 0, entry{}, errors.New("giá trị rỗng")
	}

	state := raw[0]
	if state != stateInFlight && state != stateDone {
		return 0, entry{}, fmt.Errorf("byte trạng thái không hợp lệ (%q)", state)
	}

	var e entry
	if err := json.Unmarshal([]byte(raw[1:]), &e); err != nil {
		return 0, entry{}, fmt.Errorf("giải mã bản ghi: %w", err)
	}
	return state, e, nil
}
