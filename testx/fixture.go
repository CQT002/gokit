package testx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// LoadFixture nạp dữ liệu test từ file JSON hoặc YAML.
//
// Định dạng suy ra từ phần mở rộng: .json, .yaml, .yml. Đường dẫn tương đối
// tính từ package đang test — theo quy ước của Go, thường là "testdata/....".
//
//	orders := testx.LoadFixture[[]Order](t, "testdata/orders.json")
//
// Vì sao đáng có helper cho ba dòng code: ba dòng đó luôn bị viết thiếu phần xử
// lý lỗi, và một fixture sai cú pháp lại hiện ra dưới dạng slice rỗng cùng một
// test xanh vô nghĩa. Ở đây file thiếu hoặc sai cú pháp là Fatal.
func LoadFixture[T any](tb testing.TB, path string) T {
	tb.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // đường dẫn do chính test khai
	if err != nil {
		tb.Fatalf("testx: đọc fixture %s: %v", path, err)
	}

	var out T
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		// DisallowUnknownFields: một field gõ sai trong fixture sẽ thành giá trị
		// zero trong struct, và test vẫn xanh — đúng loại lỗi tệ nhất.
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&out); err != nil {
			tb.Fatalf("testx: giải mã JSON %s: %v", path, err)
		}

	case ".yaml", ".yml":
		dec := yaml.NewDecoder(strings.NewReader(string(raw)))
		dec.KnownFields(true)
		if err := dec.Decode(&out); err != nil {
			tb.Fatalf("testx: giải mã YAML %s: %v", path, err)
		}

	default:
		tb.Fatalf("testx: %s có phần mở rộng %q không hỗ trợ (cần .json, .yaml hoặc .yml)", path, ext)
	}

	return out
}
