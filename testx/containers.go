//go:build integration

// Nhóm helper container nằm sau build tag `integration` để `go test ./...`
// thường không cần Docker, và để đồ hình phụ thuộc của bản build thường không
// kéo theo client Docker.
//
//	go test -tags=integration ./...

package testx

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Ảnh Docker dùng cho container test.
//
// Ghim tag cụ thể, không dùng `latest`: một test integration đổi hành vi vì ảnh
// upstream vừa ra bản mới là loại sự cố CI không ai muốn debug. Nâng version ở
// đây là một thay đổi có chủ ý và nhìn thấy được trong diff.
const (
	PostgresImage = "postgres:17-alpine"
	MySQLImage    = "mysql:8.4"
	RedisImage    = "redis:7.4-alpine"
	KafkaImage    = "confluentinc/confluent-local:7.6.1"
)

// containerTimeout là thời gian tối đa cho việc dựng một container.
//
// Lần chạy đầu phải tải ảnh về, nên khoảng này phải rộng. Nhưng vẫn phải có
// trần: không có nó thì một Docker treo sẽ làm CI chạy tới hết timeout của job.
const containerTimeout = 3 * time.Minute

// PostgresContainer dựng một Postgres dùng riêng cho test, trả về DSN.
//
// Container tự bị xoá khi test xong, qua TB.Cleanup. Mỗi lần gọi là một
// container mới — chậm hơn nhưng không có test nào thấy dữ liệu của test khác.
// Cần dùng chung thì gọi một lần trong TestMain và truyền DSN xuống.
func PostgresContainer(tb testing.TB) string {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerTimeout)
	defer cancel()

	c, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		// Chờ tới khi thật sự nhận connection. Postgres in ra "ready" một lần
		// trong lúc khởi tạo rồi restart, nên chỉ chờ log là chưa đủ — đây là
		// nguyên nhân kinh điển của test integration chập chờn.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(containerTimeout)),
	)
	registerTerminate(tb, c)
	if err != nil {
		tb.Fatalf("testx: dựng container Postgres: %v", err)
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		tb.Fatalf("testx: lấy DSN Postgres: %v", err)
	}
	return dsn
}

// MySQLContainer dựng một MySQL dùng riêng cho test, trả về DSN dạng
// go-sql-driver ("user:pass@tcp(host:port)/db?...").
func MySQLContainer(tb testing.TB) string {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerTimeout)
	defer cancel()

	c, err := tcmysql.Run(ctx, MySQLImage,
		tcmysql.WithDatabase("test"),
		tcmysql.WithUsername("test"),
		tcmysql.WithPassword("test"),
	)
	registerTerminate(tb, c)
	if err != nil {
		tb.Fatalf("testx: dựng container MySQL: %v", err)
	}

	dsn, err := c.ConnectionString(ctx, "parseTime=true")
	if err != nil {
		tb.Fatalf("testx: lấy DSN MySQL: %v", err)
	}
	return dsn
}

// RedisContainer dựng một Redis dùng riêng cho test, trả về địa chỉ "host:port".
//
// Trả địa chỉ chứ không phải URL vì cache.Config nhận Addrs — dán thẳng vào là
// xong, không phải parse lại.
func RedisContainer(tb testing.TB) string {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerTimeout)
	defer cancel()

	c, err := tcredis.Run(ctx, RedisImage)
	registerTerminate(tb, c)
	if err != nil {
		tb.Fatalf("testx: dựng container Redis: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatalf("testx: lấy host Redis: %v", err)
	}
	port, err := c.MappedPort(ctx, "6379/tcp")
	if err != nil {
		tb.Fatalf("testx: lấy port Redis: %v", err)
	}
	return host + ":" + port.Port()
}

// KafkaContainer dựng một Kafka (chế độ KRaft, không cần ZooKeeper) và trả về
// danh sách broker.
func KafkaContainer(tb testing.TB) []string {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerTimeout)
	defer cancel()

	c, err := tckafka.Run(ctx, KafkaImage, tckafka.WithClusterID("gokit-test"))
	registerTerminate(tb, c)
	if err != nil {
		tb.Fatalf("testx: dựng container Kafka: %v", err)
	}

	brokers, err := c.Brokers(ctx)
	if err != nil {
		tb.Fatalf("testx: lấy danh sách broker Kafka: %v", err)
	}
	return brokers
}

// terminator là phần duy nhất của container mà registerTerminate cần.
type terminator interface {
	Terminate(context.Context, ...testcontainers.TerminateOption) error
}

// isNil cho biết một giá trị interface có đang bọc con trỏ nil không.
//
// Cần kiểm bằng reflect chứ không phải `c == nil`: khi Run thất bại, nó trả về
// một *Container nil, và một con trỏ nil nhét vào interface thì **interface đó
// khác nil** — cái bẫy kinh điển của Go. So sánh thường sẽ cho qua, rồi lời gọi
// Terminate ngay sau đó panic và che mất lỗi thật (thường là "Docker chưa chạy").
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// registerTerminate đăng ký việc xoá container khi test xong.
//
// Gọi **trước** khi kiểm lỗi của Run: Run có thể trả về cả container lẫn lỗi khi
// nó dựng được container nhưng chờ readiness thất bại, và bỏ qua trường hợp đó
// sẽ để lại container mồ côi chạy mãi trên máy dev.
//
// Dùng context riêng để xoá: ctx của lúc dựng đã hết hạn từ lâu.
func registerTerminate(tb testing.TB, c terminator) {
	if isNil(c) {
		return
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := c.Terminate(ctx); err != nil {
			// Không Fatal: test đã chạy xong, và một container còn sót không làm
			// kết quả test sai. Nhưng phải nói ra để người ta biết mà dọn.
			tb.Logf("testx: xoá container thất bại: %v", err)
		}
	})
}
