package timex_test

import (
	"testing"
	"time"

	"github.com/cqt002/gokit/core/timex"
)

func mustLoadHCM(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Skipf("không có tzdata cho Asia/Ho_Chi_Minh: %v", err)
	}
	return loc
}

func TestLayouts(t *testing.T) {
	ts := time.Date(2026, 8, 21, 14, 5, 9, 40_000_000, time.UTC)
	tests := []struct {
		layout string
		want   string
	}{
		{timex.DateISO, "2026-08-21"},
		{timex.DateCompact, "20260821"},
		{timex.RFC3339Milli, "2026-08-21T14:05:09.040Z"},
	}
	for _, tt := range tests {
		if got := ts.Format(tt.layout); got != tt.want {
			t.Errorf("Format(%q) = %q, muốn %q", tt.layout, got, tt.want)
		}
	}
}

// RFC3339Milli phải giữ đủ 3 chữ số cả khi phần nghìn giây bằng 0 — đó là điểm
// khác biệt so với time.RFC3339Nano, và là điều kiện để so sánh chuỗi ra đúng
// thứ tự thời gian.
func TestRFC3339Milli_LuonDuBaChuSo(t *testing.T) {
	tests := []struct {
		nanos int
		want  string
	}{
		{0, "2026-08-21T00:00:00.000Z"},
		{1_000_000, "2026-08-21T00:00:00.001Z"},
		{999_000_000, "2026-08-21T00:00:00.999Z"},
	}
	var prev string
	for _, tt := range tests {
		got := time.Date(2026, 8, 21, 0, 0, 0, tt.nanos, time.UTC).Format(timex.RFC3339Milli)
		if got != tt.want {
			t.Errorf("nanos=%d: %q, muốn %q", tt.nanos, got, tt.want)
		}
		if prev != "" && prev >= got {
			t.Errorf("so sánh chuỗi ra sai thứ tự: %q không nhỏ hơn %q", prev, got)
		}
		prev = got
	}
}

func TestStartOfDay(t *testing.T) {
	loc := mustLoadHCM(t)
	ts := time.Date(2026, 8, 21, 23, 59, 59, 999, loc)

	got := timex.StartOfDay(ts)
	want := time.Date(2026, 8, 21, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("StartOfDay = %v, muốn %v", got, want)
	}
	if got.Location() != loc {
		t.Errorf("Location = %v, phải giữ nguyên %v", got.Location(), loc)
	}
}

func TestEndOfDay(t *testing.T) {
	loc := mustLoadHCM(t)
	ts := time.Date(2026, 8, 21, 0, 0, 0, 0, loc)

	got := timex.EndOfDay(ts)
	want := time.Date(2026, 8, 21, 23, 59, 59, 999_999_999, loc)
	if !got.Equal(want) {
		t.Errorf("EndOfDay = %v, muốn %v", got, want)
	}

	// Không được chạm sang ngày hôm sau: `t <= EndOfDay(x)` phải loại được
	// 00:00:00 của ngày kế tiếp.
	nextDay := time.Date(2026, 8, 22, 0, 0, 0, 0, loc)
	if !got.Before(nextDay) {
		t.Errorf("EndOfDay = %v, đã chạm sang %v", got, nextDay)
	}
}

// Giữ location là cả lý do tồn tại của hai hàm này: với giờ sát nửa đêm, đổi sang
// UTC làm lệch hẳn một ngày.
func TestStartEndOfDay_GioSatNuaDem(t *testing.T) {
	loc := mustLoadHCM(t)
	// 00:30 giờ Việt Nam ngày 21 là 17:30 UTC ngày 20.
	ts := time.Date(2026, 8, 21, 0, 30, 0, 0, loc)

	if d := timex.StartOfDay(ts).Day(); d != 21 {
		t.Errorf("StartOfDay ra ngày %d, muốn 21 — đã bị đổi múi giờ", d)
	}
	if d := timex.EndOfDay(ts).Day(); d != 21 {
		t.Errorf("EndOfDay ra ngày %d, muốn 21", d)
	}
	if ts.UTC().Day() != 20 {
		t.Fatal("tiền đề của test sai: mốc này phải là ngày 20 theo UTC")
	}
}

func TestStartEndOfDay_BatBien(t *testing.T) {
	loc := mustLoadHCM(t)
	for _, ts := range []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, loc),
		time.Date(2026, 8, 21, 12, 0, 0, 0, loc),
		time.Date(2026, 12, 31, 23, 59, 59, 999_999_999, loc),
		time.Date(2024, 2, 29, 8, 0, 0, 0, loc), // năm nhuận
	} {
		start, end := timex.StartOfDay(ts), timex.EndOfDay(ts)
		if !start.Before(end) {
			t.Errorf("%v: start %v không trước end %v", ts, start, end)
		}
		if ts.Before(start) || ts.After(end) {
			t.Errorf("%v không nằm trong [%v, %v]", ts, start, end)
		}
		if start.Day() != ts.Day() || end.Day() != ts.Day() {
			t.Errorf("%v: start/end lệch ngày (%d, %d)", ts, start.Day(), end.Day())
		}
	}
}

// Đây là cái bẫy hàm này tồn tại để chặn: time.Parse coi chuỗi không có múi giờ
// là UTC, nên so sánh với giờ Việt Nam sẽ lệch 7 tiếng.
func TestParseInLoc(t *testing.T) {
	loc := mustLoadHCM(t)

	got, err := timex.ParseInLoc(timex.DateISO, "2026-08-21", loc)
	if err != nil {
		t.Fatalf("ParseInLoc: %v", err)
	}
	want := time.Date(2026, 8, 21, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("ParseInLoc = %v, muốn %v", got, want)
	}

	viaStdlib, err := time.Parse(timex.DateISO, "2026-08-21")
	if err != nil {
		t.Fatalf("time.Parse: %v", err)
	}
	if got.Equal(viaStdlib) {
		t.Error("kết quả trùng time.Parse — hàm này không còn tác dụng gì")
	}
	if diff := viaStdlib.Sub(got); diff != 7*time.Hour {
		t.Errorf("lệch %v, muốn 7 giờ", diff)
	}
}

func TestParseInLoc_LocNil(t *testing.T) {
	got, err := timex.ParseInLoc(timex.DateISO, "2026-08-21", nil)
	if err != nil {
		t.Fatalf("ParseInLoc: %v", err)
	}
	if got.Location() != time.UTC {
		t.Errorf("Location = %v, loc nil phải thành UTC", got.Location())
	}
}

func TestParseInLoc_ChuoiSai(t *testing.T) {
	for _, value := range []string{"", "21-08-2026", "2026-13-45", "rác"} {
		if _, err := timex.ParseInLoc(timex.DateISO, value, time.UTC); err == nil {
			t.Errorf("ParseInLoc(%q) không báo lỗi", value)
		}
	}
}
