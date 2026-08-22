package cache

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Quy tắc mã hoá của package này, đúng một câu: **string và []byte lưu thô,
// mọi thứ khác lưu JSON.**
//
// Vì sao string và []byte đi thô: giá trị đếm (INCR), cờ, và dữ liệu do service
// khác hoặc ngôn ngữ khác ghi vào đều là chuỗi thuần. Bọc chúng trong JSON làm
// `redis-cli get` trả về `"abc"` kèm dấu nháy và làm INCR không dùng được.
//
// Vì sao phần còn lại đi JSON: nó round-trip đúng cho struct, map, slice, bool,
// số và time.Time. Cách của go-redis — để driver tự chọn — thì bool thành "1"
// và đọc lại vào *bool là lỗi.

// encode đổi giá trị Go thành bytes để lưu vào Redis.
func encode(v any) ([]byte, error) {
	switch x := v.(type) {
	case []byte:
		return x, nil
	case string:
		return []byte(x), nil
	case json.RawMessage:
		return x, nil
	}

	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("cache: mã hoá giá trị %T: %w", v, err)
	}
	return b, nil
}

// decode đọc bytes từ Redis vào dst.
//
// dst phải là con trỏ. Con trỏ nil hoặc giá trị không phải con trỏ là lỗi lập
// trình, và báo ngay ở đây rẻ hơn nhiều so với để json trả một thông báo khó
// hiểu.
func decode(raw []byte, dst any) error {
	switch d := dst.(type) {
	case nil:
		return errors.New("cache: dst không được nil")
	case *[]byte:
		// Clone: raw là buffer do go-redis cấp, chỗ gọi giữ nó lâu hơn được.
		*d = bytes.Clone(raw)
		return nil
	case *string:
		*d = string(raw)
		return nil
	case *json.RawMessage:
		*d = bytes.Clone(raw)
		return nil
	}

	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer {
		return fmt.Errorf("cache: dst phải là con trỏ, nhận %T", dst)
	}
	if rv.IsNil() {
		return fmt.Errorf("cache: dst là con trỏ nil (%T)", dst)
	}

	if err := json.Unmarshal(raw, dst); err != nil {
		// Không đưa nội dung raw vào thông báo lỗi: đó là dữ liệu đang nằm
		// trong cache, và thông báo lỗi thường đi thẳng vào log.
		return fmt.Errorf("cache: giải mã giá trị vào %T: %w", dst, err)
	}
	return nil
}

// decodeInto đọc nhiều giá trị vào một slice, mỗi phần tử một giá trị.
//
// dst phải là con trỏ tới slice. Slice được đặt lại đúng len(raws) phần tử, và
// phần tử ứng với key không tồn tại (raw == nil) giữ giá trị zero.
//
// Giữ đúng thứ tự và độ dài là điều kiện để chỗ gọi ghép lại được với danh sách
// key ban đầu; nếu bỏ bớt phần tử thiếu thì không còn cách nào biết key nào đã
// miss.
func decodeInto(raws [][]byte, dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("cache: dst phải là con trỏ tới slice, nhận %T", dst)
	}
	slice := rv.Elem()
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("cache: dst phải là con trỏ tới slice, nhận %T", dst)
	}

	elemType := slice.Type().Elem()
	out := reflect.MakeSlice(slice.Type(), len(raws), len(raws))

	for i, raw := range raws {
		if raw == nil {
			continue // key không tồn tại: để nguyên giá trị zero
		}
		elem := reflect.New(elemType)
		if err := decode(raw, elem.Interface()); err != nil {
			return fmt.Errorf("cache: giải mã phần tử %d: %w", i, err)
		}
		out.Index(i).Set(elem.Elem())
	}

	slice.Set(out)
	return nil
}
