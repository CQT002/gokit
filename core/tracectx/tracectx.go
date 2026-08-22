// Package tracectx mang thông tin trace theo chuẩn W3C Trace Context qua
// context.Context, và parse/format header traceparent.
//
// Đây là nền của toàn bộ phần observability: cùng một cơ chế propagate dùng cho
// cả HTTP header và Kafka header, nên một request đi qua service → queue →
// service khác vẫn chung một trace ID. Package cố tình chỉ làm phần định dạng và
// truyền tay, không sinh span, không export đi đâu: khi nào cần OTel thật thì
// thay cài đặt của FromContext, không phải sửa mọi chỗ gọi.
//
// Tham chiếu: https://www.w3.org/TR/trace-context/
package tracectx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cqt002/gokit/core/idx"
)

// HeaderTraceparent là tên header W3C mang trace ID và span ID.
//
// Với Kafka thì dùng đúng tên này làm key của record header, để một trace đi
// xuyên HTTP và Kafka không phải đổi tên giữa đường.
const HeaderTraceparent = "traceparent"

// ErrInvalidTraceparent là lỗi gốc của mọi lỗi ParseTraceparent trả về, dùng với
// errors.Is. Chi tiết cụ thể nằm trong phần message và có thể đổi.
var ErrInvalidTraceparent = errors.New("traceparent không hợp lệ")

const (
	// version00 là phiên bản duy nhất đã được đặc tả. Phiên bản cao hơn được
	// parse theo kiểu tương thích tiến (xem ParseTraceparent).
	version00 = "00"
	// versionInvalid bị đặc tả W3C cấm hẳn, để dành làm giá trị báo lỗi.
	versionInvalid = "ff"

	traceIDLen = 32 // 16 byte hex
	spanIDLen  = 16 // 8 byte hex
	flagLen    = 2

	flagSampled = 0x01
)

// SpanContext là phần thông tin trace đi kèm một đơn vị công việc.
type SpanContext struct {
	// TraceID là 32 ký tự hex viết thường, giữ nguyên suốt một request xuyên
	// nhiều service.
	TraceID string
	// SpanID là 16 ký tự hex viết thường, riêng cho từng chặng trong trace.
	SpanID string
	// Sampled là bit sampled của trace-flags: hạ nguồn nên ghi lại trace này hay
	// không. Package này không tự quyết định sampling, chỉ truyền lại quyết định
	// của thượng nguồn.
	Sampled bool
}

// NewRoot tạo trace mới với Sampled = true.
//
// Mặc định bật sampling vì gokit chưa có bộ sampling, và mất trace của một
// request lỗi tốn thời gian điều tra hơn là tốn chỗ lưu.
func NewRoot() SpanContext {
	return SpanContext{
		TraceID: idx.NewTraceID(),
		SpanID:  idx.NewSpanID(),
		Sampled: true,
	}
}

// NewChild tạo span mới trong cùng trace: giữ TraceID và Sampled, đổi SpanID.
//
// Nếu sc không hợp lệ (thường là SpanContext rỗng lấy từ context không có gì),
// NewChild trả về một trace gốc mới thay vì nhân bản trace ID rỗng — nếu không,
// mọi request thiếu header sẽ dồn vào chung một "trace" vô nghĩa.
func (sc SpanContext) NewChild() SpanContext {
	if !sc.Valid() {
		return NewRoot()
	}
	return SpanContext{
		TraceID: sc.TraceID,
		SpanID:  idx.NewSpanID(),
		Sampled: sc.Sampled,
	}
}

// Valid cho biết TraceID và SpanID có đúng định dạng W3C không: hex viết thường,
// đủ độ dài, và không phải toàn số 0 (đặc tả coi giá trị toàn 0 là không hợp lệ).
func (sc SpanContext) Valid() bool {
	return validID(sc.TraceID, traceIDLen) && validID(sc.SpanID, spanIDLen)
}

// Traceparent trả về giá trị header dạng "00-<trace>-<span>-<flags>".
// Trả về chuỗi rỗng nếu SpanContext không hợp lệ, để không bao giờ gửi đi một
// header rác mà hạ nguồn phải tự đoán.
func (sc SpanContext) Traceparent() string {
	if !sc.Valid() {
		return ""
	}
	flags := "00"
	if sc.Sampled {
		flags = "01"
	}
	return version00 + "-" + sc.TraceID + "-" + sc.SpanID + "-" + flags
}

// String cài đặt fmt.Stringer, trả về đúng giá trị traceparent.
func (sc SpanContext) String() string { return sc.Traceparent() }

// ParseTraceparent đọc giá trị header traceparent.
//
// Quy tắc theo đặc tả W3C:
//   - phiên bản "00": phải đúng 4 trường, không được có phần thừa;
//   - phiên bản cao hơn: parse 4 trường đầu và bỏ qua phần thừa, để service này
//     không làm đứt trace của thượng nguồn dùng phiên bản mới hơn;
//   - phiên bản "ff" bị cấm;
//   - hex phải viết thường, và trace ID / span ID không được toàn số 0.
//
// Giá trị không hợp lệ trả về lỗi bọc ErrInvalidTraceparent. Chỗ gọi nên coi đó
// là "không có trace thượng nguồn" và gọi NewRoot, chứ không phải là lỗi request.
func ParseTraceparent(v string) (SpanContext, error) {
	parts := strings.Split(v, "-")
	if len(parts) < 4 {
		return SpanContext{}, fmt.Errorf("%w: cần ít nhất 4 trường, có %d", ErrInvalidTraceparent, len(parts))
	}

	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]

	if !validHex(version, flagLen) {
		return SpanContext{}, fmt.Errorf("%w: phiên bản %q", ErrInvalidTraceparent, version)
	}
	if version == versionInvalid {
		return SpanContext{}, fmt.Errorf("%w: phiên bản ff bị đặc tả cấm", ErrInvalidTraceparent)
	}
	if version == version00 && len(parts) != 4 {
		return SpanContext{}, fmt.Errorf("%w: phiên bản 00 phải có đúng 4 trường, có %d", ErrInvalidTraceparent, len(parts))
	}
	if !validID(traceID, traceIDLen) {
		return SpanContext{}, fmt.Errorf("%w: trace ID %q", ErrInvalidTraceparent, traceID)
	}
	if !validID(spanID, spanIDLen) {
		return SpanContext{}, fmt.Errorf("%w: span ID %q", ErrInvalidTraceparent, spanID)
	}
	if !validHex(flags, flagLen) {
		return SpanContext{}, fmt.Errorf("%w: trace flags %q", ErrInvalidTraceparent, flags)
	}

	return SpanContext{
		TraceID: traceID,
		SpanID:  spanID,
		// Chỉ đọc bit sampled, các bit khác thuộc phiên bản sau nên bỏ qua.
		Sampled: hexNibble(flags[1])&flagSampled != 0,
	}, nil
}

type ctxKey struct{}

// WithSpanContext gắn sc vào ctx, ghi đè giá trị cũ nếu có.
func WithSpanContext(ctx context.Context, sc SpanContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, sc)
}

// FromContext lấy SpanContext đã gắn. Trả về ok = false nếu ctx chưa có gì —
// lúc đó dùng NewRoot.
func FromContext(ctx context.Context) (SpanContext, bool) {
	sc, ok := ctx.Value(ctxKey{}).(SpanContext)
	return sc, ok
}

// TraceIDFrom trả về trace ID trong ctx, hoặc chuỗi rỗng nếu chưa có. Tiện cho
// chỗ chỉ cần đính trace ID vào dòng log.
func TraceIDFrom(ctx context.Context) string {
	sc, ok := FromContext(ctx)
	if !ok {
		return ""
	}
	return sc.TraceID
}

// validID kiểm tra định dạng hex đúng độ dài và loại giá trị toàn số 0.
func validID(s string, n int) bool {
	if !validHex(s, n) {
		return false
	}
	for i := range len(s) {
		if s[i] != '0' {
			return true
		}
	}
	return false
}

// validHex đòi hex viết thường, đúng đặc tả W3C. Chuỗi viết hoa bị coi là không
// hợp lệ chứ không "sửa hộ": nếu thượng nguồn gửi sai định dạng, im lặng chấp
// nhận sẽ che mất lỗi ở phía họ.
func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// hexNibble đổi một ký tự hex viết thường đã kiểm tra thành giá trị 0..15.
func hexNibble(c byte) byte {
	if c >= 'a' {
		return c - 'a' + 10
	}
	return c - '0'
}
