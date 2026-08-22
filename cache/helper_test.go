package cache_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cqt002/gokit/cache"
)

// newClient dựng Client trên một Redis giả chạy trong process.
//
// miniredis là server Redis thuần Go: nó nói đúng protocol, có TTL thật, và có
// cả EVAL. Nhờ vậy test đơn vị của package này kiểm tra được hành vi thật —
// SET NX, TTL âm, script Lua — mà không cần Docker. Phần chỉ có ở Redis thật
// (cluster, failover) để dành cho test integration.
func newClient(t *testing.T) (*cache.Client, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	c, err := cache.New(cache.Config{Addrs: []string{mr.Addr()}, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

// newClientWithLog như newClient nhưng ghi log vào buffer để test đọc lại được.
func newClientWithLog(t *testing.T) (*cache.Client, *miniredis.Miniredis, *bytes.Buffer) {
	t.Helper()

	mr := miniredis.RunT(t)
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := cache.New(cache.Config{Addrs: []string{mr.Addr()}, Logger: log})
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, mr, &buf
}

// discardLogger trả về logger bỏ mọi dòng log.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// rawValue đọc giá trị thô trong Redis giả, để kiểm tra dạng đã mã hoá.
func rawValue(t *testing.T, mr *miniredis.Miniredis, key string) string {
	t.Helper()
	v, err := mr.Get(key)
	if err != nil {
		t.Fatalf("miniredis.Get(%q): %v", key, err)
	}
	return v
}

// logLines tách buffer log thành từng dòng JSON đã giải mã.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("dòng log không phải JSON: %v — %s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// hasLog cho biết trong log có dòng nào ở level và chứa msg.
func hasLog(t *testing.T, buf *bytes.Buffer, level, msg string) bool {
	t.Helper()
	for _, l := range logLines(t, buf) {
		if l["level"] == level && strings.Contains(l["msg"].(string), msg) {
			return true
		}
	}
	return false
}

// user là kiểu dùng để test mã hoá struct.
type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// newRedis dựng client go-redis thô trên một Redis giả, cho các test cần đi
// thẳng xuống dưới.
func newRedis(t *testing.T) (redis.UniversalClient, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewUniversalClient(&redis.UniversalOptions{Addrs: []string{mr.Addr()}})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// typeName trả về tên kiểu của client go-redis bên dưới.
func typeName(c *cache.Client) string {
	return fmt.Sprintf("%T", c.Redis())
}

// contains là strings.Contains, đặt tên ngắn cho vòng lặp kiểm tra log.
func contains(s, sub string) bool { return strings.Contains(s, sub) }
