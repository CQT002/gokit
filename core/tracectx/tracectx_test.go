package tracectx_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/tracectx"
)

const (
	validTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	validSpan  = "00f067aa0ba902b7"
)

func TestNewRoot(t *testing.T) {
	sc := tracectx.NewRoot()
	if !sc.Valid() {
		t.Fatalf("NewRoot() không hợp lệ: %+v", sc)
	}
	if !sc.Sampled {
		t.Error("Sampled = false, muốn true (mặc định bật)")
	}
	if other := tracectx.NewRoot(); other.TraceID == sc.TraceID {
		t.Error("hai lần NewRoot() ra cùng trace ID")
	}
}

func TestTraceparent(t *testing.T) {
	tests := []struct {
		name string
		sc   tracectx.SpanContext
		want string
	}{
		{
			name: "sampled",
			sc:   tracectx.SpanContext{TraceID: validTrace, SpanID: validSpan, Sampled: true},
			want: "00-" + validTrace + "-" + validSpan + "-01",
		},
		{
			name: "không sampled",
			sc:   tracectx.SpanContext{TraceID: validTrace, SpanID: validSpan},
			want: "00-" + validTrace + "-" + validSpan + "-00",
		},
		{"rỗng thì không phát header", tracectx.SpanContext{}, ""},
		{"thiếu span ID", tracectx.SpanContext{TraceID: validTrace}, ""},
		{"trace ID toàn số 0", tracectx.SpanContext{TraceID: strings.Repeat("0", 32), SpanID: validSpan}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sc.Traceparent(); got != tt.want {
				t.Errorf("Traceparent() = %q, muốn %q", got, tt.want)
			}
			if got := tt.sc.String(); got != tt.want {
				t.Errorf("String() = %q, muốn %q", got, tt.want)
			}
		})
	}
}

func TestParseTraceparent_HopLe(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantSampled bool
	}{
		{"phiên bản 00 sampled", "00-" + validTrace + "-" + validSpan + "-01", true},
		{"phiên bản 00 không sampled", "00-" + validTrace + "-" + validSpan + "-00", false},
		{"flags có bit khác ngoài sampled", "00-" + validTrace + "-" + validSpan + "-03", true},
		{"flags chỉ bit lạ", "00-" + validTrace + "-" + validSpan + "-02", false},
		{"flags ff", "00-" + validTrace + "-" + validSpan + "-ff", true},
		// Tương thích tiến: phiên bản mới hơn được phép thêm trường.
		{"phiên bản 01 có trường thừa", "01-" + validTrace + "-" + validSpan + "-01-abcdef", true},
		{"phiên bản fe", "fe-" + validTrace + "-" + validSpan + "-00", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, err := tracectx.ParseTraceparent(tt.in)
			if err != nil {
				t.Fatalf("ParseTraceparent(%q) lỗi: %v", tt.in, err)
			}
			if sc.TraceID != validTrace {
				t.Errorf("TraceID = %q, muốn %q", sc.TraceID, validTrace)
			}
			if sc.SpanID != validSpan {
				t.Errorf("SpanID = %q, muốn %q", sc.SpanID, validSpan)
			}
			if sc.Sampled != tt.wantSampled {
				t.Errorf("Sampled = %v, muốn %v", sc.Sampled, tt.wantSampled)
			}
		})
	}
}

func TestParseTraceparent_KhongHopLe(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"rỗng", ""},
		{"rác", "khong-phai-traceparent"},
		{"thiếu trường", "00-" + validTrace + "-" + validSpan},
		{"phiên bản ff bị cấm", "ff-" + validTrace + "-" + validSpan + "-01"},
		{"phiên bản 1 ký tự", "0-" + validTrace + "-" + validSpan + "-01"},
		{"phiên bản không phải hex", "zz-" + validTrace + "-" + validSpan + "-01"},
		{"phiên bản 00 có trường thừa", "00-" + validTrace + "-" + validSpan + "-01-thua"},
		{"trace ID ngắn", "00-" + validTrace[:31] + "-" + validSpan + "-01"},
		{"trace ID dài", "00-" + validTrace + "a-" + validSpan + "-01"},
		{"trace ID toàn số 0", "00-" + strings.Repeat("0", 32) + "-" + validSpan + "-01"},
		{"trace ID viết hoa", "00-" + strings.ToUpper(validTrace) + "-" + validSpan + "-01"},
		{"trace ID không phải hex", "00-" + strings.Repeat("g", 32) + "-" + validSpan + "-01"},
		{"span ID ngắn", "00-" + validTrace + "-" + validSpan[:15] + "-01"},
		{"span ID toàn số 0", "00-" + validTrace + "-" + strings.Repeat("0", 16) + "-01"},
		{"span ID viết hoa", "00-" + validTrace + "-" + strings.ToUpper(validSpan) + "-01"},
		{"flags 1 ký tự", "00-" + validTrace + "-" + validSpan + "-1"},
		{"flags không phải hex", "00-" + validTrace + "-" + validSpan + "-zz"},
		{"chỉ có dấu gạch", "---"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, err := tracectx.ParseTraceparent(tt.in)
			if err == nil {
				t.Fatalf("ParseTraceparent(%q) không báo lỗi, trả %+v", tt.in, sc)
			}
			if !errors.Is(err, tracectx.ErrInvalidTraceparent) {
				t.Errorf("lỗi %v không bọc ErrInvalidTraceparent", err)
			}
			if sc != (tracectx.SpanContext{}) {
				t.Errorf("khi lỗi phải trả SpanContext rỗng, được %+v", sc)
			}
		})
	}
}

// Vòng tròn format → parse → format phải bất động: đây là tính chất giữ cho
// trace không bị đứt khi đi qua nhiều chặng.
func TestVongTronFormatParse(t *testing.T) {
	for _, sampled := range []bool{true, false} {
		sc := tracectx.NewRoot()
		sc.Sampled = sampled

		got, err := tracectx.ParseTraceparent(sc.Traceparent())
		if err != nil {
			t.Fatalf("ParseTraceparent: %v", err)
		}
		if got != sc {
			t.Errorf("vòng tròn lệch: %+v → %+v", sc, got)
		}
	}
}

func TestNewChild(t *testing.T) {
	parent := tracectx.SpanContext{TraceID: validTrace, SpanID: validSpan}
	child := parent.NewChild()

	if child.TraceID != parent.TraceID {
		t.Errorf("TraceID = %q, muốn giữ nguyên %q", child.TraceID, parent.TraceID)
	}
	if child.SpanID == parent.SpanID {
		t.Error("SpanID không đổi, span con phải có ID riêng")
	}
	if !child.Valid() {
		t.Errorf("span con không hợp lệ: %+v", child)
	}
	if child.Sampled != parent.Sampled {
		t.Errorf("Sampled = %v, muốn kế thừa %v", child.Sampled, parent.Sampled)
	}

	parent.Sampled = true
	if !parent.NewChild().Sampled {
		t.Error("span con không kế thừa Sampled = true")
	}
}

// Trường hợp hay gặp nhất trong middleware: request không có header nào.
func TestNewChild_TuSpanContextRong(t *testing.T) {
	child := tracectx.SpanContext{}.NewChild()
	if !child.Valid() {
		t.Fatalf("muốn trace gốc mới, được %+v", child)
	}
	if !child.Sampled {
		t.Error("trace gốc mới phải có Sampled = true")
	}

	// Cả trường hợp SpanContext hỏng một nửa cũng phải ra trace gốc mới, không
	// được nhân bản trace ID rác.
	broken := tracectx.SpanContext{TraceID: "khong-phai-hex", SpanID: validSpan}.NewChild()
	if !broken.Valid() || broken.TraceID == "khong-phai-hex" {
		t.Errorf("muốn trace gốc mới, được %+v", broken)
	}
}

func TestContext(t *testing.T) {
	ctx := context.Background()

	if sc, ok := tracectx.FromContext(ctx); ok {
		t.Errorf("context rỗng trả ok = true (%+v)", sc)
	}
	if got := tracectx.TraceIDFrom(ctx); got != "" {
		t.Errorf("TraceIDFrom(rỗng) = %q, muốn rỗng", got)
	}

	want := tracectx.NewRoot()
	ctx = tracectx.WithSpanContext(ctx, want)

	got, ok := tracectx.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext trả ok = false sau khi gắn")
	}
	if got != want {
		t.Errorf("FromContext = %+v, muốn %+v", got, want)
	}
	if id := tracectx.TraceIDFrom(ctx); id != want.TraceID {
		t.Errorf("TraceIDFrom = %q, muốn %q", id, want.TraceID)
	}

	// Gắn lần nữa thì ghi đè.
	next := want.NewChild()
	if got, _ := tracectx.FromContext(tracectx.WithSpanContext(ctx, next)); got != next {
		t.Errorf("ghi đè không có tác dụng: %+v", got)
	}
}

func TestHeaderTraceparent(t *testing.T) {
	// Tên header phải viết thường đúng đặc tả: Kafka header phân biệt hoa thường,
	// nên một chữ hoa ở đây làm trace đứt khi đi qua queue.
	if tracectx.HeaderTraceparent != "traceparent" {
		t.Errorf("HeaderTraceparent = %q", tracectx.HeaderTraceparent)
	}
}
