// Package httpx cung cấp phần glue để viết HTTP API bằng net/http thuần: định
// dạng response thống nhất, decode và validate request, map error sang HTTP status,
// server có graceful shutdown, và HTTP client có retry.
//
// Không phụ thuộc router nào. Middleware nằm ở httpx/middleware và có kiểu
// func(http.Handler) http.Handler, nên dùng được với chi, gorilla, ServeMux thuần
// hay bất cứ thứ gì tiêu thụ được http.Handler.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/core/tracectx"
	"github.com/cqt002/gokit/httpx/middleware"
)

// Trạng thái trong Envelope.Status.
const (
	// StatusAccept là request đã được xử lý thành công.
	StatusAccept = "ACCEPT"
	// StatusReject là request bị từ chối, dù do client hay do server.
	StatusReject = "REJECT"
	// StatusProcessing là request đã nhận và đang xử lý bất đồng bộ.
	StatusProcessing = "PROCESSING"
)

// Field là chi tiết lỗi gắn với một field của dữ liệu vào.
type Field struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Envelope là hình dạng chung của mọi response JSON.
//
// Một hình dạng duy nhất cho cả thành công và thất bại: client viết một hàm parse
// dùng cho mọi endpoint, và không phải đoán xem lần này server trả object gì.
type Envelope struct {
	// TraceID để người dùng báo lỗi kèm ID này là tra được ngay toàn bộ log.
	TraceID string `json:"trace_id,omitempty"`
	// Status là ACCEPT, REJECT hoặc PROCESSING.
	Status string `json:"status"`
	// Code là mã lỗi phân loại được, lấy từ errs.Code.
	Code string `json:"code,omitempty"`
	// Message là thông báo an toàn để hiển thị cho người dùng.
	Message string `json:"message,omitempty"`
	// Fields là chi tiết lỗi theo từng field, dùng cho lỗi validate.
	Fields []Field `json:"fields,omitempty"`
	// Data là dữ liệu trả về khi thành công.
	Data any `json:"data,omitempty"`
	// ElapsedMs là thời gian xử lý, chỉ có khi middleware đã ghi mốc bắt đầu.
	ElapsedMs int64 `json:"elapsed_ms,omitempty"`
}

// JSON ghi body dưới dạng JSON với status cho trước.
//
// Đặt Content-Type trước khi ghi status — sau khi status đã đi ra dây thì header
// không đổi được nữa, và đó là lỗi rất dễ mắc.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// Không thể sửa response nữa: status và một phần body đã gửi. Chỉ còn cách
		// để lại dấu vết cho người vận hành.
		slog.Default().Error("httpx: không encode được response", slog.Any("error", err))
	}
}

// OK trả 200 kèm data.
func OK(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, r, http.StatusOK, Envelope{
		Status: StatusAccept,
		Data:   data,
	})
}

// Created trả 201 kèm data.
func Created(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, r, http.StatusCreated, Envelope{
		Status: StatusAccept,
		Data:   data,
	})
}

// Accepted trả 202 cho việc xử lý bất đồng bộ.
func Accepted(w http.ResponseWriter, r *http.Request, data any) {
	writeEnvelope(w, r, http.StatusAccepted, Envelope{
		Status: StatusProcessing,
		Data:   data,
	})
}

// NoContent trả 204 không có body.
func NoContent(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// Fail map err sang HTTP status và Envelope.
//
// Cách map:
//
//   - *errs.Error: dùng Code, Message, Fields, HTTPStatus của nó. Đây là đường
//     bình thường — tầng nghiệp vụ trả errs.New(errs.CodeNotFound, ...) và không
//     cần biết gì về HTTP.
//   - error thường: 500 với message chung. **Không** đưa err.Error() ra client:
//     nội dung đó thường chứa tên host, câu SQL, đường dẫn file. Chi tiết thật đi
//     vào log qua AccessLog và BodyLog.
//
// err là nil thì trả 500 — gọi Fail mà không có lỗi là bug ở chỗ gọi, và im lặng
// trả 200 sẽ che mất bug đó.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := errs.As(err)
	if !ok {
		writeEnvelope(w, r, http.StatusInternalServerError, Envelope{
			Status:  StatusReject,
			Code:    string(errs.CodeInternal),
			Message: "đã có lỗi xảy ra, vui lòng thử lại",
		})
		return
	}

	writeEnvelope(w, r, e.HTTPStatus, Envelope{
		Status:  StatusReject,
		Code:    string(e.Code),
		Message: e.Message,
		Fields:  fieldsFrom(e.Fields),
		Data:    e.Data,
	})
}

func fieldsFrom(in []errs.Field) []Field {
	if len(in) == 0 {
		return nil
	}
	out := make([]Field, len(in))
	for i, f := range in {
		out[i] = Field{Field: f.Field, Message: f.Message}
	}
	return out
}

// writeEnvelope điền trace ID, thời gian xử lý, đăng ký body cho log, rồi ghi ra.
func writeEnvelope(w http.ResponseWriter, r *http.Request, status int, env Envelope) {
	ctx := r.Context()

	env.TraceID = tracectx.TraceIDFrom(ctx)
	if start, ok := startFrom(ctx); ok {
		env.ElapsedMs = time.Since(start).Milliseconds()
	}

	// Đăng ký bản response đã biết type để BodyLog áp được tag `log:` — tầng chất
	// lượng của thiết kế hai tầng ở middleware.BodyLog.
	middleware.SetResponseBody(ctx, env)

	JSON(w, status, env)
}

type startKey struct{}

// WithStart gắn mốc bắt đầu xử lý vào context, để Envelope điền được ElapsedMs.
//
// Middleware nào cũng gọi được; nếu không gọi thì ElapsedMs bị bỏ khỏi response.
func WithStart(r *http.Request, t time.Time) *http.Request {
	return r.WithContext(contextWithStart(r.Context(), t))
}
