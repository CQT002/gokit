package secret_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cqt002/gokit/core/secret"
)

const plaintext = "s3cr3t-p@ssw0rd"

type dbConfig struct {
	Host     string        `json:"host" yaml:"host"`
	Password secret.Secret `json:"password" yaml:"password"`
}

// Bảng này là hợp đồng chính của package: mọi đường ra text đều phải che.
func TestMoiDuongRaDeuChe(t *testing.T) {
	s := secret.Secret(plaintext)
	tests := []struct {
		name string
		got  string
	}{
		{"String", s.String()},
		{"GoString", s.GoString()},
		{"fmt %v", fmt.Sprintf("%v", s)},
		// Đúng là gọi String() trực tiếp thì gọn hơn, nhưng ở đây mục đích là kiểm
		// tra chính đường đi qua verb %s của fmt.
		{"fmt %s", fmt.Sprintf("%s", s)}, //nolint:staticcheck // cố tình đi qua fmt
		{"fmt %q", fmt.Sprintf("%q", s)},
		{"fmt %#v", fmt.Sprintf("%#v", s)},
		{"fmt %+v struct", fmt.Sprintf("%+v", dbConfig{Host: "db", Password: s})},
		{"fmt %#v struct", fmt.Sprintf("%#v", dbConfig{Host: "db", Password: s})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.got, plaintext) {
				t.Fatalf("giá trị thật bị lộ: %s", tt.got)
			}
			if !strings.Contains(tt.got, secret.Redacted) {
				t.Errorf("không thấy %s trong %q", secret.Redacted, tt.got)
			}
		})
	}
}

func TestMarshalJSON(t *testing.T) {
	b, err := json.Marshal(dbConfig{Host: "db", Password: plaintext})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), plaintext) {
		t.Fatalf("giá trị thật bị lộ: %s", b)
	}
	want := `{"host":"db","password":"[REDACTED]"}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

func TestMarshalText(t *testing.T) {
	b, err := secret.Secret(plaintext).MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(b) != secret.Redacted {
		t.Errorf("got %q, want %q", b, secret.Redacted)
	}
}

func TestMarshalYAML(t *testing.T) {
	b, err := yaml.Marshal(dbConfig{Host: "db", Password: plaintext})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), plaintext) {
		t.Fatalf("giá trị thật bị lộ khi dump YAML: %s", b)
	}
	if !strings.Contains(string(b), secret.Redacted) {
		t.Errorf("không thấy %s trong:\n%s", secret.Redacted, b)
	}
}

// Chiều đọc vào phải giữ giá trị thật, nếu không thì config vô dụng.
func TestUnmarshal_GiuGiaTriThat(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var cfg dbConfig
		if err := yaml.Unmarshal([]byte("host: db\npassword: "+plaintext+"\n"), &cfg); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if cfg.Password.Reveal() != plaintext {
			t.Errorf("Reveal() = %q, muốn %q", cfg.Password.Reveal(), plaintext)
		}
	})
	t.Run("json", func(t *testing.T) {
		var cfg dbConfig
		if err := json.Unmarshal([]byte(`{"host":"db","password":"`+plaintext+`"}`), &cfg); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if cfg.Password.Reveal() != plaintext {
			t.Errorf("Reveal() = %q, muốn %q", cfg.Password.Reveal(), plaintext)
		}
	})
}

func TestSlogChe(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))

	log.Info("cfg", "password", secret.Secret(plaintext))
	log.Info("cfg nested", "db", dbConfig{Host: "db", Password: plaintext})

	out := buf.String()
	if strings.Contains(out, plaintext) {
		t.Fatalf("giá trị thật bị lộ qua slog: %s", out)
	}
	if strings.Count(out, secret.Redacted) != 2 {
		t.Errorf("số lần che = %d, muốn 2:\n%s", strings.Count(out, secret.Redacted), out)
	}
}

func TestReveal(t *testing.T) {
	if got := secret.Secret(plaintext).Reveal(); got != plaintext {
		t.Errorf("Reveal() = %q, muốn %q", got, plaintext)
	}
}

// Bí mật rỗng cũng che, không in ra chuỗi rỗng: nếu rỗng và không rỗng nhìn khác
// nhau trong log thì log tự tiết lộ trạng thái cấu hình.
func TestSecretRong_VanChe(t *testing.T) {
	var s secret.Secret
	if got := s.String(); got != secret.Redacted {
		t.Errorf("String() = %q, muốn %q", got, secret.Redacted)
	}
	if !s.IsZero() {
		t.Error("IsZero() = false, muốn true")
	}
	if secret.Secret(plaintext).IsZero() {
		t.Error("IsZero() = true với bí mật không rỗng")
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b secret.Secret
		want bool
	}{
		{"giống nhau", plaintext, plaintext, true},
		{"khác nhau", plaintext, "khac", false},
		{"lệch một ký tự cuối", "abcdef", "abcdeg", false},
		{"lệch một ký tự đầu", "abcdef", "zbcdef", false},
		{"khác độ dài", "abc", "abcd", false},
		{"cùng rỗng", "", "", true},
		{"một bên rỗng", plaintext, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("Equal() = %v, muốn %v", got, tt.want)
			}
		})
	}
}
