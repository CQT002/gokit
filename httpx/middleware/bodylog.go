package middleware

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/cqt002/gokit/core/log"
)

// Giá trị mặc định của BodyLogOptions.
const (
	// DefaultMaxCapture là trần buffer cho body, mỗi chiều.
	DefaultMaxCapture int64 = 64 << 10
)

// BodyLogOptions cấu hình BodyLog.
type BodyLogOptions struct {
	// MaxCapture là số byte tối đa buffer cho mỗi chiều. <= 0 dùng DefaultMaxCapture.
	//
	// Đây là trần bộ nhớ, không phải trần log: một endpoint trả file 50MB không
	// được giữ cả file trong RAM chỉ để ghi log.
	MaxCapture int64

	// Mask cấu hình che cho lớp 1 và lớp 3 khi phải dùng bản raw.
	Mask log.MaskConfig

	// SkipPaths là path không ghi body, thường là /healthz, /readyz, /metrics.
	SkipPaths []string

	// SkipContentTypes là content type không ghi nội dung.
	// nil thì dùng DefaultBinaryContentTypes.
	SkipContentTypes []string
}

// DefaultBinaryContentTypes là các content type chỉ log content-type và số byte,
// không log nội dung.
//
// Nội dung nhị phân trong log vừa vô nghĩa với người đọc vừa tốn dung lượng gấp
// nhiều lần vì phải escape.
var DefaultBinaryContentTypes = []string{
	"image/", "video/", "audio/", "application/pdf",
	"application/octet-stream", "application/zip", "application/gzip",
}

// bodyCapture là holder gắn vào context để handler đăng ký bản body đã mask.
//
// Đây là tầng "chất lượng" của thiết kế hai tầng: middleware bảo đảm luôn có log
// (dùng lớp 1 và lớp 3 trên raw bytes), còn Decode và OK/Fail đăng ký bản đã mask
// theo tag (lớp 2) vào đây. Cuối request, bản đã đăng ký được ưu tiên.
type bodyCapture struct {
	mu       sync.Mutex
	request  any
	response any
	hasReq   bool
	hasResp  bool
}

type captureKey struct{}

// withCapture gắn holder vào context.
func withCapture(ctx context.Context) (context.Context, *bodyCapture) {
	c := &bodyCapture{}
	return context.WithValue(ctx, captureKey{}, c), c
}

func captureFrom(ctx context.Context) *bodyCapture {
	c, _ := ctx.Value(captureKey{}).(*bodyCapture)
	return c
}

// SetRequestBody đăng ký bản request body đã mask theo tag `log:`.
//
// httpx.Decode gọi hàm này sau khi parse thành công. Bản đăng ký ở đây thắng bản
// mask từ raw bytes của middleware, vì nó biết type nên áp được tag trên từng field.
//
// Không có BodyLog trong chain thì hàm này không làm gì — gọi thoải mái.
func SetRequestBody(ctx context.Context, masked any) {
	if c := captureFrom(ctx); c != nil {
		c.mu.Lock()
		c.request, c.hasReq = masked, true
		c.mu.Unlock()
	}
}

// SetResponseBody đăng ký bản response body đã mask theo tag `log:`.
//
// httpx.OK, httpx.Created và httpx.Fail gọi hàm này.
func SetResponseBody(ctx context.Context, masked any) {
	if c := captureFrom(ctx); c != nil {
		c.mu.Lock()
		c.response, c.hasResp = masked, true
		c.mu.Unlock()
	}
}

func (c *bodyCapture) registered() (req, resp any, hasReq, hasResp bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request, c.response, c.hasReq, c.hasResp
}

// BodyLog ghi body của request và response cho **mọi** request.
//
// Đây là yêu cầu vận hành đã chốt ở plan: phải tra được mọi request khi cần. Bù lại
// rủi ro dung lượng và rủi ro lộ dữ liệu bằng ba lớp masking của core/log cộng trần
// MaxCapture ở đây.
//
// # Thiết kế hai tầng
//
// Masking tốt nhất (lớp 2, theo tag `log:`) cần biết type, mà middleware chạy trước
// khi handler decode nên chưa biết type. Nên:
//
//	Tầng bảo đảm    (middleware này):    luôn ghi log, dùng lớp 1 + lớp 3 trên raw bytes
//	Tầng chất lượng (Decode, OK, Fail):  đăng ký bản đã mask theo tag vào context
//
// Cuối request, bản đã đăng ký được ưu tiên; không có thì fallback về SafeMap của
// raw bytes. Kết quả: không bao giờ mất log, và có type thì log đẹp hơn.
//
// Cách này cũng bỏ được một lần parse JSON so với việc middleware tự parse để mask
// rồi handler parse lại lần nữa.
func BodyLog(l *slog.Logger, opts BodyLogOptions) Middleware {
	maxCapture := opts.MaxCapture
	if maxCapture <= 0 {
		maxCapture = DefaultMaxCapture
	}
	binaryTypes := opts.SkipContentTypes
	if binaryTypes == nil {
		binaryTypes = DefaultBinaryContentTypes
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slices.Contains(opts.SkipPaths, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			rawReq, reqTruncated := drainRequest(r, maxCapture)

			ctx, capture := withCapture(r.Context())
			r = r.WithContext(ctx)

			rec := newRecorder(w, maxCapture)
			next.ServeHTTP(rec, r)

			rawResp, respTruncated := rec.capturedBody()
			regReq, regResp, hasReq, hasResp := capture.registered()

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
			}

			if a, ok := bodyAttr("request", r.Header.Get("Content-Type"), rawReq,
				reqTruncated, regReq, hasReq, binaryTypes, opts.Mask, r); ok {
				attrs = append(attrs, a)
			}
			if a, ok := bodyAttr("response", rec.Header().Get("Content-Type"), rawResp,
				respTruncated, regResp, hasResp, binaryTypes, opts.Mask, nil); ok {
				attrs = append(attrs, a)
			}

			l.LogAttrs(ctx, slog.LevelInfo, "body", attrs...)
		})
	}
}

// bodyAttr dựng attribute cho một chiều body.
//
// Thứ tự quyết định: bản đã đăng ký (lớp 2) → multipart metadata → nội dung nhị
// phân chỉ ghi kích thước → JSON qua SafeMap (lớp 3 + lớp 1) → chuỗi thô (lớp 1).
func bodyAttr(
	key, contentType string, raw []byte, truncated bool,
	registered any, hasRegistered bool,
	binaryTypes []string, mask log.MaskConfig, r *http.Request,
) (slog.Attr, bool) {
	if hasRegistered {
		// Đã có bản mask theo tag: dùng luôn, không cần chạm vào raw.
		return slog.Any(key, log.Safe(registered)), true
	}

	mediaType := mediaTypeOf(contentType)

	if strings.HasPrefix(mediaType, "multipart/") {
		return slog.Any(key, multipartAttr(r, len(raw))), true
	}
	if isBinary(mediaType, binaryTypes) {
		return slog.Any(key, slog.GroupValue(
			slog.String("content_type", mediaType),
			slog.Int("bytes", len(raw)),
		)), true
	}
	if len(raw) == 0 {
		return slog.Attr{}, false
	}

	value := log.Safe(jsonOrText(raw, mask))
	if !truncated {
		return slog.Any(key, value), true
	}
	// Nói rõ là đã bị cắt: một JSON thiếu nửa cuối trông y như một JSON hỏng, và
	// người đọc log sẽ đi tìm bug không tồn tại.
	return slog.Any(key, slog.GroupValue(
		slog.Any("value", value),
		slog.Bool("truncated", true),
		slog.Int("captured_bytes", len(raw)),
	)), true
}

// jsonOrText mask body: parse được JSON object thì mask theo tên field, không thì
// coi là chuỗi thô và để lớp 1 xử lý.
func jsonOrText(raw []byte, mask log.MaskConfig) any {
	if m, ok := decodeJSONObject(raw); ok {
		return log.SafeMap(m, mask)
	}
	return string(raw)
}

// multipartAttr ghi metadata của form multipart.
//
// Chỉ đọc từ r.MultipartForm — tức là chỉ có dữ liệu khi handler đã gọi
// ParseMultipartForm. Cố tình không tự parse: parse ở middleware nghĩa là đọc hết
// body vào bộ nhớ hoặc đĩa tạm cho **mọi** request upload, chỉ để ghi log.
//
// Upload file là chỗ hay lỗi nhất mà lại thường không có log nào, nên phần metadata
// này đáng có: tên field, tên file, content type, kích thước.
func multipartAttr(r *http.Request, rawLen int) slog.Value {
	if r == nil || r.MultipartForm == nil {
		return slog.GroupValue(
			slog.String("content_type", mediaTypeOf(headerOf(r, "Content-Type"))),
			slog.Int("bytes", rawLen),
			slog.String("note", "handler chưa gọi ParseMultipartForm nên không có metadata"),
		)
	}

	parts := make([]any, 0, len(r.MultipartForm.File)+len(r.MultipartForm.Value))
	for field, headers := range r.MultipartForm.File {
		for _, h := range headers {
			parts = append(parts, map[string]any{
				"field":        field,
				"filename":     h.Filename,
				"content_type": h.Header.Get("Content-Type"),
				"bytes":        h.Size,
			})
		}
	}
	for field, values := range r.MultipartForm.Value {
		for _, v := range values {
			parts = append(parts, map[string]any{"field": field, "value": v})
		}
	}
	return slog.AnyValue(parts)
}

func headerOf(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return r.Header.Get(name)
}

// drainRequest đọc tối đa maxCapture byte từ body rồi trả body lại nguyên vẹn cho
// handler.
func drainRequest(r *http.Request, maxCapture int64) (raw []byte, truncated bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, false
	}

	// Đọc thêm 1 byte để phân biệt "vừa đủ maxCapture" với "dài hơn maxCapture".
	buf := make([]byte, maxCapture+1)
	n, _ := io.ReadFull(r.Body, buf)
	buf = buf[:n]

	if int64(n) > maxCapture {
		truncated = true
		raw = buf[:maxCapture]
	} else {
		raw = buf
	}

	// Ghép phần đã đọc lại trước phần còn lại: handler phải nhận được body đầy đủ,
	// kể cả phần vượt maxCapture mà ta không giữ để log.
	r.Body = &rewoundBody{
		Reader: io.MultiReader(bytes.NewReader(buf), r.Body),
		closer: r.Body,
	}
	return raw, truncated
}

// rewoundBody ghép phần body đã đọc với phần còn lại, và vẫn đóng được body gốc.
type rewoundBody struct {
	io.Reader
	closer io.Closer
}

func (b *rewoundBody) Close() error { return b.closer.Close() }

func mediaTypeOf(contentType string) string {
	if contentType == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Content-Type rác: trả về phần trước dấu chấm phẩy để vẫn có nhãn dùng được.
		mt, _, _ = strings.Cut(contentType, ";")
		return strings.TrimSpace(strings.ToLower(mt))
	}
	return mt
}

func isBinary(mediaType string, binaryTypes []string) bool {
	if mediaType == "" {
		return false
	}
	for _, prefix := range binaryTypes {
		if strings.HasPrefix(mediaType, prefix) {
			return true
		}
	}
	return false
}
