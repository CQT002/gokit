package log

// SafeMap che một payload dạng map theo cấu hình cfg.
//
// Đây là lớp 3 — dùng khi không có struct để gắn tag: proxy payload của đối tác,
// webhook, endpoint nhận JSON động. Luật khớp theo **tên field ở mọi độ sâu**, kể
// cả trong slice và trong map lồng map.
//
// Trả về map mới, không sửa m. Giá trị nào tự cài slog.LogValuer (như
// core/secret.Secret) đều được tôn trọng, nên bí mật không lọt ra kể cả khi tên
// field không nằm trong danh sách.
//
// cfg giá trị zero dùng được ngay: MaxLen 256, danh sách field mặc định ở
// DefaultMaskFields.
func SafeMap(m map[string]any, cfg MaskConfig) map[string]any {
	if m == nil {
		return nil
	}
	val, _ := maskValue(m, cfg.normalize(), 0)
	out, ok := toAny(val).(map[string]any)
	if !ok {
		// Không xảy ra: maskValue trên map luôn trả về group. Nhưng trả map rỗng
		// còn hơn trả nil và để chỗ gọi log ra giá trị chưa che.
		return map[string]any{}
	}
	return out
}
