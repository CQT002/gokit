package testx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// UpdateEnv là biến môi trường bật chế độ ghi lại file golden.
//
//	UPDATE_GOLDEN=1 go test ./...
//
// Dùng biến môi trường chứ không phải flag `-update`: một thư viện đăng ký flag
// toàn cục sẽ xung đột với flag cùng tên của chính project dùng nó, và lỗi đó
// hiện ra dưới dạng "flag redefined" lúc chạy test — rất khó lần ra nguồn.
const UpdateEnv = "UPDATE_GOLDEN"

// GoldenDir là thư mục chứa file golden, tính từ package đang test.
const GoldenDir = "testdata"

// Golden so got với nội dung file testdata/<name>.golden.
//
// Dùng cho output dài — JSON response, câu SQL sinh ra, báo cáo — nơi mà viết
// assert tay vừa dài vừa không đọc được, và nơi mà thứ ta thật sự muốn biết là
// "có gì đổi so với lần trước".
//
// Chạy với UPDATE_GOLDEN=1 để ghi lại file. Sau đó **phải đọc diff** trong
// git trước khi commit: cả giá trị của cách làm này nằm ở bước đó. Ghi lại rồi
// commit mà không xem là biến golden test thành một cái dấu cao su.
//
// File golden hết dùng không bị xoá tự động. [GoldenClean] làm việc đó.
func Golden(tb testing.TB, name string, got []byte) {
	tb.Helper()

	path := goldenPath(name)

	if os.Getenv(UpdateEnv) != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			tb.Fatalf("testx: tạo thư mục %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			tb.Fatalf("testx: ghi %s: %v", path, err)
		}
		tb.Logf("testx: đã ghi lại %s (%d byte)", path, len(got))
		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // đường dẫn dẫn xuất từ tên test
	if err != nil {
		if os.IsNotExist(err) {
			tb.Fatalf("testx: chưa có %s — chạy lại với %s=1 để tạo", path, UpdateEnv)
		}
		tb.Fatalf("testx: đọc %s: %v", path, err)
	}

	if bytes.Equal(got, want) {
		return
	}
	tb.Errorf("testx: khác với %s\n%s", path, diff(want, got))
}

// GoldenClean báo lỗi nếu trong testdata có file .golden mà lần chạy này không
// dùng tới.
//
// Gọi từ TestMain sau khi m.Run() xong, hoặc từ một test riêng chạy cuối. File
// golden bị bỏ quên là thứ tích lại theo thời gian và không ai dám xoá vì không
// biết còn dùng hay không.
//
// used là danh sách name đã truyền cho [Golden].
func GoldenClean(tb testing.TB, used ...string) {
	tb.Helper()

	entries, err := os.ReadDir(GoldenDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		tb.Fatalf("testx: đọc %s: %v", GoldenDir, err)
	}

	keep := make(map[string]bool, len(used))
	for _, name := range used {
		keep[filepath.Base(goldenPath(name))] = true
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".golden") {
			continue
		}
		if !keep[e.Name()] {
			tb.Errorf("testx: %s/%s không được test nào dùng — xoá nó đi",
				GoldenDir, e.Name())
		}
	}
}

// goldenPath dựng đường dẫn file golden từ tên.
//
// Dấu `/` trong name thành thư mục con, nên t.Run("a/b") cho ra
// testdata/a/b.golden — khớp với cách Go đặt tên subtest.
func goldenPath(name string) string {
	clean := strings.ReplaceAll(name, " ", "_")
	return filepath.Join(GoldenDir, filepath.Clean(clean)+".golden")
}

// diff dựng một so sánh theo dòng, đọc được trong output của test.
//
// Tự viết thay vì gọi `diff` ngoài hay kéo về một thư viện: nó là hai mươi dòng,
// và một helper test không nên yêu cầu công cụ hệ thống mới chạy được.
func diff(want, got []byte) string {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")

	var b strings.Builder
	b.WriteString("  (- mong đợi, + nhận được)\n")

	for i := range max(len(wantLines), len(gotLines)) {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}

		switch {
		case i >= len(wantLines):
			b.WriteString("  +" + g + "\n")
		case i >= len(gotLines):
			b.WriteString("  -" + w + "\n")
		case w != g:
			b.WriteString("  -" + w + "\n")
			b.WriteString("  +" + g + "\n")
		}
	}
	return b.String()
}
