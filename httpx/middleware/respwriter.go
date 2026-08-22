package middleware

import (
	"bytes"
	"net/http"
)

// responseRecorder bọc http.ResponseWriter để ghi lại status, số byte đã ghi, và
// tuỳ chọn buffer một phần body.
//
// Dùng chung cho AccessLog, BodyLog và bất cứ middleware nào cần biết response đã
// đi ra là gì. Chỉ có một cài đặt để tất cả cùng thống nhất về những chỗ dễ sai:
// status mặc định khi handler không gọi WriteHeader, và Unwrap cho
// http.ResponseController.
type responseRecorder struct {
	http.ResponseWriter

	status      int
	bytesOut    int64
	wroteHeader bool

	// capture giữ phần đầu của body, chỉ khi maxCapture > 0.
	capture    *bytes.Buffer
	maxCapture int64
	truncated  bool
}

func newRecorder(w http.ResponseWriter, maxCapture int64) *responseRecorder {
	r := &responseRecorder{
		ResponseWriter: w,
		status:         http.StatusOK,
		maxCapture:     maxCapture,
	}
	if maxCapture > 0 {
		r.capture = &bytes.Buffer{}
	}
	return r
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		// net/http đã tự cảnh báo "superfluous WriteHeader"; ở đây giữ status đầu
		// tiên vì đó là status thật đi ra dây.
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	// Handler ghi body mà không gọi WriteHeader thì net/http hiểu là 200.
	r.wroteHeader = true

	n, err := r.ResponseWriter.Write(b)
	r.bytesOut += int64(n)

	if r.capture != nil {
		// Chỉ giữ tới maxCapture: một response trả file 50MB không được nằm trong
		// RAM chỉ để phục vụ việc ghi log.
		remain := r.maxCapture - int64(r.capture.Len())
		switch {
		case remain <= 0:
			r.truncated = true
		case int64(n) > remain:
			r.capture.Write(b[:remain])
			r.truncated = true
		default:
			r.capture.Write(b[:n])
		}
	}
	return n, err
}

// Unwrap cho http.ResponseController tìm tới ResponseWriter gốc, nhờ vậy Flush,
// Hijack và SetWriteDeadline vẫn dùng được qua middleware.
//
// Không có method này thì server-sent events và WebSocket upgrade sẽ vỡ khi có
// middleware ở giữa, và lỗi hiện ra rất khó truy.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// capturedBody trả về phần body đã buffer, cùng cờ cho biết có bị cắt không.
func (r *responseRecorder) capturedBody() (body []byte, truncated bool) {
	if r.capture == nil {
		return nil, false
	}
	return r.capture.Bytes(), r.truncated
}
