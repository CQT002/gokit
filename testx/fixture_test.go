package testx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cqt002/gokit/testx"
)

type order struct {
	ID     string `json:"id"     yaml:"id"`
	Amount int    `json:"amount" yaml:"amount"`
}

// writeFile ghi một file trong thư mục tạm và trả về đường dẫn.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLoadFixture_JSON(t *testing.T) {
	path := writeFile(t, "orders.json", `[{"id":"od-1","amount":1000},{"id":"od-2","amount":2000}]`)

	got := testx.LoadFixture[[]order](t, path)
	if len(got) != 2 {
		t.Fatalf("len = %d, muốn 2", len(got))
	}
	if got[0].ID != "od-1" || got[1].Amount != 2000 {
		t.Errorf("got = %+v", got)
	}
}

func TestLoadFixture_YAML(t *testing.T) {
	path := writeFile(t, "orders.yaml", "- id: od-1\n  amount: 1000\n")

	got := testx.LoadFixture[[]order](t, path)
	if len(got) != 1 || got[0].ID != "od-1" || got[0].Amount != 1000 {
		t.Errorf("got = %+v", got)
	}
}

func TestLoadFixture_YML(t *testing.T) {
	path := writeFile(t, "one.yml", "id: od-9\namount: 42\n")

	got := testx.LoadFixture[order](t, path)
	if got.ID != "od-9" || got.Amount != 42 {
		t.Errorf("got = %+v", got)
	}
}

// Một field gõ sai trong fixture sẽ thành giá trị zero trong struct, và test vẫn
// xanh — đúng loại lỗi tệ nhất. Nên nó phải là Fatal.
func TestLoadFixture_FieldLa(t *testing.T) {
	for name, content := range map[string]string{
		"amout.json": `{"id":"od-1","amout":1000}`,
		"amout.yaml": "id: od-1\namout: 1000\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, name, content)

			fake := &fakeTB{}
			fake.run(func() { testx.LoadFixture[order](fake, path) })

			if fake.fatal == "" {
				t.Error("field gõ sai mà không Fatal")
			}
		})
	}
}

func TestLoadFixture_FileKhongCo(t *testing.T) {
	fake := &fakeTB{}
	fake.run(func() { testx.LoadFixture[order](fake, "khong-ton-tai.json") })

	if fake.fatal == "" {
		t.Fatal("file không tồn tại mà không Fatal")
	}
	if !strings.Contains(fake.fatal, "khong-ton-tai.json") {
		t.Errorf("thông báo không nói file nào: %s", fake.fatal)
	}
}

func TestLoadFixture_CuPhapSai(t *testing.T) {
	path := writeFile(t, "bad.json", `{"id": `)

	fake := &fakeTB{}
	fake.run(func() { testx.LoadFixture[order](fake, path) })

	if fake.fatal == "" {
		t.Fatal("JSON sai cú pháp mà không Fatal")
	}
}

func TestLoadFixture_PhanMoRongLa(t *testing.T) {
	path := writeFile(t, "data.toml", "id = 'od-1'")

	fake := &fakeTB{}
	fake.run(func() { testx.LoadFixture[order](fake, path) })

	if fake.fatal == "" {
		t.Fatal("phần mở rộng không hỗ trợ mà không Fatal")
	}
	if !strings.Contains(fake.fatal, ".toml") {
		t.Errorf("thông báo không nói phần mở rộng: %s", fake.fatal)
	}
}

func TestLoadFixture_KieuCoBan(t *testing.T) {
	path := writeFile(t, "ids.json", `["a","b","c"]`)

	got := testx.LoadFixture[[]string](t, path)
	if len(got) != 3 || got[2] != "c" {
		t.Errorf("got = %v", got)
	}
}
