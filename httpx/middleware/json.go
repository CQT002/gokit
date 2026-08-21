package middleware

import (
	"bytes"
	"encoding/json"
)

// decodeJSONObject thử parse raw thành map.
//
// Dùng json.Decoder với UseNumber thay vì json.Unmarshal thẳng: số trong JSON sẽ
// giữ nguyên dạng chuỗi gốc thay vì bị chuyển sang float64. Nếu không, một ID
// 19 chữ số trong log sẽ hiện thành 1.2345678901234568e+18 — mất mấy chữ số cuối,
// đúng những chữ số cần để tra cứu.
func decodeJSONObject(raw []byte) (map[string]any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}

	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()

	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, false
	}
	return m, true
}
