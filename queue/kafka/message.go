package kafka

import (
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Message là một bản ghi Kafka ở dạng của package này.
//
// Bốn field cuối chỉ có nghĩa ở phía consumer: producer để trống chúng và
// broker tự điền.
type Message struct {
	// Topic là tên topic. Bắt buộc khi gửi.
	Topic string

	// Key quyết định partition: cùng Key thì cùng partition, nên cùng thứ tự.
	//
	// Rỗng nghĩa là để client rải đều — chỉ dùng được khi thứ tự giữa các
	// message không quan trọng. Với dữ liệu theo thực thể (đơn hàng, tài khoản)
	// thì Key phải là ID của thực thể đó, nếu không hai sự kiện của cùng một
	// đơn hàng có thể được xử lý sai thứ tự.
	Key string

	// Value là nội dung message.
	Value []byte

	// Headers là metadata đi kèm. Producer tự thêm `traceparent` vào đây khi
	// bật [ProducerConfig.PropagateTrace].
	Headers map[string]string

	// Partition là partition chứa message. Chỉ có nghĩa ở phía consumer.
	Partition int32

	// Offset là vị trí trong partition. Chỉ có nghĩa ở phía consumer.
	Offset int64

	// Timestamp là thời điểm của message. Chỉ có nghĩa ở phía consumer.
	Timestamp time.Time
}

// toRecord đổi Message thành bản ghi của franz-go.
func (m Message) toRecord() *kgo.Record {
	rec := &kgo.Record{
		Topic: m.Topic,
		Value: m.Value,
	}
	if m.Key != "" {
		rec.Key = []byte(m.Key)
	}
	rec.Headers = toHeaders(m.Headers)
	return rec
}

// fromRecord đổi bản ghi của franz-go thành Message.
func fromRecord(rec *kgo.Record) Message {
	return Message{
		Topic:     rec.Topic,
		Key:       string(rec.Key),
		Value:     rec.Value,
		Headers:   fromHeaders(rec.Headers),
		Partition: rec.Partition,
		Offset:    rec.Offset,
		Timestamp: rec.Timestamp,
	}
}

// toHeaders đổi map thành slice header của Kafka.
//
// Kafka cho phép trùng key trong header; map thì không. Đánh đổi có ý thức:
// header trùng key hầu như không được dùng, còn map thì đọc và ghi dễ hơn hẳn
// một slice cặp key/value.
func toHeaders(m map[string]string) []kgo.RecordHeader {
	if len(m) == 0 {
		return nil
	}
	out := make([]kgo.RecordHeader, 0, len(m))
	for k, v := range m {
		out = append(out, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	return out
}

// fromHeaders đổi slice header của Kafka thành map.
//
// Key trùng nhau thì giá trị cuối thắng.
func fromHeaders(hs []kgo.RecordHeader) map[string]string {
	if len(hs) == 0 {
		return nil
	}
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		out[h.Key] = string(h.Value)
	}
	return out
}
