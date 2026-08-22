package idempotency

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"
)

// MemoryStore là Store lưu trong bộ nhớ của process.
//
// Dùng cho test và cho service chạy **một** instance. Nhiều replica thì mỗi replica
// có bảng riêng, nên hai request trùng vào hai replica khác nhau sẽ đều chạy — dùng
// cache/idemstore (Redis) cho trường hợp đó.
//
// An toàn khi dùng từ nhiều goroutine.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]*memEntry

	// now cho phép test điều khiển thời gian mà không phải sleep.
	now func() time.Time
}

type memEntry struct {
	record   *Record
	reqHash  string
	inFlight bool
	expireAt time.Time
}

// NewMemoryStore tạo store rỗng.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]*memEntry),
		now:     time.Now,
	}
}

// Reserve cài đặt Store.
func (s *MemoryStore) Reserve(_ context.Context, key, reqHash string, ttl time.Duration) (*Record, bool, error) {
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.evictLocked(now)

	if e, ok := s.entries[key]; ok {
		switch {
		case e.inFlight:
			return nil, false, ErrInFlight
		case e.record != nil:
			return e.record, true, nil
		}
		// Có entry nhưng không đang chạy và cũng không có kết quả: trạng thái này
		// chỉ xảy ra nếu Release đã chạy. Coi như chưa có và giành lại quyền xử lý.
	}

	s.entries[key] = &memEntry{
		reqHash:  reqHash,
		inFlight: true,
		expireAt: now.Add(ttl),
	}
	return nil, false, nil
}

// Commit cài đặt Store.
func (s *MemoryStore) Commit(_ context.Context, key string, rec Record, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Copy body: chỗ gọi có thể dùng lại slice đó cho việc khác.
	stored := rec
	stored.Body = bytes.Clone(rec.Body)

	s.entries[key] = &memEntry{
		record:   &stored,
		reqHash:  rec.ReqHash,
		expireAt: s.now().Add(ttl),
	}
	return nil
}

// Release cài đặt Store.
func (s *MemoryStore) Release(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[key]; ok && e.record == nil {
		delete(s.entries, key)
	}
	return nil
}

// Len trả về số entry đang giữ, dùng cho test.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// evictLocked xoá entry đã hết hạn.
//
// Dọn khi có thao tác thay vì chạy goroutine nền: store này dành cho một instance
// và lượng key nhỏ, nên một goroutine chạy mãi là chi phí không cần thiết.
func (s *MemoryStore) evictLocked(now time.Time) {
	for key, e := range s.entries {
		if now.After(e.expireAt) {
			delete(s.entries, key)
		}
	}
}

// replayBody trả lại body đã đọc cho handler, và vẫn đóng được body gốc.
type replayBody struct {
	io.Reader
	closer io.Closer
}

func (b *replayBody) Close() error { return b.closer.Close() }

func newBytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
