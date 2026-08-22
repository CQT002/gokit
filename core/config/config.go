// Package config nạp cấu hình từ YAML rồi cho biến môi trường ghi đè lên.
//
// Hai dependency (yaml.v3 và go-envconfig) thay vì mười ba của viper, không có
// state toàn cục, và mọi biến môi trường đều grep được vì tên nó nằm ngay trong
// tag `env:` trên field.
//
// # Thứ tự ưu tiên
//
// Biến môi trường > YAML > giá trị zero của Go. Env chỉ ghi đè khi biến đó thực
// sự tồn tại, nên khai một phần trong YAML và override phần còn lại bằng env là
// cách dùng bình thường.
//
// Thứ tự này không phải mặc định của envconfig: mặc định của nó là không ghi đè
// field đã có giá trị, tức là mọi field khai trong YAML sẽ miễn nhiễm với biến môi
// trường. LoadWith bật DefaultOverwrite để có đúng thứ tự trên.
//
// `default=` trong tag env vẫn dùng được cùng YAML: default chỉ áp khi field còn
// là giá trị zero, nên YAML thắng default. Thứ tự đầy đủ là env > YAML > default
// trong tag > zero.
//
// # Key lạ trong YAML là lỗi
//
// Không phải bị bỏ qua. Một key gõ sai bị im lặng bỏ qua là kiểu sự cố tốn nhiều
// giờ nhất của mọi config loader: cấu hình trông như đã khai mà thực tế đang chạy
// giá trị mặc định.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sethvargo/go-envconfig"
	"gopkg.in/yaml.v3"
)

// Lookuper tra giá trị của một biến môi trường.
//
// Định nghĩa lại ở đây thay vì dùng thẳng type của envconfig để đường dẫn import
// của thư viện đó không lọt vào API công khai của gokit — đổi thư viện env về sau
// sẽ không phải là breaking change với người dùng.
type Lookuper interface {
	Lookup(key string) (value string, found bool)
}

// MapLookuper trả về Lookuper đọc từ một map, dùng cho test.
//
// Nhờ nó mà test cấu hình không phải gọi os.Setenv — thứ tác động lên toàn
// process nên không chạy song song được và hay rò rỉ sang test khác.
func MapLookuper(m map[string]string) Lookuper {
	return envconfig.MapLookuper(m)
}

// Load nạp cấu hình từ nội dung YAML, rồi cho biến môi trường của process ghi đè.
func Load[T any](yamlBytes []byte) (*T, error) {
	return LoadWith[T](yamlBytes, nil)
}

// LoadFile nạp cấu hình từ file YAML, rồi cho biến môi trường của process ghi đè.
func LoadFile[T any](path string) (*T, error) {
	// Đường dẫn do chính app quyết định (cờ dòng lệnh, biến môi trường), không
	// phải từ input của người dùng — và đọc file theo đường dẫn biến chính là
	// việc của hàm này.
	//nolint:gosec // G304: đường dẫn config do app cung cấp
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: đọc file %q: %w", path, err)
	}
	return LoadWith[T](b, nil)
}

// LoadWith như Load nhưng lấy biến môi trường từ lookup.
//
// lookup là nil thì đọc biến môi trường thật của process.
func LoadWith[T any](yamlBytes []byte, lookup Lookuper) (*T, error) {
	cfg := new(T)

	if len(bytes.TrimSpace(yamlBytes)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(yamlBytes))
		// Key lạ là lỗi: xem phần "hai cái bẫy" ở godoc của package.
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config: đọc YAML: %w", err)
		}
	}

	lk := envconfig.OsLookuper()
	if lookup != nil {
		lk = lookup
	}
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   cfg,
		Lookuper: lk,
		// Bắt buộc: mặc định của envconfig là KHÔNG ghi đè field đã có giá trị,
		// nên nếu để nguyên thì mọi field đã khai trong YAML sẽ miễn nhiễm với
		// biến môi trường — tức là mất hẳn thứ tự ưu tiên mà package này hứa.
		DefaultOverwrite: true,
	}); err != nil {
		return nil, fmt.Errorf("config: đọc biến môi trường: %w", err)
	}

	return cfg, nil
}
