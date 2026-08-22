// Package middleware cung cấp middleware HTTP theo đúng chuẩn net/http.
//
// Mọi middleware ở đây có kiểu func(http.Handler) http.Handler — chuẩn của stdlib,
// nên dùng được với chi, gorilla, echo (qua adapter), hay ServeMux thuần. Package
// này không phụ thuộc router nào: một router tiêu thụ được middleware stdlib, còn
// middleware của một router cụ thể thì không đi đâu được.
//
// # Thứ tự lồng middleware
//
// Chain(A, B, C) làm A chạy ngoài cùng. Thứ tự nên dùng, từ ngoài vào trong:
//
//	Recover      — phải ngoài cùng để bắt được panic của mọi tầng bên trong
//	Trace        — sinh trace ID trước, để mọi log sau đó có nó
//	AccessLog    — cần trace ID, và cần đo được cả thời gian của các tầng trong
//	Metrics      — cùng lý do
//	CORS         — trả preflight sớm, không cần đi sâu hơn
//	RateLimit    — chặn trước khi tốn công đọc body
//	MaxBodySize  — trước khi có ai đọc body
//	BodyLog      — sau MaxBodySize để không buffer body quá lớn
//	Timeout      — trong cùng, sát handler
package middleware

import "net/http"

// Middleware là một tầng bọc quanh http.Handler.
//
// Khai bằng type alias (dấu =) chứ không phải type mới: nhờ vậy một hàm trả về
// func(http.Handler) http.Handler ở package khác dùng được ngay ở đây và ngược
// lại, không cần chuyển đổi.
type Middleware = func(http.Handler) http.Handler

// Chain gộp nhiều middleware thành một.
//
// Middleware đầu tiên nằm ngoài cùng, tức là nó thấy request trước và thấy
// response sau cùng:
//
//	handler = Chain(Recover(l), Trace(o), AccessLog(l, o))(mux)
//
// tương đương Recover(Trace(AccessLog(mux))).
func Chain(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		// Bọc từ phải sang trái để phần tử đầu tiên thành tầng ngoài cùng.
		for i := len(mws) - 1; i >= 0; i-- {
			if mws[i] != nil {
				next = mws[i](next)
			}
		}
		return next
	}
}
