package testx_test

import (
	"fmt"
	"strings"
	"testing"
)

// fakeTB là testing.TB giả, dùng để test chính các helper của testx.
//
// Cần nó vì thứ phải kiểm ở đây là "helper có báo lỗi đúng lúc không", và không
// có cách nào kiểm điều đó bằng *testing.T thật — một Errorf thật sẽ làm chính
// test này đỏ.
type fakeTB struct {
	testing.TB

	errors []string
	fatal  string
	logs   []string
}

// fatalPanic là sentinel thay cho runtime.Goexit của Fatalf thật.
type fatalPanic struct{}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

// Fatalf ghi lại rồi panic bằng sentinel.
//
// Fatalf thật gọi runtime.Goexit, tức là code sau nó không chạy. Nếu ở đây chỉ
// ghi lại rồi trả về bình thường thì helper sẽ chạy tiếp với dữ liệu không hợp
// lệ, và test sẽ kiểm sai thứ.
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fatal = fmt.Sprintf(format, args...)
	panic(fatalPanic{})
}

func (f *fakeTB) Fatal(args ...any) {
	f.fatal = fmt.Sprint(args...)
	panic(fatalPanic{})
}

// Cleanup bỏ qua: test dùng fakeTB tự dọn bằng t.TempDir của TB thật.
func (f *fakeTB) Cleanup(func()) {}

// run gọi fn và bắt sentinel của Fatalf.
func (f *fakeTB) run(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fatalPanic); ok {
				return
			}
			panic(r)
		}
	}()
	fn()
}

// failed cho biết có Errorf hoặc Fatalf nào đã xảy ra.
func (f *fakeTB) failed() bool { return len(f.errors) > 0 || f.fatal != "" }

// message gộp mọi thông báo, để test tìm chuỗi con.
func (f *fakeTB) message() string {
	return strings.Join(append(append([]string{}, f.errors...), f.fatal), "\n")
}
