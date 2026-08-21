package log

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"
)

// Nhãn của Elided.Elided. Chỉ để người đọc log dễ hiểu đang bỏ qua cái gì —
// không dùng làm điều kiện kích hoạt việc rút gọn.
const (
	LabelBase64  = "base64"
	LabelDataURI = "data-uri"
	LabelText    = "text"
)

// Elided là metadata thay cho một giá trị đã bị rút gọn vì quá dài.
//
// Đây là điểm khác biệt so với việc cắt chuỗi: 50 ký tự đầu của một ảnh base64
// không nói lên điều gì, còn kích thước cộng 8 hex đầu của sha256 trả lời được cả
// ba câu hỏi hay gặp nhất khi debug — có file hay không, nặng bao nhiêu, có đúng
// file client nói họ gửi không — trong khoảng 60 byte.
type Elided struct {
	// Elided là nhãn loại nội dung: base64, data-uri hoặc text.
	Elided string `json:"_elided"`
	// Bytes là kích thước thật của giá trị gốc.
	Bytes int `json:"bytes"`
	// SHA256 là 8 hex đầu của sha256, rỗng khi MaskConfig.DisableElideHash bật.
	// Đủ để đối chiếu "client gửi trùng file hai lần", không đủ để dò ngược.
	SHA256 string `json:"sha256,omitempty"`
}

// LogValue cài đặt slog.LogValuer, nên Elided ra JSON thành object lồng và ra
// text handler vẫn đọc được, không phải một chuỗi %v.
func (e Elided) LogValue() slog.Value {
	attrs := make([]slog.Attr, 0, 3)
	attrs = append(attrs,
		slog.String("_elided", e.Elided),
		slog.Int("bytes", e.Bytes),
	)
	if e.SHA256 != "" {
		attrs = append(attrs, slog.String("sha256", e.SHA256))
	}
	return slog.GroupValue(attrs...)
}

// dropped là marker thay cho attribute bị bỏ vì dòng log vượt MaxLineBytes.
type dropped struct {
	reason string
	bytes  int
}

// LogValue cài đặt slog.LogValuer.
func (d dropped) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("_dropped", d.reason),
		slog.Int("original_bytes", d.bytes),
	)
}

// elide dựng metadata cho một giá trị quá dài.
func elide(b []byte, cfg MaskConfig) Elided {
	e := Elided{Elided: labelOf(b), Bytes: len(b)}
	if !cfg.DisableElideHash {
		sum := sha256.Sum256(b)
		e.SHA256 = hex.EncodeToString(sum[:4])
	}
	return e
}

// labelOf đoán loại nội dung để đặt nhãn.
//
// Kiểm tra data-uri trước vì nó là dạng cụ thể hơn; thứ tự không ảnh hưởng kết
// quả, một data URI luôn chứa `:` nên không bao giờ khớp bảng chữ base64.
func labelOf(b []byte) string {
	if hasPrefixFold(b, "data:") {
		return LabelDataURI
	}
	if isBase64(b) {
		return LabelBase64
	}
	return LabelText
}

func hasPrefixFold(b []byte, prefix string) bool {
	if len(b) < len(prefix) {
		return false
	}
	return strings.EqualFold(string(b[:len(prefix)]), prefix)
}

// isBase64 khớp `^[A-Za-z0-9+/]+=*$`. Viết tay thay vì dùng regexp để không phải
// quét lại chuỗi nhiều MB bằng máy trạng thái của regexp.
func isBase64(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	i := 0
	for ; i < len(b); i++ {
		c := b[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			continue
		}
		break
	}
	if i == 0 {
		return false // không có ký tự dữ liệu nào trước phần padding
	}
	for ; i < len(b); i++ {
		if b[i] != '=' {
			return false
		}
	}
	return true
}

// applyRule áp một luật đã parse lên giá trị, trả về slog.Value để ghi log.
//
// omit = true nghĩa là chỗ gọi phải bỏ hẳn key này khỏi output.
func applyRule(r parsedRule, v any, cfg MaskConfig) (val slog.Value, omit bool) {
	if r.kind == RuleOmit {
		return slog.Value{}, true
	}
	if r.kind == RuleRedact {
		// Redact không cần đọc giá trị: che là che, bất kể dài ngắn hay kiểu gì.
		return slog.StringValue(redactedText), false
	}

	b := rawBytes(v)
	switch r.kind {
	case RuleElide:
		return slog.AnyValue(elide(b, cfg)), false
	case RuleHash:
		sum := sha256.Sum256(b)
		return slog.StringValue(hex.EncodeToString(sum[:])), false
	case RuleTruncate:
		return slog.StringValue(truncateRunes(string(b), r.n)), false
	case RuleEdges:
		return slog.StringValue(maskEdges(string(b), r.n, r.tail)), false
	default:
		// parseRule không sinh ra nhánh này, nhưng nếu có thì che là hướng đúng.
		return slog.StringValue(redactedText), false
	}
}

// rawBytes lấy byte thô của giá trị để hash hoặc đo kích thước.
//
// []byte dùng trực tiếp — đó là trường hợp payload nhị phân, và đi qua fmt sẽ
// biến nó thành "[72 101 ...]" làm kích thước lẫn hash đều sai.
func rawBytes(v any) []byte {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return t
	case string:
		return []byte(t)
	default:
		return []byte(fmt.Sprint(v))
	}
}

// truncateRunes giữ n rune đầu, thêm dấu … nếu có phần bị bỏ.
//
// Cắt theo rune chứ không theo byte: cắt giữa một ký tự UTF-8 nhiều byte làm
// dòng log chứa byte không hợp lệ, và tiếng Việt thì ký tự nào cũng nhiều byte.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return "…"
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// maskEdges giữ head rune đầu và tail rune cuối, phần giữa thành dấu sao.
//
// Nếu giá trị ngắn tới mức head + tail đã phủ hết thì che toàn bộ: thà mất thông
// tin còn hơn để lộ nguyên giá trị chỉ vì nó ngắn hơn dự kiến.
func maskEdges(s string, head, tail int) string {
	runes := []rune(s)
	if head+tail >= len(runes) {
		return redactedText
	}

	hidden := len(runes) - head - tail
	stars := hidden
	if stars > maxEdgesStars {
		stars = maxEdgesStars
	}

	var b strings.Builder
	b.Grow(head + stars + tail)
	b.WriteString(string(runes[:head]))
	b.WriteString(strings.Repeat("*", stars))
	b.WriteString(string(runes[len(runes)-tail:]))
	return b.String()
}
