package kafka

import (
	"context"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
)

// kgoLogger nối logger nội bộ của franz-go vào slog.
//
// Không có nó thì client Kafka im lặng hoàn toàn, và những chuyện quan trọng —
// mất kết nối tới broker, rebalance, lỗi metadata — không xuất hiện ở đâu cả.
type kgoLogger struct {
	log *slog.Logger
}

func newKgoLogger(l *slog.Logger) kgo.Logger {
	if l == nil {
		l = slog.Default()
	}
	return &kgoLogger{log: l}
}

// Level cho franz-go biết có nên dựng thông điệp log hay không.
//
// Trả Info khi slog đang mở tới Debug, còn lại trả Warn. Mức Info của franz-go
// ghi mỗi lần metadata cập nhật và mỗi lần rebalance — hữu ích lúc điều tra,
// nhưng ở production thì đó là nhiễu, và tệ hơn là chi phí dựng chuỗi cho những
// dòng sẽ bị bỏ ngay sau đó.
func (k *kgoLogger) Level() kgo.LogLevel {
	if k.log.Enabled(context.Background(), slog.LevelDebug) {
		return kgo.LogLevelInfo
	}
	return kgo.LogLevelWarn
}

// Log cài đặt kgo.Logger.
func (k *kgoLogger) Log(level kgo.LogLevel, msg string, keyvals ...any) {
	// keyvals của franz-go luôn là các cặp key (string) và value, đúng dạng
	// slog nhận — nên không phải chuyển đổi gì.
	k.log.Log(context.Background(), slogLevel(level), msg, keyvals...)
}

// slogLevel đổi mức log của franz-go sang mức của slog.
//
// Info của franz-go hạ xuống Debug: đó là log về hoạt động nội bộ của client
// (metadata, rebalance), không phải sự kiện của service.
func slogLevel(l kgo.LogLevel) slog.Level {
	switch l {
	case kgo.LogLevelError:
		return slog.LevelError
	case kgo.LogLevelWarn:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}
