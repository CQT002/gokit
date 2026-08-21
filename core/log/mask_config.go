package log

import (
	"strconv"
	"strings"
)

// Rule là luật che một giá trị. Dùng ở cả lớp 2 (tag `log:` trên struct) và lớp 3
// (danh sách tên field trong MaskConfig.Fields), nên cú pháp giống nhau ở hai chỗ.
//
// Luật có tham số thì viết sau dấu `=`: "truncate=100", "edges=6,4".
type Rule string

// Các luật che.
const (
	// RuleRedact thay toàn bộ giá trị bằng "********", bất kể dài ngắn.
	RuleRedact Rule = "redact"
	// RuleElide thay giá trị bằng metadata {_elided, bytes, sha256}. Dùng cho
	// payload nhị phân hoặc base64: 50 ký tự đầu của base64 không đọc được gì,
	// còn kích thước và hash trả lời được "có file không, nặng bao nhiêu, có
	// trùng file lần trước không".
	RuleElide Rule = "elide"
	// RuleTruncate giữ N ký tự đầu, mặc định 32 nếu không khai. Cắt theo rune
	// chứ không theo byte, nên tiếng Việt không bị đứt giữa ký tự.
	RuleTruncate Rule = "truncate"
	// RuleEdges giữ p ký tự đầu và s ký tự cuối, mặc định "edges=0,4" — chỉ giữ
	// 4 ký tự cuối. Dùng cho số thẻ, số tài khoản, CCCD.
	RuleEdges Rule = "edges"
	// RuleOmit bỏ hẳn key khỏi log.
	RuleOmit Rule = "omit"
	// RuleHash thay giá trị bằng sha256 đầy đủ: đối chiếu được hai lần xuất hiện
	// mà không đọc được nội dung.
	//
	// CẢNH BÁO: hash chỉ một chiều khi giá trị có đủ entropy. sha256 của một OTP
	// 6 số hay một số thẻ 16 số bị dò ngược trong vài giây bằng brute force. Với
	// những giá trị đó dùng RuleRedact hoặc RuleEdges.
	RuleHash Rule = "hash"
)

// Giá trị mặc định của MaskConfig.
const (
	// DefaultMaxLen là ngưỡng lớp 1: string dài hơn mức này bị rút gọn thành
	// metadata.
	DefaultMaxLen = 256
	// DefaultMaxLineBytes là trần cho toàn dòng log, xấp xỉ theo byte thô.
	DefaultMaxLineBytes = 32 << 10

	defaultTruncate  = 32
	defaultEdgesTail = 4

	// redactedText là giá trị thay thế của RuleRedact. Khác chuỗi của
	// core/secret để đọc log biết được che ở tầng nào.
	redactedText = "********"

	// maxEdgesStars là số dấu sao tối đa RuleEdges sinh ra. Giá trị ngắn (số thẻ,
	// CCCD) giữ đúng độ dài cho dễ đọc; giá trị dài thì chặn lại để không biến
	// thành hàng nghìn dấu sao.
	maxEdgesStars = 32

	// maxDepth chặn đệ quy vô hạn khi struct hoặc map trỏ vòng lại chính nó.
	// Logger không được phép làm sập process vì một cấu trúc dữ liệu lạ.
	maxDepth = 32

	// minMaxLen là sàn của MaxLen, đủ chỗ cho chuỗi thay thế dài nhất mà cơ chế
	// che tự sinh ra ("[REDACTED]" của core/secret, 10 byte).
	minMaxLen = 16
)

// MaskConfig cấu hình lớp 1 (theo kích thước) và lớp 3 (theo tên field), cùng
// trần cho toàn dòng log.
//
// Giá trị zero dùng được ngay và đã an toàn: MaxLen 256, MaxLineBytes 32KB, và
// danh sách tên field mặc định ở DefaultMaskFields. Khai thêm chỉ để tinh chỉnh.
type MaskConfig struct {
	// Fields là luật theo tên field cho lớp 3, khớp ở mọi độ sâu.
	//
	// Nội dung khai ở đây được **trộn thêm** vào DefaultMaskFields chứ không
	// thay thế: khai một field riêng của app không được vô tình tắt việc che
	// password. Trùng tên thì giá trị khai ở đây thắng.
	//
	// So khớp không phân biệt hoa thường, và coi `-` với `_` là như nhau, nên
	// "Api-Key", "api_key", "API_KEY" đều khớp cùng một luật.
	Fields map[string]Rule

	// MaxLen là ngưỡng của lớp 1: string dài hơn mức này bị rút gọn thành
	// metadata. Đây là lưới an toàn bắt được cả field chưa ai nghĩ tới, nên
	// <= 0 nghĩa là dùng mặc định 256, không phải "tắt".
	//
	// Giá trị nhỏ hơn minMaxLen bị nâng lên: chuỗi thay thế của chính cơ chế che
	// ("[REDACTED]", "********") không được biến thành metadata elide, vì lúc đó
	// dòng log vừa vô nghĩa vừa làm người đọc tưởng có dữ liệu bị bỏ.
	MaxLen int

	// DisableElideHash tắt phần sha256 trong metadata elide.
	//
	// Đặt tên theo chiều phủ định để giá trị zero là chiều an toàn (CÓ hash).
	// Đặc tả ở plan gọi field này là HashElide với mặc định true — cùng hành vi,
	// nhưng bool zero trong Go là false nên chiều phủ định mới giữ được mặc định.
	DisableElideHash bool

	// MaxLineBytes là trần cho toàn dòng log. Vượt trần thì attribute lớn nhất
	// bị bỏ và thay bằng marker _dropped, giữ lại các attribute nhỏ như trace_id
	// và status. <= 0 nghĩa là dùng mặc định 32KB.
	//
	// Kích thước được ước lượng theo byte thô, chưa tính phần escape của JSON,
	// nên hãy đặt thấp hơn giới hạn thật của backend một chút.
	MaxLineBytes int
}

// DefaultMaskFields trả về danh sách tên field được che mặc định ở lớp 3.
//
// Trả về bản copy mới mỗi lần gọi để chỗ gọi sửa thoải mái mà không ảnh hưởng
// logger khác.
func DefaultMaskFields() map[string]Rule {
	return map[string]Rule{
		"password":      RuleRedact,
		"new_password":  RuleRedact,
		"token":         RuleRedact,
		"access_token":  RuleRedact,
		"refresh_token": RuleRedact,
		"otp":           RuleRedact,
		"pin":           RuleRedact,
		"cvv":           RuleRedact,
		"secret":        RuleRedact,
		"authorization": RuleRedact,
		"api_key":       RuleRedact,
	}
}

// normalize điền giá trị mặc định và chuẩn hoá key của Fields. Gọi một lần lúc
// dựng handler, không phải mỗi dòng log.
func (c MaskConfig) normalize() MaskConfig {
	if c.MaxLen <= 0 {
		c.MaxLen = DefaultMaxLen
	}
	if c.MaxLen < minMaxLen {
		c.MaxLen = minMaxLen
	}
	if c.MaxLineBytes <= 0 {
		c.MaxLineBytes = DefaultMaxLineBytes
	}

	fields := DefaultMaskFields()
	for k, v := range c.Fields {
		fields[normalizeFieldName(k)] = v
	}
	c.Fields = fields
	return c
}

// ruleFor tra luật theo tên field. Trả về ok = false nếu tên không nằm trong danh sách.
func (c MaskConfig) ruleFor(name string) (Rule, bool) {
	r, ok := c.Fields[normalizeFieldName(name)]
	return r, ok
}

// normalizeFieldName đưa tên field về dạng so khớp được: chữ thường, `-` thành `_`.
func normalizeFieldName(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "-", "_")
}

// parsedRule là một Rule đã tách tham số.
type parsedRule struct {
	kind Rule
	n    int // truncate=n, hoặc số ký tự đầu của edges
	tail int // số ký tự cuối của edges
}

// parseRule tách "truncate=100" hay "edges=6,4" thành luật và tham số.
//
// Cú pháp sai hoặc luật không nhận ra đều trả về RuleRedact. Che chặt hơn ý muốn
// là hướng sai an toàn; và output thành "********" thay vì giá trị mong đợi là
// thứ test nhận ra ngay.
func parseRule(r Rule) parsedRule {
	name, arg, hasArg := strings.Cut(string(r), "=")

	switch Rule(strings.TrimSpace(name)) {
	case RuleRedact:
		return parsedRule{kind: RuleRedact}
	case RuleOmit:
		return parsedRule{kind: RuleOmit}
	case RuleElide:
		return parsedRule{kind: RuleElide}
	case RuleHash:
		return parsedRule{kind: RuleHash}

	case RuleTruncate:
		n := defaultTruncate
		if hasArg {
			parsed, err := strconv.Atoi(strings.TrimSpace(arg))
			if err != nil || parsed < 0 {
				return parsedRule{kind: RuleRedact}
			}
			n = parsed
		}
		return parsedRule{kind: RuleTruncate, n: n}

	case RuleEdges:
		p, tail := 0, defaultEdgesTail
		if hasArg {
			head, last, ok := strings.Cut(arg, ",")
			if !ok {
				return parsedRule{kind: RuleRedact}
			}
			var err error
			if p, err = strconv.Atoi(strings.TrimSpace(head)); err != nil || p < 0 {
				return parsedRule{kind: RuleRedact}
			}
			if tail, err = strconv.Atoi(strings.TrimSpace(last)); err != nil || tail < 0 {
				return parsedRule{kind: RuleRedact}
			}
		}
		return parsedRule{kind: RuleEdges, n: p, tail: tail}

	default:
		return parsedRule{kind: RuleRedact}
	}
}
