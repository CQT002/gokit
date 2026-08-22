package testx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// CaptureLogs trả về logger ghi vào một buffer đọc lại được.
//
// Logger ghi JSON ở mức Debug, tức là thấy mọi dòng. Dùng nó ở chỗ nào service
// nhận *slog.Logger:
//
//	log, logs := testx.CaptureLogs(t)
//	svc := NewService(log)
//	svc.Handle(ctx, req)
//
//	if !logs.Has(slog.LevelWarn, "giới hạn tốc độ") {
//	    t.Errorf("không có cảnh báo:\n%s", logs)
//	}
//
// Vì sao đáng test log: log là hợp đồng với người vận hành. Một alert bám vào
// dòng log "payment failed" sẽ chết im lặng khi ai đó đổi câu chữ, và không có
// test nào bắt được — trừ test này.
func CaptureLogs(tb testing.TB) (*slog.Logger, *LogBuffer) {
	tb.Helper()

	buf := &LogBuffer{tb: tb}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// LogBuffer giữ các dòng log đã ghi.
//
// An toàn khi ghi từ nhiều goroutine: slog gọi Write từ chính goroutine đang ghi
// log, và service thật ghi log từ nhiều goroutine.
type LogBuffer struct {
	tb testing.TB

	mu  sync.Mutex
	raw bytes.Buffer
}

// Write cài đặt io.Writer cho handler của slog.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.raw.Write(p)
}

// Lines trả về các dòng log đã giải mã, theo thứ tự ghi.
func (b *LogBuffer) Lines() []map[string]any {
	b.mu.Lock()
	text := b.raw.String()
	b.mu.Unlock()

	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			// Handler của slog luôn sinh JSON hợp lệ, nên tới đây nghĩa là có
			// ai khác cũng đang ghi vào buffer này — đáng dừng test lại.
			b.tb.Fatalf("testx: dòng log không phải JSON: %v — %s", err, line)
		}
		out = append(out, m)
	}
	return out
}

// Len trả về số dòng log.
func (b *LogBuffer) Len() int { return len(b.Lines()) }

// Reset xoá mọi dòng đã ghi.
//
// Dùng để tách các giai đoạn của một test dài: chạy phần một, Reset, rồi kiểm
// tra log của riêng phần hai.
func (b *LogBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.raw.Reset()
}

// Has cho biết có dòng nào ở đúng level và **chứa** msg không.
//
// So khớp bằng chứa chứ không bằng bằng nhau: một thông báo log thường có thêm
// ngữ cảnh ở hai đầu, và ép test khớp nguyên văn sẽ làm mọi lần sửa câu chữ
// thành một lần sửa test.
func (b *LogBuffer) Has(level slog.Level, msg string) bool {
	_, ok := b.Find(level, msg)
	return ok
}

// Find trả về dòng đầu tiên khớp level và chứa msg.
//
// Dùng khi cần kiểm tra cả các attr của dòng đó:
//
//	line, ok := logs.Find(slog.LevelError, "payment failed")
//	if !ok { t.Fatal(...) }
//	if line["order_id"] != "od-1" { ... }
func (b *LogBuffer) Find(level slog.Level, msg string) (map[string]any, bool) {
	want := level.String()
	for _, line := range b.Lines() {
		if line["level"] != want {
			continue
		}
		if got, ok := line["msg"].(string); ok && strings.Contains(got, msg) {
			return line, true
		}
	}
	return nil, false
}

// Count đếm số dòng khớp level và chứa msg.
func (b *LogBuffer) Count(level slog.Level, msg string) int {
	want := level.String()
	n := 0
	for _, line := range b.Lines() {
		if line["level"] != want {
			continue
		}
		if got, ok := line["msg"].(string); ok && strings.Contains(got, msg) {
			n++
		}
	}
	return n
}

// Field trả về giá trị của attr key ở dòng thứ idx (đếm từ 0).
//
// Attr trong group được truy cập bằng đường dẫn có dấu chấm:
// Field(0, "request.method").
//
// Trả nil nếu idx ngoài phạm vi hoặc key không có. Không Fatal, để test tự
// quyết cái gì là lỗi — nhiều test kiểm tra chính việc một field **không** có
// mặt (ví dụ mật khẩu đã bị mask bỏ hẳn).
func (b *LogBuffer) Field(idx int, key string) any {
	lines := b.Lines()
	if idx < 0 || idx >= len(lines) {
		return nil
	}

	var cur any = lines[idx]
	for part := range strings.SplitSeq(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[part]
		if !ok {
			return nil
		}
	}
	return cur
}

// String trả về toàn bộ log dạng text, để nhét vào thông báo lỗi của test.
//
// Đây là thứ biến một test đỏ khó hiểu thành một test đỏ đọc được: t.Errorf với
// %s của LogBuffer cho thấy service đã ghi gì thật, thay vì chỉ nói "không tìm
// thấy dòng log mong đợi".
func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.raw.String()
}
