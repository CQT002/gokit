package kafka

import (
	"context"

	"github.com/cqt002/gokit/core/tracectx"
)

// injectTrace chèn header traceparent vào message.
//
// Không ghi đè header đã có: chỗ gọi tự đặt traceparent nghĩa là họ đang nối
// tiếp một trace mà package này không nhìn thấy — ví dụ khi phát lại message từ
// DLQ và muốn giữ nguyên trace gốc.
//
// Không có trace trong ctx thì tạo một trace gốc mới, thay vì gửi đi header
// rỗng: một message không có trace là một message không lần ra được nguồn, và
// đó thường là message đáng lần ra nhất.
func injectTrace(ctx context.Context, m Message) Message {
	if _, ok := m.Headers[tracectx.HeaderTraceparent]; ok {
		return m
	}

	sc, ok := tracectx.FromContext(ctx)
	if !ok || !sc.Valid() {
		sc = tracectx.NewRoot()
	}

	tp := sc.Traceparent()
	if tp == "" {
		return m
	}

	// Copy map: Message do chỗ gọi cấp, và sửa map của họ là một tác dụng phụ
	// không ai chờ đợi — nhất là khi cùng một Message được gửi lại lần hai.
	headers := make(map[string]string, len(m.Headers)+1)
	for k, v := range m.Headers {
		headers[k] = v
	}
	headers[tracectx.HeaderTraceparent] = tp
	m.Headers = headers
	return m
}

// extractTrace đọc traceparent từ header của message và đặt span con vào ctx.
//
// Luôn tạo span **con**, không dùng lại span của producer: việc xử lý ở consumer
// là một chặng riêng trong cùng một trace, nên nó cần span ID của mình. Nhờ vậy
// log của producer và của consumer nối được với nhau bằng trace ID mà vẫn phân
// biệt được ai làm gì.
//
// Header thiếu hoặc sai định dạng thì bắt đầu một trace mới. Không phải lỗi của
// message: nhiều producer khác — của đội khác, hoặc viết bằng ngôn ngữ khác —
// đơn giản là không gửi header này.
func extractTrace(ctx context.Context, m Message) context.Context {
	var parent tracectx.SpanContext

	if tp := m.Headers[tracectx.HeaderTraceparent]; tp != "" {
		if sc, err := tracectx.ParseTraceparent(tp); err == nil {
			parent = sc
		}
	}

	// NewChild tự trả về trace gốc mới khi parent không hợp lệ.
	return tracectx.WithSpanContext(ctx, parent.NewChild())
}
