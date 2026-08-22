package log

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// maskHandler áp masking lên mọi record đi qua, rồi chặn trần MaxLineBytes.
type maskHandler struct {
	next slog.Handler
	cfg  MaskConfig
}

// NewMaskHandler bọc next để mọi attribute đều đi qua ba lớp che.
//
// Đây là handler ngoài cùng của chain: nó phải thấy attribute ở dạng nguyên bản để
// che được, trước khi ContextHandler thêm metadata và trước khi handler cuối
// serialize.
//
// Attribute thêm bằng logger.With cũng được che, và che **một lần lúc gọi With**
// thay vì mỗi dòng log — logger dùng chung trong một request thường mang theo
// vài attribute cố định.
func NewMaskHandler(next slog.Handler, cfg MaskConfig) slog.Handler {
	return &maskHandler{next: next, cfg: cfg.normalize()}
}

func (h *maskHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *maskHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		if masked, keep := h.maskAttr(a); keep {
			attrs = append(attrs, masked)
		}
		return true
	})

	attrs = h.capLine(r.Message, attrs)

	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	out.AddAttrs(attrs...)
	return h.next.Handle(ctx, out)
}

func (h *maskHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	masked := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if m, keep := h.maskAttr(a); keep {
			masked = append(masked, m)
		}
	}
	return &maskHandler{next: h.next.WithAttrs(masked), cfg: h.cfg}
}

func (h *maskHandler) WithGroup(name string) slog.Handler {
	return &maskHandler{next: h.next.WithGroup(name), cfg: h.cfg}
}

// maskAttr che một attribute. keep = false nghĩa là bỏ hẳn attribute khỏi dòng log.
func (h *maskHandler) maskAttr(a slog.Attr) (out slog.Attr, keep bool) {
	if a.Value.Kind() == slog.KindGroup {
		members := a.Value.Group()
		masked := make([]slog.Attr, 0, len(members))
		for _, m := range members {
			if mm, ok := h.maskAttr(m); ok {
				masked = append(masked, mm)
			}
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(masked...)}, true
	}

	// Tên attribute cũng là tên field: slog.String("password", pw) phải bị che
	// đúng như một field password trong body.
	if r, ok := h.cfg.ruleFor(a.Key); ok {
		val, omit := applyRule(parseRule(r), a.Value.Any(), h.cfg)
		if omit {
			return slog.Attr{}, false
		}
		return slog.Attr{Key: a.Key, Value: lint1(val, h.cfg)}, true
	}

	// Không Resolve() ở đây: safeValue phải được maskValue xử lý bằng cfg của
	// handler này, còn Resolve() sẽ khiến nó tự che bằng cấu hình mặc định.
	val, omit := maskValue(a.Value.Any(), h.cfg, 0)
	if omit {
		return slog.Attr{}, false
	}
	return slog.Attr{Key: a.Key, Value: val}, true
}

// capLine là chốt cuối: nếu dòng log vẫn vượt trần sau khi che, bỏ attribute lớn
// nhất và thay bằng marker, lặp tới khi lọt trần.
//
// Thà mất body của một request còn hơn để backend từ chối cả dòng log — lúc đó
// mất luôn cả trace_id lẫn status, tức là mất khả năng biết request nào đã xảy ra.
// Vì bỏ dần từ attribute lớn nhất, những attribute nhỏ và quan trọng nhất
// (trace_id, status, method) là những thứ sống sót cuối cùng.
func (h *maskHandler) capLine(msg string, attrs []slog.Attr) []slog.Attr {
	sizes := make([]int, len(attrs))
	total := recordOverhead + len(msg)
	for i, a := range attrs {
		sizes[i] = estimateAttr(a)
		total += sizes[i]
	}
	if total <= h.cfg.MaxLineBytes {
		return attrs
	}

	// Bỏ theo thứ tự kích thước giảm dần, tên field làm tiêu chí phân định khi
	// bằng nhau để kết quả tất định.
	order := make([]int, len(attrs))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(x, y int) int {
		if sizes[x] != sizes[y] {
			return sizes[y] - sizes[x]
		}
		return strings.Compare(attrs[x].Key, attrs[y].Key)
	})

	reason := fmt.Sprintf("log line exceeded %s", humanBytes(h.cfg.MaxLineBytes))
	for _, i := range order {
		if total <= h.cfg.MaxLineBytes {
			break
		}
		marker := slog.Attr{
			Key:   attrs[i].Key,
			Value: slog.AnyValue(dropped{reason: reason, bytes: sizes[i]}),
		}
		total += estimateAttr(marker) - sizes[i]
		attrs[i] = marker
	}
	return attrs
}

// Chi phí cố định của một dòng log JSON: time, level, dấu ngoặc, key của msg.
// Xấp xỉ là đủ — mục đích là đứng dưới giới hạn của backend, không phải đếm chính xác.
const recordOverhead = 96

// estimateAttr ước lượng số byte một attribute chiếm trong JSON.
func estimateAttr(a slog.Attr) int {
	return len(a.Key) + 4 + estimateValue(a.Value)
}

func estimateValue(v slog.Value) int {
	switch v.Kind() {
	case slog.KindString:
		return len(v.String()) + 2
	case slog.KindGroup:
		n := 2
		for _, a := range v.Group() {
			n += estimateAttr(a)
		}
		return n
	case slog.KindLogValuer:
		return estimateValue(v.Resolve())
	case slog.KindAny:
		return estimateAny(v.Any())
	default:
		// Số, bool, thời gian, duration: luôn ngắn.
		return 24
	}
}

func estimateAny(v any) int {
	switch t := v.(type) {
	case nil:
		return 4
	case string:
		return len(t) + 2
	case []byte:
		// JSON encode []byte thành base64, dài hơn khoảng 4/3.
		return len(t)*4/3 + 2
	case []any:
		n := 2
		for _, item := range t {
			n += estimateAny(item) + 1
		}
		return n
	case map[string]any:
		n := 2
		for k, item := range t {
			n += len(k) + 4 + estimateAny(item)
		}
		return n
	case Elided:
		return len(t.Elided) + len(t.SHA256) + 48
	default:
		return 24
	}
}

// humanBytes in kích thước theo KB/MB khi chia hết, để marker đọc ra "32KB" thay
// vì "32768 bytes".
func humanBytes(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dMB", n/(1<<20))
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%dKB", n/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
