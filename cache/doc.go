// Package cache cung cấp client Redis dùng chung, cache-aside chống stampede,
// và các interface hẹp để consumer khai đúng phần mình cần.
//
// Các package con:
//
//   - [github.com/cqt002/gokit/cache/lock]      distributed lock, mất lock thì cancel context
//   - [github.com/cqt002/gokit/cache/leader]    leader election
//   - [github.com/cqt002/gokit/cache/cron]      cron chạy trên đúng một instance
//   - [github.com/cqt002/gokit/cache/idemstore] store Redis cho httpx/idempotency
//
// Hai quyết định định hình package này:
//
// **Một type cho cả standalone và cluster.** redis.UniversalClient của go-redis
// đã trừu tượng hoá điều đó, nên không có redis.go và redis_cluster.go gần trùng
// nhau — một địa chỉ là standalone, nhiều địa chỉ là cluster.
//
// **Nhiều interface hẹp, không một interface to.** [KV], [Hash], [PubSub],
// [Stream], [Pipeline] tách riêng; [Client] cài đặt tất cả. Một repository chỉ
// đọc/ghi key thì khai phụ thuộc vào [KV] và mock được bằng năm dòng, thay vì
// phải mock ba mươi method nó không dùng.
package cache
