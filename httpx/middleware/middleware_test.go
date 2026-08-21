package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cqt002/gokit/core/ctxmeta"
	"github.com/cqt002/gokit/core/log"
	"github.com/cqt002/gokit/core/tracectx"
	"github.com/cqt002/gokit/httpx/middleware"
)

// newLogger tạo logger ghi vào buffer, có masking như thật.
func newLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	return log.New(log.Options{Output: buf}), buf
}

// lines parse từng dòng log JSON.
func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("dòng log không phải JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// ---------- Chain ----------

func TestChain_ThuTuNgoaiVaoTrong(t *testing.T) {
	var order []string
	mark := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "vào "+name)
				next.ServeHTTP(w, r)
				order = append(order, "ra "+name)
			})
		}
	}

	h := middleware.Chain(mark("A"), mark("B"), mark("C"))(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { order = append(order, "handler") }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"vào A", "vào B", "vào C", "handler", "ra C", "ra B", "ra A"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("thứ tự = %v, muốn %v", order, want)
	}
}

func TestChain_BoQuaNil(t *testing.T) {
	h := middleware.Chain(nil, nil)(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestChain_Rong(t *testing.T) {
	rec := httptest.NewRecorder()
	middleware.Chain()(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// ---------- Trace ----------

func TestTrace_SinhTraceMoi(t *testing.T) {
	var gotTrace, gotRequestID string
	h := middleware.Trace(middleware.TraceOptions{})(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			gotTrace = tracectx.TraceIDFrom(r.Context())
			gotRequestID = ctxmeta.RequestID(r.Context())
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(gotTrace) != 32 {
		t.Errorf("trace ID = %q, muốn 32 hex", gotTrace)
	}
	if gotRequestID == "" {
		t.Error("không sinh request ID")
	}
	if tp := rec.Header().Get("traceparent"); !strings.Contains(tp, gotTrace) {
		t.Errorf("header traceparent = %q, phải chứa trace ID %q", tp, gotTrace)
	}
	if rec.Header().Get(middleware.HeaderRequestID) != gotRequestID {
		t.Error("request ID không được ghi vào header response")
	}
}

// Trace thượng nguồn phải được giữ: đó là cả lý do package tracectx tồn tại.
func TestTrace_KeThuaTraceThuongNguon(t *testing.T) {
	const upstreamTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	const upstreamSpan = "00f067aa0ba902b7"

	var got tracectx.SpanContext
	h := middleware.Trace(middleware.TraceOptions{})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got, _ = tracectx.FromContext(r.Context())
		}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("traceparent", "00-"+upstreamTrace+"-"+upstreamSpan+"-01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.TraceID != upstreamTrace {
		t.Errorf("TraceID = %q, muốn giữ %q", got.TraceID, upstreamTrace)
	}
	if got.SpanID == upstreamSpan {
		t.Error("SpanID không đổi — phải là span con, không phải dùng lại span cha")
	}
	if !got.Sampled {
		t.Error("mất cờ sampled của thượng nguồn")
	}
}

// Header rác không được làm request lỗi: thượng nguồn gửi sai không phải lý do từ
// chối phục vụ người dùng.
func TestTrace_HeaderRacThiTaoTraceMoi(t *testing.T) {
	for _, bad := range []string{"rác", "00-xxx-yyy-01", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", ""} {
		var got tracectx.SpanContext
		h := middleware.Trace(middleware.TraceOptions{})(http.HandlerFunc(
			func(_ http.ResponseWriter, r *http.Request) {
				got, _ = tracectx.FromContext(r.Context())
			}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("traceparent", bad)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if !got.Valid() {
			t.Errorf("traceparent %q: không tạo được trace mới (%+v)", bad, got)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("traceparent %q: status = %d, không được từ chối request", bad, rec.Code)
		}
	}
}

// Mặc định KHÔNG tin request ID của client: nếu tin thì client trộn được log của
// mình vào log của request khác.
func TestTrace_MacDinhKhongTinRequestIDCuaClient(t *testing.T) {
	var got string
	h := middleware.Trace(middleware.TraceOptions{})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) { got = ctxmeta.RequestID(r.Context()) }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.HeaderRequestID, "id-cua-client")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got == "id-cua-client" {
		t.Error("đã dùng request ID của client dù chưa bật TrustIncomingRequestID")
	}
}

func TestTrace_TinRequestIDKhiBat(t *testing.T) {
	var got string
	h := middleware.Trace(middleware.TraceOptions{TrustIncomingRequestID: true})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) { got = ctxmeta.RequestID(r.Context()) }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.HeaderRequestID, "id-cua-client")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "id-cua-client" {
		t.Errorf("request ID = %q, muốn dùng của client", got)
	}
}

// ID từ client đi vào mọi dòng log, nên ký tự xuống dòng trong đó là log injection.
func TestTrace_LamSachIDCuaClient(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ký tự xuống dòng", "abc\ndef", "abcdef"},
		{"dấu ngoặc kép", `abc"def`, "abcdef"},
		{"khoảng trắng", "abc def", "abcdef"},
		{"giữ gạch và chấm", "abc-def_1.2", "abc-def_1.2"},
		{"toàn ký tự lạ", "\n\r\t{}", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := middleware.Trace(middleware.TraceOptions{})(http.HandlerFunc(
				func(_ http.ResponseWriter, r *http.Request) {
					got = ctxmeta.CorrelationID(r.Context())
				}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(middleware.HeaderCorrelationID, tt.in)
			h.ServeHTTP(httptest.NewRecorder(), req)

			if got != tt.want {
				t.Errorf("correlation ID = %q, muốn %q", got, tt.want)
			}
		})
	}
}

func TestTrace_CatIDQuaDai(t *testing.T) {
	var got string
	h := middleware.Trace(middleware.TraceOptions{MaxIDLen: 10})(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = ctxmeta.CorrelationID(r.Context())
		}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.HeaderCorrelationID, strings.Repeat("a", 5000))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(got) != 10 {
		t.Errorf("độ dài = %d, muốn 10", len(got))
	}
}

// ---------- Recover ----------

func TestRecover(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.Recover(l)(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic("bùm") }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, muốn 500", rec.Code)
	}

	logs := lines(t, buf)
	if len(logs) != 1 {
		t.Fatalf("số dòng log = %d, muốn 1: %s", len(logs), buf.String())
	}
	if logs[0]["level"] != "ERROR" {
		t.Errorf("level = %v, muốn ERROR", logs[0]["level"])
	}
	if logs[0]["stack"] == nil {
		t.Error("thiếu stack trace — không có nó thì panic gần như không truy được")
	}
	if logs[0]["path"] != "/api" {
		t.Errorf("path = %v", logs[0]["path"])
	}
}

// Handler đã ghi header rồi mới panic: không được ghi thêm, vì status đã đi ra dây.
func TestRecover_HandlerDaGhiHeader(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.Recover(l)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("một phần"))
			panic("bùm")
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, muốn giữ 201 đã gửi", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Errorf("đã ghi thêm vào body đã gửi: %q", rec.Body.String())
	}
	if len(lines(t, buf)) != 1 {
		t.Error("vẫn phải ghi log panic")
	}
}

// ErrAbortHandler là cách net/http nói "hủy response này có chủ ý".
func TestRecover_TruyenTiepErrAbortHandler(t *testing.T) {
	l, _ := newLogger(t)
	h := middleware.Recover(l)(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))

	defer func() {
		if v := recover(); v == nil {
			t.Error("ErrAbortHandler bị bắt, phải được truyền tiếp")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRecover_KhongPanicThiKhongLog(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.Recover(l)(okHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if buf.Len() != 0 {
		t.Errorf("ghi log dù không panic: %s", buf.String())
	}
}

// ---------- Timeout ----------

// Điểm quan trọng nhất: client nhận response ngay tại mốc timeout, không phải chờ
// handler chịu dừng.
func TestTimeout_TraLoiNgayKhongChoHandler(t *testing.T) {
	handlerDone := make(chan struct{})
	h := middleware.Timeout(30 * time.Millisecond)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			// Handler cố tình không tôn trọng ctx.
			time.Sleep(400 * time.Millisecond)
			_, _ = w.Write([]byte("quá muộn"))
			close(handlerDone)
		}))

	rec := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("mất %v mới trả về — đã chờ handler thay vì cắt đúng mốc timeout", elapsed)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, muốn 503", rec.Code)
	}

	<-handlerDone
	if strings.Contains(rec.Body.String(), "quá muộn") {
		t.Errorf("handler ghi được vào response sau khi hết hạn: %q", rec.Body.String())
	}
}

func TestTimeout_HandlerXongTruocThiKhongCanThiep(t *testing.T) {
	h := middleware.Timeout(time.Second)(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestTimeout_HuyContextCuaHandler(t *testing.T) {
	var ctxErr error
	done := make(chan struct{})
	h := middleware.Timeout(20 * time.Millisecond)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			ctxErr = r.Context().Err()
			close(done)
		}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	<-done

	if ctxErr == nil {
		t.Error("context của handler không bị huỷ")
	}
}

// Panic trong handler chạy ở goroutine riêng phải nổi lên goroutine gọi, nếu không
// nó làm sập process thay vì được Recover bắt.
func TestTimeout_PanicVanDenDuocRecover(t *testing.T) {
	l, buf := newLogger(t)
	h := middleware.Chain(
		middleware.Recover(l),
		middleware.Timeout(time.Second),
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("bùm") }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, muốn 500", rec.Code)
	}
	if len(lines(t, buf)) != 1 {
		t.Errorf("Recover không thấy panic: %s", buf.String())
	}
}

func TestTimeout_KhongDuongThiBoQua(t *testing.T) {
	h := middleware.Timeout(0)(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// ---------- MaxBodySize ----------

func TestMaxBodySize(t *testing.T) {
	var readErr error
	h := middleware.MaxBodySize(10)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
		}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 100)))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if readErr == nil {
		t.Error("đọc body vượt giới hạn mà không lỗi")
	}
	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Errorf("lỗi = %v, muốn *http.MaxBytesError để Decode map được thành 400", readErr)
	}
}

func TestMaxBodySize_TrongGioiHan(t *testing.T) {
	var body []byte
	h := middleware.MaxBodySize(100)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) { body, _ = io.ReadAll(r.Body) }))

	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/", strings.NewReader("vừa đủ")))

	if string(body) != "vừa đủ" {
		t.Errorf("body = %q", body)
	}
}
