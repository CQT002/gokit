package testx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cqt002/gokit/testx"
)

// writeGolden ghi sẵn một file golden trong thư mục làm việc hiện tại.
func writeGolden(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(testx.GoldenDir, name+".golden")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestGolden_Khop(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGolden(t, "out", "xin chào\n")

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "out", []byte("xin chào\n")) })

	if fake.failed() {
		t.Errorf("nội dung khớp mà vẫn báo lỗi: %s", fake.message())
	}
}

func TestGolden_KhacThiBaoLoiKemDiff(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGolden(t, "out", "dòng một\ndòng hai\n")

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "out", []byte("dòng một\ndòng khác\n")) })

	if !fake.failed() {
		t.Fatal("nội dung khác mà không báo lỗi")
	}
	msg := fake.message()
	// Diff phải cho thấy cả hai phía, không chỉ nói "khác nhau".
	if !strings.Contains(msg, "-dòng hai") || !strings.Contains(msg, "+dòng khác") {
		t.Errorf("thông báo không có diff đọc được:\n%s", msg)
	}
	if strings.Contains(msg, "-dòng một") {
		t.Errorf("diff báo cả dòng giống nhau:\n%s", msg)
	}
}

func TestGolden_DiffDaiNganKhacNhau(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGolden(t, "out", "a\nb\n")

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "out", []byte("a\nb\nc\n")) })

	if !fake.failed() {
		t.Fatal("thêm dòng mà không báo lỗi")
	}
	if !strings.Contains(fake.message(), "+c") {
		t.Errorf("diff không báo dòng thêm vào:\n%s", fake.message())
	}

	fake2 := &fakeTB{}
	fake2.run(func() { testx.Golden(fake2, "out", []byte("a\n")) })
	if !strings.Contains(fake2.message(), "-b") {
		t.Errorf("diff không báo dòng bị mất:\n%s", fake2.message())
	}
}

// File chưa có là Fatal kèm hướng dẫn, không phải im lặng tạo mới: tạo mới trong
// im lặng nghĩa là lần chạy đầu luôn xanh dù kết quả có sai.
func TestGolden_ChuaCoFile(t *testing.T) {
	t.Chdir(t.TempDir())

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "out", []byte("gì đó")) })

	if fake.fatal == "" {
		t.Fatal("file chưa có mà không Fatal")
	}
	if !strings.Contains(fake.fatal, testx.UpdateEnv) {
		t.Errorf("thông báo không chỉ ra cách tạo file: %s", fake.fatal)
	}
	if _, err := os.Stat(filepath.Join(testx.GoldenDir, "out.golden")); err == nil {
		t.Error("Golden tự tạo file dù không bật chế độ ghi lại")
	}
}

func TestGolden_GhiLai(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(testx.UpdateEnv, "1")

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "out", []byte("nội dung mới\n")) })

	if fake.failed() {
		t.Fatalf("chế độ ghi lại mà báo lỗi: %s", fake.message())
	}

	got, err := os.ReadFile(filepath.Join(dir, testx.GoldenDir, "out.golden"))
	if err != nil {
		t.Fatalf("đọc file vừa ghi: %v", err)
	}
	if string(got) != "nội dung mới\n" {
		t.Errorf("nội dung = %q", got)
	}
	// Phải nói ra là đã ghi lại: im lặng thì người ta không biết mà xem diff.
	if len(fake.logs) == 0 {
		t.Error("không có log nào về việc đã ghi lại")
	}
}

func TestGolden_GhiLaiDeNoiDungCu(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeGolden(t, "out", "cũ\n")
	t.Setenv(testx.UpdateEnv, "1")

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "out", []byte("mới\n")) })
	if fake.failed() {
		t.Fatalf("%s", fake.message())
	}

	got, err := os.ReadFile(filepath.Join(dir, testx.GoldenDir, "out.golden"))
	if err != nil {
		t.Fatalf("đọc: %v", err)
	}
	if string(got) != "mới\n" {
		t.Errorf("nội dung = %q", got)
	}
}

// Dấu `/` trong name thành thư mục con, khớp với cách Go đặt tên subtest.
func TestGolden_TenCoThuMucCon(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(testx.UpdateEnv, "1")

	fake := &fakeTB{}
	fake.run(func() { testx.Golden(fake, "nhom/truong hop", []byte("x")) })
	if fake.failed() {
		t.Fatalf("%s", fake.message())
	}

	// Khoảng trắng thành gạch dưới, giống cách Go đặt tên subtest.
	want := filepath.Join(dir, testx.GoldenDir, "nhom", "truong_hop.golden")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("không tìm thấy %s: %v", want, err)
	}
}

func TestGoldenClean_BaoFileKhongDung(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGolden(t, "dang-dung", "x")
	writeGolden(t, "bo-quen", "x")

	fake := &fakeTB{}
	fake.run(func() { testx.GoldenClean(fake, "dang-dung") })

	if len(fake.errors) != 1 {
		t.Fatalf("errors = %v, muốn đúng một", fake.errors)
	}
	if !strings.Contains(fake.errors[0], "bo-quen") {
		t.Errorf("báo sai file: %s", fake.errors[0])
	}
}

func TestGoldenClean_KhongCoThuMuc(t *testing.T) {
	t.Chdir(t.TempDir())

	fake := &fakeTB{}
	fake.run(func() { testx.GoldenClean(fake) })

	if fake.failed() {
		t.Errorf("chưa có testdata mà báo lỗi: %s", fake.message())
	}
}

// File không phải .golden trong testdata (fixture, dữ liệu mẫu) không bị báo.
func TestGoldenClean_BoQuaFileKhac(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(testx.GoldenDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testx.GoldenDir, "orders.json"), []byte("[]"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fake := &fakeTB{}
	fake.run(func() { testx.GoldenClean(fake) })

	if fake.failed() {
		t.Errorf("file không phải .golden bị báo: %s", fake.message())
	}
}
