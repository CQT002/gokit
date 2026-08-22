//go:build integration

package testx

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// nilContainer là một *T nil cài đặt terminator — đúng thứ mà Run trả về khi
// dựng container thất bại.
type nilContainer struct{}

func (n *nilContainer) Terminate(context.Context, ...testcontainers.TerminateOption) error {
	// Gọi được nghĩa là isNil đã bỏ sót: con trỏ nil thật sẽ panic trước khi tới đây.
	panic("Terminate được gọi trên container nil")
}

// Đây là cái bẫy kinh điển của Go: một con trỏ nil nhét vào interface thì
// interface đó **khác** nil. Bỏ sót nó làm lỗi thật ("Docker chưa chạy") bị che
// mất sau một panic ở bước dọn dẹp.
func TestIsNil_ConTroNilTrongInterface(t *testing.T) {
	var typed *nilContainer

	if !isNil(typed) {
		t.Error("isNil bỏ sót con trỏ nil bọc trong interface")
	}
	if !isNil(nil) {
		t.Error("isNil(nil) = false")
	}
	if isNil(&nilContainer{}) {
		t.Error("isNil báo nhầm cho con trỏ hợp lệ")
	}
}

// registerTerminate nhận container nil mà không đăng ký gì, nên bước dọn dẹp
// không panic.
func TestRegisterTerminate_ContainerNil(t *testing.T) {
	var typed *nilContainer
	registerTerminate(t, typed)
}
