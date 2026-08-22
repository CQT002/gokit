// Package testx cung cấp helper cho test của service viết trên gokit.
//
// Module này tồn tại vì một lý do thực dụng: nếu người ta không test nổi service
// viết trên gokit thì họ sẽ không dùng gokit. Bốn nhóm helper:
//
//   - [CaptureLogs] — kiểm tra service đã ghi đúng dòng log nào. Log là hợp đồng
//     với người vận hành, và một hợp đồng không có test thì sẽ vỡ.
//   - [Golden] — so kết quả với file mẫu trong testdata. Dùng cho output dài
//     (JSON response, SQL sinh ra) mà viết assert tay là không đọc được.
//   - [LoadFixture] — nạp dữ liệu test từ file JSON hoặc YAML.
//   - [FreezeTime] — nguồn thời gian điều khiển được, không phải sleep.
//
// Nhóm container — PostgresContainer, MySQLContainer, RedisContainer,
// KafkaContainer — nằm sau build tag `integration` nên `go test ./...` bình
// thường **không** cần Docker (và bản build thường cũng không kéo theo client
// Docker):
//
//	go test -tags=integration ./...
//
// Mọi helper nhận testing.TB thay vì *testing.T, nên dùng được cả trong
// benchmark và trong helper của chính test. Chúng tự đăng ký dọn dẹp bằng
// TB.Cleanup, nên không có gì phải đóng bằng tay.
package testx
