// Package timex cung cấp layout thời gian dùng chung và vài helper mà stdlib
// không có sẵn.
//
// Package này cố tình giữ nhỏ. Không có interface TimeHelper, không có wrapper
// quanh time.Now(): bọc time.Now().In(loc) lại không tạo ra giá trị nào, chỉ thêm
// một tầng phải đọc. Muốn test được thời gian thì truyền time.Time vào hàm, hoặc
// truyền một func() time.Time — đừng bọc cả package time.
package timex

import "time"

// Các layout hay dùng, đặt tên theo ý nghĩa thay vì để mỗi service tự viết lại
// chuỗi "2006-01-02" và gõ sai một lần trong mười lần.
const (
	// DateISO là ngày theo ISO 8601: 2026-08-21.
	DateISO = "2006-01-02"
	// DateCompact là ngày không dấu phân cách: 20260821. Hay gặp trong tên file
	// và mã tham chiếu của hệ thống liên ngân hàng.
	DateCompact = "20060102"
	// RFC3339Milli là RFC 3339 với đúng 3 chữ số phần nghìn giây.
	//
	// Khác time.RFC3339Nano ở chỗ số chữ số là cố định: dùng "000" thay vì "999"
	// nên phần nghìn giây không bị cắt khi bằng 0, và chuỗi sinh ra sắp xếp được
	// theo thứ tự thời gian bằng cách so sánh chuỗi.
	RFC3339Milli = "2006-01-02T15:04:05.000Z07:00"
)

// StartOfDay trả về 00:00:00.000000000 của cùng ngày, giữ nguyên location của t.
//
// Giữ location là điều quan trọng: "đầu ngày" của một giao dịch phụ thuộc múi giờ
// nghiệp vụ, và đổi sang UTC ở đây sẽ lệch ngày cho những giờ sát nửa đêm.
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// EndOfDay trả về 23:59:59.999999999 của cùng ngày, giữ nguyên location của t.
//
// Dùng nanosecond cuối cùng chứ không phải 00:00:00 của ngày hôm sau, để so sánh
// dạng `t <= EndOfDay(x)` không vô tình bao gồm cả đầu ngày kế tiếp.
func EndOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 23, 59, 59, int(time.Second-time.Nanosecond), t.Location())
}

// ParseInLoc parse value theo layout, hiểu chuỗi là giờ của loc.
//
// time.Parse coi chuỗi không có thông tin múi giờ là UTC, nên "2026-08-21" parse
// bằng time.Parse rồi đem so sánh với giờ Việt Nam sẽ lệch 7 tiếng. Hàm này nhận
// location tường minh để chỗ gọi không thể quên.
//
// loc là nil thì dùng time.UTC.
func ParseInLoc(layout, value string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	return time.ParseInLocation(layout, value, loc)
}
