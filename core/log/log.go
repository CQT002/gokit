// Package log dựng *slog.Logger có sẵn masking và tự đính trace từ context.
//
// Trả về type của stdlib (*slog.Logger) chứ không phải interface tự định nghĩa:
// service dùng gokit không bị khoá vào thư viện này, và mọi thư viện khác nhận
// *slog.Logger đều cắm vào được.
//
// # Vì sao có masking ba lớp
//
// gokit log đầy đủ body request và response cho mọi request, mọi status code —
// đây là quyết định vận hành, để lúc cần tra thì tra được. Nó tạo ra hai rủi ro
// khác nhau, và chúng cần hai luật khác nhau:
//
//  1. Dữ liệu nhạy cảm lọt vào log. Phải che bất kể dài ngắn.
//  2. Dung lượng. Một field base64 ảnh vài MB làm phình chi phí, chậm pipeline,
//     và có thể vượt giới hạn kích thước dòng của Loki hay CloudWatch khiến mất
//     trắng cả dòng log đó.
//
// Ba lớp che, theo thứ tự ưu tiên khi cùng áp vào một giá trị:
//
//   - Lớp 2 — tag `log:` trên struct field. Cơ chế chính, xem Safe.
//   - Lớp 3 — danh sách tên field trong MaskConfig.Fields. Fallback khi payload
//     không có struct, xem SafeMap.
//   - Lớp 1 — mọi chuỗi dài hơn MaxConfig.MaxLen bị rút gọn thành metadata. Đây
//     là lưới an toàn: nó bắt cả field chưa ai nghĩ tới, nên sprint sau thêm
//     signature_base64 thì vẫn tự động an toàn. Lớp này không có ngoại lệ, kể cả
//     với field đã khai tag.
//
// Và một chốt cuối: nếu dòng log vẫn vượt MaxLineBytes sau khi che, attribute lớn
// nhất bị bỏ và thay bằng marker _dropped.
package log

import (
	"io"
	"log/slog"
	"os"
)

// Format là định dạng dòng log.
type Format string

// Các định dạng được hỗ trợ.
const (
	// FormatJSON là mặc định — thứ mà pipeline log nào cũng parse được.
	FormatJSON Format = "json"
	// FormatText dễ đọc bằng mắt, dùng khi dev ở máy cá nhân.
	FormatText Format = "text"
)

// Options cấu hình logger.
//
// Giá trị zero dùng được ngay: mức Info, JSON, ra stdout, masking mặc định.
type Options struct {
	// Level là mức thấp nhất được ghi. Zero là slog.LevelInfo.
	Level slog.Level
	// Format mặc định FormatJSON.
	Format Format
	// Output mặc định os.Stdout.
	Output io.Writer
	// AppName nếu khác rỗng sẽ thành attribute "app" trên mọi dòng log.
	AppName string
	// AddSource đính file và dòng gọi. Có chi phí, nên thường chỉ bật ở mức debug.
	AddSource bool
	// Mask cấu hình lớp 1 và lớp 3 cùng trần MaxLineBytes.
	Mask MaskConfig
}

// New dựng logger với chain handler: mask → context → json/text.
//
// Thứ tự này là bắt buộc. MaskHandler phải ngoài cùng để thấy attribute ở dạng
// nguyên bản; handler serialize phải trong cùng vì sau nó thì dòng log đã thành
// byte, không sửa được nữa.
func New(opts Options) *slog.Logger {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     opts.Level,
		AddSource: opts.AddSource,
	}

	var base slog.Handler
	switch opts.Format {
	case FormatText:
		base = slog.NewTextHandler(out, handlerOpts)
	case FormatJSON:
		base = slog.NewJSONHandler(out, handlerOpts)
	default:
		base = slog.NewJSONHandler(out, handlerOpts)
	}

	logger := slog.New(NewMaskHandler(NewContextHandler(base), opts.Mask))
	if opts.AppName != "" {
		logger = logger.With(slog.String("app", opts.AppName))
	}
	return logger
}
