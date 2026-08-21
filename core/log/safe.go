package log

import (
	"encoding"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// tagKey là tên tag đọc luật che trên struct field.
const tagKey = "log"

// Safe bọc v lại để mọi luật che được áp khi ghi log.
//
//	logger.InfoContext(ctx, "request", slog.Any("body", log.Safe(req)))
//
// Đây là lớp 2 — cơ chế chính. Luật khai bằng tag `log:` ngay trên field:
//
//	type UploadDocRequest struct {
//	    UserID   string `json:"user_id"`
//	    Image    string `json:"image"    log:"elide"`
//	    Note     string `json:"note"     log:"truncate=100"`
//	    Password string `json:"password" log:"redact"`
//	    CardNo   string `json:"card_no"  log:"edges=6,4"`
//	    Internal string `json:"-"        log:"omit"`
//	}
//
// Luật đi cùng field nên đổi tên field thì luật đi theo, không thể lệch pha. Struct
// lồng nhau thì đệ quy tự đi theo type, không cần cú pháp đường dẫn.
//
// Field không có tag vẫn được lớp 3 (theo tên field) và lớp 1 (theo kích thước)
// phủ, nên bọc Safe không bao giờ làm log kém an toàn hơn không bọc.
//
// Tên key trong log lấy từ tag `json` nếu có, để dòng log khớp với thứ client gửi.
func Safe(v any) slog.LogValuer { return safeValue{v: v} }

// safeValue là giá trị chờ được che. Handler của package này nhận ra type để che
// theo đúng MaxLen và danh sách field đã cấu hình; dùng với handler slog thường
// thì nó tự che theo cấu hình mặc định.
type safeValue struct{ v any }

// LogValue cài đặt slog.LogValuer.
func (s safeValue) LogValue() slog.Value {
	val, omit := maskValue(s.v, defaultConfig(), 0)
	if omit {
		return slog.StringValue(redactedText)
	}
	return val
}

var (
	defaultConfigOnce sync.Once
	defaultConfigVal  MaskConfig
)

func defaultConfig() MaskConfig {
	defaultConfigOnce.Do(func() { defaultConfigVal = MaskConfig{}.normalize() })
	return defaultConfigVal
}

// maskValue là walk duy nhất của cả ba lớp, dùng chung cho Safe, SafeMap và
// handler.
//
// Thứ tự ưu tiên tại mỗi giá trị:
//
//  1. tag `log:` trên struct field (lớp 2) — khai tường minh thì thắng;
//  2. tên field khớp MaskConfig.Fields (lớp 3) — fallback khi không có tag;
//  3. kích thước vượt MaxLen (lớp 1) — lưới an toàn, không có ngoại lệ.
//
// Lớp 1 áp cả lên kết quả của lớp 2: một tag truncate=1000 với MaxLen 256 thì vẫn
// bị rút gọn. Lưới an toàn mà có ngoại lệ thì không còn là lưới.
func maskValue(v any, cfg MaskConfig, depth int) (val slog.Value, omit bool) {
	if depth > maxDepth {
		return slog.StringValue("…(vượt độ sâu tối đa)"), false
	}
	if v == nil {
		return slog.AnyValue(nil), false
	}

	// Giá trị tự biết cách hiện ra log thì tôn trọng — đây là đường che của
	// core/secret.Secret, và nó phải chạy trước mọi thao tác reflect, nếu không
	// reflect sẽ đọc thẳng chuỗi bí mật.
	if lv, ok := v.(slog.LogValuer); ok {
		if sv, isSafe := lv.(safeValue); isSafe {
			return maskValue(sv.v, cfg, depth) // Safe lồng Safe
		}
		return maskValue(lv.LogValue().Resolve().Any(), cfg, depth+1)
	}

	switch t := v.(type) {
	case string:
		return elideIfLong([]byte(t), cfg, slog.StringValue(t)), false
	case []byte:
		return elideIfLong(t, cfg, slog.AnyValue(t)), false
	}

	// Dạng text chuẩn của những type như time.Time, uuid.UUID, net.IP: chúng
	// không có field export nào, walk vào chỉ ra group rỗng.
	if tm, ok := v.(encoding.TextMarshaler); ok {
		if b, err := tm.MarshalText(); err == nil {
			return elideIfLong(b, cfg, slog.StringValue(string(b))), false
		}
	}

	return maskReflect(reflect.ValueOf(v), cfg, depth)
}

// elideIfLong là lớp 1: quá dài thì thay bằng metadata, còn lại giữ nguyên.
func elideIfLong(b []byte, cfg MaskConfig, asIs slog.Value) slog.Value {
	// So theo rune để một chuỗi tiếng Việt không bị coi là dài gấp ba chỉ vì
	// UTF-8, nhưng vẫn dùng độ dài byte làm trần cứng cho dữ liệu nhị phân.
	if len(b) <= cfg.MaxLen || utf8.RuneCount(b) <= cfg.MaxLen {
		return asIs
	}
	return slog.AnyValue(elide(b, cfg))
}

func maskReflect(rv reflect.Value, cfg MaskConfig, depth int) (val slog.Value, omit bool) {
	switch rv.Kind() {
	case reflect.Invalid:
		return slog.AnyValue(nil), false

	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return slog.AnyValue(nil), false
		}
		return maskValue(rv.Elem().Interface(), cfg, depth+1)

	case reflect.Struct:
		return slog.GroupValue(maskStruct(rv, cfg, depth)...), false

	case reflect.Map:
		return maskMap(rv, cfg, depth)

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return slog.AnyValue(nil), false
		}
		items := make([]any, 0, rv.Len())
		for i := range rv.Len() {
			item, skip := maskValue(rv.Index(i).Interface(), cfg, depth+1)
			if skip {
				continue
			}
			items = append(items, toAny(item))
		}
		return slog.AnyValue(items), false

	default:
		// Số, bool, và các kiểu vô hại khác: không có gì để che, và lớp 1 không
		// áp dụng vì chúng không thể dài.
		return slog.AnyValue(rv.Interface()), false
	}
}

// maskStruct che từng field theo plan đã cache của type.
func maskStruct(rv reflect.Value, cfg MaskConfig, depth int) []slog.Attr {
	plan := planFor(rv.Type())
	attrs := make([]slog.Attr, 0, len(plan))

	for _, f := range plan {
		fv := rv.Field(f.index)

		// Field nhúng không có tên json: trải phẳng vào cha, giống encoding/json,
		// để dòng log có cùng hình dạng với body thật.
		if f.embed {
			if fv.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue
				}
				fv = fv.Elem()
			}
			attrs = append(attrs, maskStruct(fv, cfg, depth+1)...)
			continue
		}

		if f.hasRule {
			// Lớp 2 thắng: đã khai tường minh thì không xét tên field nữa.
			val, omit := applyRule(f.rule, valueOf(fv), cfg)
			if omit {
				continue
			}
			attrs = append(attrs, slog.Attr{Key: f.name, Value: lint1(val, cfg)})
			continue
		}

		if r, ok := cfg.ruleFor(f.name); ok {
			// Lớp 3: không có tag thì rơi về danh sách tên field.
			val, omit := applyRule(parseRule(r), valueOf(fv), cfg)
			if omit {
				continue
			}
			attrs = append(attrs, slog.Attr{Key: f.name, Value: lint1(val, cfg)})
			continue
		}

		val, omit := maskValue(valueOf(fv), cfg, depth+1)
		if omit {
			continue
		}
		attrs = append(attrs, slog.Attr{Key: f.name, Value: val})
	}
	return attrs
}

// maskMap che từng entry của map, key sắp xếp để output tất định.
//
// Thứ tự lặp map trong Go là ngẫu nhiên; không sắp xếp thì hai lần log cùng một
// payload ra hai dòng khác nhau và golden test không dùng được.
func maskMap(rv reflect.Value, cfg MaskConfig, depth int) (slog.Value, bool) {
	if rv.IsNil() {
		return slog.AnyValue(nil), false
	}

	keys := rv.MapKeys()
	names := make([]string, 0, len(keys))
	byName := make(map[string]reflect.Value, len(keys))
	for _, k := range keys {
		name := keyString(k)
		names = append(names, name)
		byName[name] = rv.MapIndex(k)
	}
	slices.Sort(names)

	attrs := make([]slog.Attr, 0, len(names))
	for _, name := range names {
		mv := byName[name]

		if r, ok := cfg.ruleFor(name); ok {
			val, omit := applyRule(parseRule(r), valueOf(mv), cfg)
			if omit {
				continue
			}
			attrs = append(attrs, slog.Attr{Key: name, Value: lint1(val, cfg)})
			continue
		}

		val, omit := maskValue(valueOf(mv), cfg, depth+1)
		if omit {
			continue
		}
		attrs = append(attrs, slog.Attr{Key: name, Value: val})
	}
	return slog.GroupValue(attrs...), false
}

// lint1 áp lớp 1 lên kết quả của một luật tường minh. Chỉ chuỗi mới cần: mọi luật
// khác đã trả về giá trị ngắn hoặc metadata.
func lint1(v slog.Value, cfg MaskConfig) slog.Value {
	if v.Kind() != slog.KindString {
		return v
	}
	return elideIfLong([]byte(v.String()), cfg, v)
}

// valueOf lấy any từ reflect.Value, an toàn với field không đọc được.
func valueOf(rv reflect.Value) any {
	if !rv.IsValid() || !rv.CanInterface() {
		return nil
	}
	return rv.Interface()
}

// keyString đổi key của map thành chuỗi. Key không phải string vẫn hiển thị được,
// nhưng luật theo tên field chỉ khớp với key dạng chuỗi.
func keyString(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	val, _ := maskValue(valueOf(k), defaultConfig(), maxDepth)
	return val.String()
}

// toAny đổi slog.Value về giá trị Go thường, để nhúng vào slice hoặc trả ra từ
// SafeMap.
func toAny(v slog.Value) any {
	switch v.Kind() {
	case slog.KindGroup:
		out := make(map[string]any, len(v.Group()))
		for _, a := range v.Group() {
			out[a.Key] = toAny(a.Value)
		}
		return out
	case slog.KindString:
		return v.String()
	case slog.KindLogValuer:
		return toAny(v.Resolve())
	default:
		return v.Any()
	}
}

// fieldPlan là kết quả đọc tag của một field, đã tính sẵn để không phải parse lại.
type fieldPlan struct {
	index   int
	name    string
	rule    parsedRule
	hasRule bool
	embed   bool
}

// planCache giữ plan theo reflect.Type, nên chi phí reflect chỉ trả lần đầu cho
// mỗi type. Khoá là reflect.Type nên hai type khác nhau có cùng tên field cũng
// không thể lẫn plan của nhau.
var planCache sync.Map // reflect.Type -> []fieldPlan

func planFor(t reflect.Type) []fieldPlan {
	if cached, ok := planCache.Load(t); ok {
		return cached.([]fieldPlan)
	}

	plan := make([]fieldPlan, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		name, jsonSkip := jsonName(f)

		// Field nhúng không có tên json riêng thì trải phẳng, giống encoding/json.
		//
		// Xét trước điều kiện export: struct nhúng có thể là type unexported, và
		// khi đó bản thân field nhúng không lấy được qua Interface() nhưng các
		// field export bên trong nó thì lấy được. encoding/json cũng trải phẳng
		// trường hợp này, nên bỏ qua sẽ làm log thiếu field so với body thật.
		if f.Anonymous && name == f.Name && isStructish(f.Type) {
			plan = append(plan, fieldPlan{index: i, embed: true})
			continue
		}

		if !f.IsExported() {
			continue // không đọc được, và reflect sẽ panic nếu cố
		}

		rule, hasRule := f.Tag.Lookup(tagKey)
		if jsonSkip && !hasRule {
			// `json:"-"` mà không khai tag log: field không nằm trong body, cũng
			// không cần nằm trong log.
			continue
		}

		plan = append(plan, fieldPlan{
			index:   i,
			name:    name,
			rule:    parseRule(Rule(rule)),
			hasRule: hasRule,
		})
	}

	planCache.Store(t, plan)
	return plan
}

// jsonName lấy tên key theo tag json, trả về skip = true với `json:"-"`.
func jsonName(f reflect.StructField) (name string, skip bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, false
	}
	if tag == "-" {
		return f.Name, true
	}
	if n, _, _ := strings.Cut(tag, ","); n != "" {
		return n, false
	}
	return f.Name, false
}

func isStructish(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}
