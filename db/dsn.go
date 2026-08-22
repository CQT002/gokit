package db

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/cqt002/gokit/core/tlsx"
)

// openSQL dựng *sql.DB cho driver tương ứng.
//
// Việc tự dựng *sql.DB thay vì truyền DSN cho driver của GORM có một lý do cụ
// thể: TLS. Cả hai driver chỉ nhận cấu hình TLS đầy đủ qua struct Go, còn qua
// DSN thì chỉ có sslmode (Postgres) hoặc một tên trong registry toàn cục
// (MySQL). Dựng ở đây nên tlsx.Options dùng được nguyên vẹn, và mật khẩu không
// phải đi qua bước nối chuỗi nào — chỗ mà password chứa dấu cách hay dấu nháy
// sẽ hỏng theo cách rất khó tìm.
func openSQL(cfg Config) (*sql.DB, error) {
	tlsCfg, err := clientTLS(cfg.TLS)
	if err != nil {
		return nil, err
	}

	switch cfg.Driver {
	case Postgres:
		return openPostgres(cfg, tlsCfg)
	case MySQL:
		return openMySQL(cfg, tlsCfg)
	default:
		// validate đã chặn từ trước; nhánh này chỉ để switch là toàn phần.
		return nil, fmt.Errorf("db: Driver %q không được hỗ trợ", cfg.Driver)
	}
}

// clientTLS dựng *tls.Config, hoặc nil khi cfg không khai gì về TLS.
func clientTLS(o tlsx.Options) (*tls.Config, error) {
	if !hasTLS(o) {
		return nil, nil
	}
	c, err := tlsx.ClientConfig(o)
	if err != nil {
		return nil, fmt.Errorf("db: cấu hình TLS: %w", err)
	}
	return c, nil
}

// hasTLS cho biết Options có khai gì để bật TLS không.
//
// InsecureSkipVerify và ServerName **không** tính: hai field đó chỉ tinh chỉnh
// cách verify, còn thứ quyết định "có dùng TLS hay không" là có vật liệu
// certificate. Một config chỉ đặt InsecureSkipVerify: true mà không có cert nào
// thì ý muốn là "kết nối tới server dùng cert tự ký" — nhưng nó cũng có nghĩa
// người dùng đang chờ TLS, nên nhánh này tính cả nó vào.
func hasTLS(o tlsx.Options) bool {
	return len(o.CertPEM) > 0 || o.CertFile != "" || o.CertB64 != "" ||
		len(o.RootCAPEM) > 0 || o.RootCAFile != "" || o.RootCAB64 != "" ||
		o.InsecureSkipVerify
}

// openPostgres dựng *sql.DB trên pgx stdlib.
func openPostgres(cfg Config, tlsCfg *tls.Config) (*sql.DB, error) {
	q := url.Values{}
	if cfg.Schema != "" {
		q.Set("search_path", cfg.Schema)
	}
	if cfg.TimeZone != "" {
		q.Set("timezone", cfg.TimeZone)
	}
	// libpq đo connect_timeout bằng giây và coi 0 là "chờ vô hạn", nên phải làm
	// tròn lên ít nhất 1 — không thì một cấu hình 300ms lại thành không timeout.
	q.Set("connect_timeout", strconv.Itoa(max(int(cfg.ConnectTimeout.Round(time.Second).Seconds()), 1)))
	if tlsCfg == nil {
		q.Set("sslmode", "disable")
	} else {
		// "require" cho ra một đường TLS duy nhất, không có fallback plaintext.
		// Việc verify certificate do tlsCfg quyết định, nên sslmode chặt hơn
		// (verify-full) cũng không thêm gì.
		q.Set("sslmode", "require")
	}

	// url.URL escape user, password và tên database — đường duy nhất chịu được
	// mật khẩu có ký tự đặc biệt.
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password.Reveal()),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.Database,
		RawQuery: q.Encode(),
	}

	pgxCfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		// Không bọc err: thông báo của pgx có thể chứa DSN, và DSN chứa mật khẩu.
		return nil, fmt.Errorf("db: dựng cấu hình kết nối postgres thất bại")
	}

	if tlsCfg != nil {
		// ParseConfig sinh TLSConfig từ sslmode. Thay bằng cấu hình của tlsx —
		// và phải thay ở cả Fallbacks, vì pgx quay số theo danh sách đó chứ
		// không chỉ theo TLSConfig ở gốc.
		pgxCfg.TLSConfig = tlsCfg
		for _, fb := range pgxCfg.Fallbacks {
			if fb.TLSConfig != nil {
				fb.TLSConfig = tlsCfg
			}
		}
	}

	return stdlib.OpenDB(*pgxCfg), nil
}

// openMySQL dựng *sql.DB trên go-sql-driver/mysql.
func openMySQL(cfg Config, tlsCfg *tls.Config) (*sql.DB, error) {
	my := gomysql.NewConfig()
	my.Net = "tcp"
	my.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	my.User = cfg.User
	my.Passwd = cfg.Password.Reveal()
	my.DBName = cfg.Database
	my.Timeout = cfg.ConnectTimeout
	my.TLS = tlsCfg

	// ParseTime bắt buộc: không có nó thì driver trả cột DATETIME/TIMESTAMP về
	// dạng []byte và GORM không scan được vào time.Time.
	my.ParseTime = true

	if cfg.TimeZone != "" {
		loc, err := time.LoadLocation(cfg.TimeZone)
		if err != nil {
			return nil, fmt.Errorf("db: TimeZone %q không hợp lệ: %w", cfg.TimeZone, err)
		}
		// Loc là múi giờ dùng để dịch cột DATETIME — kiểu đó không mang offset,
		// nên chọn sai là mọi mốc thời gian lệch đúng bằng chênh lệch múi giờ.
		my.Loc = loc
		my.Params = map[string]string{"time_zone": "'" + tzOffset(loc) + "'"}
	}

	connector, err := gomysql.NewConnector(my)
	if err != nil {
		return nil, fmt.Errorf("db: dựng cấu hình kết nối mysql thất bại")
	}
	return sql.OpenDB(connector), nil
}

// tzOffset đổi *time.Location thành offset dạng "+07:00" cho biến session
// time_zone của MySQL.
//
// Dùng offset thay vì tên vùng ("Asia/Ho_Chi_Minh") vì bảng timezone của MySQL
// là tuỳ chọn: rất nhiều bản cài không nạp nó, và lúc đó SET time_zone theo tên
// vùng trả lỗi. Đánh đổi: offset không theo được giờ mùa hè — không ảnh hưởng
// các múi giờ không có DST, và ở múi có DST thì nên dùng UTC trong database.
func tzOffset(loc *time.Location) string {
	_, secs := time.Now().In(loc).Zone()
	sign := "+"
	if secs < 0 {
		sign = "-"
		secs = -secs
	}
	return fmt.Sprintf("%s%02d:%02d", sign, secs/3600, (secs%3600)/60)
}
