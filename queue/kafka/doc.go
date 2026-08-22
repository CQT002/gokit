// Package kafka cung cấp producer và consumer trên franz-go, có propagate trace,
// retry và DLQ.
//
// Ba quyết định định hình package này:
//
// **Consumer nhận handler, không phát ra channel.** Channel không cho biết
// message đã xử lý xong hay chưa, nên việc commit offset tách rời khỏi việc xử
// lý — và mất message khi restart. Handler trả về error thì commit mới có nghĩa:
// chỉ commit khi handler trả nil.
//
// **Có retry và DLQ.** Không có chúng thì một message hỏng hoặc bị bỏ trong im
// lặng, hoặc kẹt vòng lặp vô hạn và chặn cả partition.
//
// **Xử lý song song theo partition, tuần tự trong một partition.** Kafka chỉ đảm
// bảo thứ tự **trong** một partition; một worker pool bốc message từ chung một
// hàng đợi sẽ phá đúng cái đảm bảo đó. Xem [ConsumerConfig.Concurrency].
//
// Trace đi xuyên qua broker bằng header `traceparent` — cùng cơ chế và cùng tên
// header như phía HTTP, nên một request đi qua service → Kafka → service khác
// vẫn chung một trace ID trong log.
//
// Module này **không** phụ thuộc obs: metric của client do plugin kprom của
// franz-go sinh, còn metric về kết quả xử lý thì package này tự đăng ký. Cả hai
// nhận thẳng một *prometheus.Registry.
package kafka
