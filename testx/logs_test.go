package testx_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/cqt002/gokit/testx"
)

func TestCaptureLogs_HasVaFind(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	log.Info("đơn hàng đã tạo", slog.String("order_id", "od-1"))
	log.Warn("gần chạm giới hạn tốc độ", slog.Int("remaining", 3))

	if !logs.Has(slog.LevelInfo, "đơn hàng đã tạo") {
		t.Errorf("Has không tìm được dòng info:\n%s", logs)
	}
	// So khớp theo chứa, không phải bằng nhau.
	if !logs.Has(slog.LevelWarn, "giới hạn tốc độ") {
		t.Errorf("Has không khớp theo chuỗi con:\n%s", logs)
	}
	if logs.Has(slog.LevelError, "đơn hàng đã tạo") {
		t.Error("Has khớp sai level")
	}

	line, ok := logs.Find(slog.LevelInfo, "đơn hàng")
	if !ok {
		t.Fatal("Find không tìm được dòng")
	}
	if line["order_id"] != "od-1" {
		t.Errorf("order_id = %v", line["order_id"])
	}
}

// Logger ghi ở mức Debug: test phải thấy được mọi dòng, kể cả dòng mà production
// sẽ bỏ.
func TestCaptureLogs_ThayCaDebug(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	log.Debug("chi tiết nội bộ")
	if !logs.Has(slog.LevelDebug, "chi tiết nội bộ") {
		t.Errorf("không thấy dòng debug:\n%s", logs)
	}
}

func TestCaptureLogs_LenVaCount(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	for range 3 {
		log.Error("payment failed")
	}
	log.Info("xong")

	if got := logs.Len(); got != 4 {
		t.Errorf("Len = %d, muốn 4", got)
	}
	if got := logs.Count(slog.LevelError, "payment failed"); got != 3 {
		t.Errorf("Count = %d, muốn 3", got)
	}
	if got := logs.Count(slog.LevelError, "khong-co"); got != 0 {
		t.Errorf("Count = %d, muốn 0", got)
	}
}

func TestCaptureLogs_Field(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	log.Info("một", slog.String("a", "1"))
	log.Info("hai", slog.Int("n", 42))

	if got := logs.Field(0, "a"); got != "1" {
		t.Errorf("Field(0, a) = %v", got)
	}
	if got := logs.Field(1, "n"); got != float64(42) {
		t.Errorf("Field(1, n) = %v (%T)", got, got)
	}
	if got := logs.Field(1, "msg"); got != "hai" {
		t.Errorf("Field(1, msg) = %v", got)
	}

	// Ngoài phạm vi và key không có đều trả nil, không Fatal: nhiều test kiểm
	// tra chính việc một field không có mặt.
	if got := logs.Field(99, "a"); got != nil {
		t.Errorf("Field ngoài phạm vi = %v", got)
	}
	if got := logs.Field(0, "khong-co"); got != nil {
		t.Errorf("Field key không có = %v", got)
	}
	if got := logs.Field(-1, "a"); got != nil {
		t.Errorf("Field idx âm = %v", got)
	}
}

// Attr trong group truy cập được bằng đường dẫn có dấu chấm — cần thiết vì
// core/log đặt các field của request vào group.
func TestCaptureLogs_FieldTrongGroup(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	log.Info("request", slog.Group("http",
		slog.String("method", "POST"),
		slog.Int("status", 201)))

	if got := logs.Field(0, "http.method"); got != "POST" {
		t.Errorf("Field(0, http.method) = %v", got)
	}
	if got := logs.Field(0, "http.status"); got != float64(201) {
		t.Errorf("Field(0, http.status) = %v", got)
	}
	if got := logs.Field(0, "http.khong-co"); got != nil {
		t.Errorf("key không có trong group = %v", got)
	}
	// Đi xuyên qua một giá trị không phải map.
	if got := logs.Field(0, "msg.gi-do"); got != nil {
		t.Errorf("đường dẫn qua giá trị không phải map = %v", got)
	}
}

func TestCaptureLogs_Reset(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	log.Info("giai đoạn một")
	logs.Reset()
	log.Info("giai đoạn hai")

	if logs.Has(slog.LevelInfo, "giai đoạn một") {
		t.Error("Reset không xoá dòng cũ")
	}
	if !logs.Has(slog.LevelInfo, "giai đoạn hai") {
		t.Error("dòng sau Reset không được ghi")
	}
	if got := logs.Len(); got != 1 {
		t.Errorf("Len = %d, muốn 1", got)
	}
}

// String là thứ biến một test đỏ khó hiểu thành một test đỏ đọc được.
func TestCaptureLogs_String(t *testing.T) {
	log, logs := testx.CaptureLogs(t)
	log.Info("xin chào")

	if !strings.Contains(logs.String(), "xin chào") {
		t.Errorf("String() = %q", logs.String())
	}
	if logs.Len() == 0 {
		t.Error("String() làm mất dòng log")
	}
}

// Service thật ghi log từ nhiều goroutine. Test này chạy dưới -race.
func TestCaptureLogs_NhieuGoroutine(t *testing.T) {
	log, logs := testx.CaptureLogs(t)

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.InfoContext(context.Background(), "song song", slog.Int("i", i))
		}()
	}
	wg.Wait()

	if got := logs.Len(); got != n {
		t.Errorf("Len = %d, muốn %d", got, n)
	}
}

func TestCaptureLogs_RongThiKhongCoGi(t *testing.T) {
	_, logs := testx.CaptureLogs(t)

	if got := logs.Len(); got != 0 {
		t.Errorf("Len = %d, muốn 0", got)
	}
	if logs.Lines() != nil {
		t.Errorf("Lines() = %v, muốn nil", logs.Lines())
	}
	if logs.Has(slog.LevelInfo, "gì đó") {
		t.Error("Has trên buffer rỗng trả true")
	}
	if got := logs.Field(0, "msg"); got != nil {
		t.Errorf("Field trên buffer rỗng = %v", got)
	}
}
