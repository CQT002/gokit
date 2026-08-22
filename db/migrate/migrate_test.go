package migrate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/cqt002/gokit/db/migrate"
)

// appliedIDs đọc lịch sử migration và trả về danh sách ID.
func appliedIDs(t *testing.T, gdb *gorm.DB, opts migrate.Options) []string {
	t.Helper()
	recs, err := migrate.Applied(context.Background(), gdb, opts)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}

	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}

func TestRun_ChayTheoThuTuTrongSlice(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	// ID cố tình không theo thứ tự chữ cái: thứ tự chạy là thứ tự trong slice.
	ms := []migrate.Migration{
		{ID: "b_second", Up: rec.up("b_second")},
		{ID: "a_first", Up: rec.up("a_first")},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"up:b_second", "up:a_first"}
	if !slices.Equal(rec.steps, want) {
		t.Errorf("thứ tự chạy = %v, muốn %v", rec.steps, want)
	}
	if got := appliedIDs(t, gdb, opts); len(got) != 2 {
		t.Errorf("lịch sử = %v, muốn 2 bản ghi", got)
	}
	if !gdb.Migrator().HasTable("a_first") {
		t.Error("migration chạy mà bảng không được tạo")
	}
}

// Gọi Run nhiều lần là an toàn — pod nào khởi động cũng gọi nó.
func TestRun_KhongChayLaiMigrationDaChay(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	for range 3 {
		if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	if len(rec.steps) != 1 {
		t.Errorf("migration chạy %d lần, muốn 1", len(rec.steps))
	}
}

func TestRun_ThemMigrationMoi(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ms = append(ms, migrate.Migration{ID: "m2", Up: rec.up("m2")})
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run lần hai: %v", err)
	}

	if !slices.Equal(rec.steps, []string{"up:m1", "up:m2"}) {
		t.Errorf("steps = %v", rec.steps)
	}
	if got := appliedIDs(t, gdb, opts); !slices.Equal(got, []string{"m1", "m2"}) {
		t.Errorf("lịch sử = %v", got)
	}
}

// Migration lỗi thì transaction rollback, lịch sử không có bản ghi, và các
// migration sau không chạy.
func TestRun_MigrationLoi(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	boom := errors.New("boom")
	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1")},
		{ID: "m2", Up: func(tx *gorm.DB) error {
			rec.steps = append(rec.steps, "up:m2")
			if err := tx.Exec("CREATE TABLE m2 (id INTEGER)").Error; err != nil {
				return err
			}
			return boom
		}},
		{ID: "m3", Up: rec.up("m3")},
	}

	err := migrate.Run(context.Background(), gdb, ms, opts)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, muốn bọc lỗi gốc", err)
	}
	if !strings.Contains(err.Error(), "m2") {
		t.Errorf("lỗi không nói migration nào thất bại: %v", err)
	}

	if slices.Contains(rec.steps, "up:m3") {
		t.Error("migration sau migration lỗi vẫn chạy")
	}
	if got := appliedIDs(t, gdb, opts); !slices.Equal(got, []string{"m1"}) {
		t.Errorf("lịch sử = %v, muốn chỉ có m1", got)
	}
	if gdb.Migrator().HasTable("m2") {
		t.Error("transaction của migration lỗi không được rollback")
	}
}

// Chạy lại sau khi sửa lỗi phải tiếp tục từ đúng chỗ dừng.
func TestRun_TiepTucSauKhiSuaLoi(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	fail := true
	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1")},
		{ID: "m2", Up: func(tx *gorm.DB) error {
			if fail {
				return errors.New("boom")
			}
			return rec.up("m2")(tx)
		}},
	}

	if err := migrate.Run(context.Background(), gdb, ms, opts); err == nil {
		t.Fatal("muốn lỗi ở lần chạy đầu")
	}

	fail = false
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run lần hai: %v", err)
	}
	if !slices.Equal(rec.steps, []string{"up:m1", "up:m2"}) {
		t.Errorf("steps = %v — m1 không được chạy lại, m2 phải chạy", rec.steps)
	}
}

func TestRun_CanhBaoMigrationKhongConTrongCode(t *testing.T) {
	gdb := newDB(t)
	opts, buf := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1")},
		{ID: "m2", Up: rec.up("m2")},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	buf.Reset()
	if err := migrate.Run(context.Background(), gdb, ms[:1], opts); err != nil {
		t.Fatalf("Run lần hai: %v", err)
	}

	log := buf.String()
	if !strings.Contains(log, `"level":"WARN"`) || !strings.Contains(log, "m2") {
		t.Errorf("không có cảnh báo cho m2: %s", log)
	}
}

func TestRun_ValidateChanTruocKhiChamDatabase(t *testing.T) {
	tests := map[string][]migrate.Migration{
		"ID rỗng":  {{ID: "", Up: func(*gorm.DB) error { return nil }}},
		"thiếu Up": {{ID: "m1"}},
		"ID trùng": {
			{ID: "m1", Up: func(*gorm.DB) error { return nil }},
			{ID: "m1", Up: func(*gorm.DB) error { return nil }},
		},
	}
	for name, ms := range tests {
		t.Run(name, func(t *testing.T) {
			gdb := newDB(t)
			opts, _ := newOptions(t)

			if err := migrate.Run(context.Background(), gdb, ms, opts); err == nil {
				t.Fatal("danh sách không hợp lệ mà Run không báo lỗi")
			}
			// Bảng lịch sử cũng không được tạo: validate chạy trước mọi thứ.
			if gdb.Migrator().HasTable(migrate.DefaultTable) {
				t.Error("Run đã chạm vào database dù danh sách không hợp lệ")
			}
		})
	}
}

// ID trùng ở cuối danh sách phải chặn cả những migration trước nó.
func TestRun_IDTrungChanCaMigrationTruocDo(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1")},
		{ID: "m2", Up: rec.up("m2")},
		{ID: "m2", Up: rec.up("m2")},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err == nil {
		t.Fatal("muốn lỗi vì ID trùng")
	}
	if len(rec.steps) != 0 {
		t.Errorf("đã chạy %v dù danh sách không hợp lệ", rec.steps)
	}
}

func TestRun_TenBangTuyChon(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	opts.Table = "lich_su_migration"
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !gdb.Migrator().HasTable("lich_su_migration") {
		t.Error("không dùng tên bảng đã khai")
	}
	if gdb.Migrator().HasTable(migrate.DefaultTable) {
		t.Error("vẫn tạo bảng mặc định")
	}
}

func TestRollback_ToRongThiHoanTacHet(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1"), Down: rec.down("m1")},
		{ID: "m2", Up: rec.up("m2"), Down: rec.down("m2")},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec.steps = nil
	if err := migrate.Rollback(context.Background(), gdb, ms, "", opts); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// Hoàn tác theo thứ tự ngược.
	if !slices.Equal(rec.steps, []string{"down:m2", "down:m1"}) {
		t.Errorf("steps = %v", rec.steps)
	}
	if got := appliedIDs(t, gdb, opts); len(got) != 0 {
		t.Errorf("lịch sử = %v, muốn rỗng", got)
	}
	if gdb.Migrator().HasTable("m1") || gdb.Migrator().HasTable("m2") {
		t.Error("Down không thực sự chạy")
	}
}

// to là mốc muốn giữ, giống cách git reset nhận commit muốn giữ.
func TestRollback_DungOMoc(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1"), Down: rec.down("m1")},
		{ID: "m2", Up: rec.up("m2"), Down: rec.down("m2")},
		{ID: "m3", Up: rec.up("m3"), Down: rec.down("m3")},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec.steps = nil
	if err := migrate.Rollback(context.Background(), gdb, ms, "m1", opts); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if !slices.Equal(rec.steps, []string{"down:m3", "down:m2"}) {
		t.Errorf("steps = %v — m1 phải được giữ lại", rec.steps)
	}
	if got := appliedIDs(t, gdb, opts); !slices.Equal(got, []string{"m1"}) {
		t.Errorf("lịch sử = %v", got)
	}
}

func TestRollback_MocKhongTonTai(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1"), Down: rec.down("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec.steps = nil
	if err := migrate.Rollback(context.Background(), gdb, ms, "khong-co", opts); err == nil {
		t.Fatal("mốc không tồn tại mà không báo lỗi")
	}
	if len(rec.steps) != 0 {
		t.Errorf("đã hoàn tác %v dù mốc không tồn tại", rec.steps)
	}
}

// Kiểm tra cả kế hoạch trước khi chạy: rollback một nửa để lại schema mà không
// môi trường nào khác có.
func TestRollback_ThieuDownThiKhongHoanTacGi(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1")}, // không có Down
		{ID: "m2", Up: rec.up("m2"), Down: rec.down("m2")},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rec.steps = nil
	err := migrate.Rollback(context.Background(), gdb, ms, "", opts)
	if err == nil {
		t.Fatal("migration không có Down mà Rollback không báo lỗi")
	}
	if !strings.Contains(err.Error(), "m1") {
		t.Errorf("lỗi không nói migration nào: %v", err)
	}
	if len(rec.steps) != 0 {
		t.Errorf("đã hoàn tác %v dù kế hoạch không hợp lệ", rec.steps)
	}
	if got := appliedIDs(t, gdb, opts); !slices.Equal(got, []string{"m1", "m2"}) {
		t.Errorf("lịch sử = %v, muốn không đổi", got)
	}
}

func TestRollback_BoQuaMigrationChuaChay(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	applied := []migrate.Migration{{ID: "m1", Up: rec.up("m1"), Down: rec.down("m1")}}
	if err := migrate.Run(context.Background(), gdb, applied, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// m2 có trong code nhưng chưa chạy — và cố tình không có Down, để chứng
	// minh nó không hề được xét tới.
	ms := append(slices.Clone(applied), migrate.Migration{ID: "m2", Up: rec.up("m2")})

	rec.steps = nil
	if err := migrate.Rollback(context.Background(), gdb, ms, "", opts); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if !slices.Equal(rec.steps, []string{"down:m1"}) {
		t.Errorf("steps = %v", rec.steps)
	}
}

// Down lỗi thì transaction rollback và bản ghi lịch sử vẫn còn.
func TestRollback_DownLoi(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	boom := errors.New("boom")
	ms := []migrate.Migration{
		{ID: "m1", Up: rec.up("m1"), Down: func(*gorm.DB) error { return boom }},
	}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := migrate.Rollback(context.Background(), gdb, ms, "", opts); !errors.Is(err, boom) {
		t.Fatalf("err = %v, muốn bọc lỗi gốc", err)
	}
	if got := appliedIDs(t, gdb, opts); !slices.Equal(got, []string{"m1"}) {
		t.Errorf("lịch sử = %v, muốn vẫn còn m1", got)
	}
}

func TestApplied_TaoBangKhiChuaCo(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)

	recs, err := migrate.Applied(context.Background(), gdb, opts)
	if err != nil {
		t.Fatalf("Applied trên database trống: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("recs = %v, muốn rỗng", recs)
	}
}

func TestApplied_CoThoiDiemChay(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	recs, err := migrate.Applied(context.Background(), gdb, opts)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %v", recs)
	}
	if recs[0].AppliedAt.IsZero() {
		t.Error("AppliedAt không được ghi")
	}
}

func TestOptions_ZeroValueDungDuoc(t *testing.T) {
	gdb := newDB(t)
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, migrate.Options{}); err != nil {
		t.Fatalf("Run với Options{}: %v", err)
	}
	if !gdb.Migrator().HasTable(migrate.DefaultTable) {
		t.Errorf("không dùng bảng mặc định %s", migrate.DefaultTable)
	}
}

func TestRun_DanhSachRong(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)

	if err := migrate.Run(context.Background(), gdb, nil, opts); err != nil {
		t.Fatalf("Run với danh sách rỗng: %v", err)
	}
}

// SQLite không có advisory lock nên Run chạy không khoá — nhưng vẫn phải chạy,
// không phải trả lỗi.
func TestRun_DriverKhongCoAdvisoryLock(t *testing.T) {
	gdb := newDB(t)
	opts, buf := newOptions(t)
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "advisory lock") {
		t.Errorf("không có log giải thích việc chạy không khoá: %s", buf.String())
	}
}

func TestRun_DisableLock(t *testing.T) {
	gdb := newDB(t)
	opts, _ := newOptions(t)
	opts.DisableLock = true
	var rec recorder

	ms := []migrate.Migration{{ID: "m1", Up: rec.up("m1")}}
	if err := migrate.Run(context.Background(), gdb, ms, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.steps) != 1 {
		t.Errorf("steps = %v", rec.steps)
	}
}
