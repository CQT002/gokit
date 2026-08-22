package migrate

import "testing"

func TestLockKey_OnDinh(t *testing.T) {
	// Khoá phải giống nhau giữa hai tiến trình và hai lần build, nếu không thì
	// hai pod cùng deploy sẽ lấy hai khoá khác nhau và không chặn nhau.
	table := DefaultTable
	if got, again := lockKey(table), lockKey(table); got != again {
		t.Errorf("lockKey không ổn định: %d rồi %d", got, again)
	}
}

func TestLockKey_KhacNhauTheoTenBang(t *testing.T) {
	// Advisory lock của Postgres có phạm vi cả cluster, nên hai database dùng
	// chung một instance nhưng khác bảng lịch sử không được chặn nhau.
	if lockKey("schema_migrations") == lockKey("lich_su_migration") {
		t.Error("hai tên bảng khác nhau cho ra cùng một khoá")
	}
}

func TestOptionsNormalize(t *testing.T) {
	got := Options{}.normalize()

	if got.Table != DefaultTable {
		t.Errorf("Table = %q, muốn %q", got.Table, DefaultTable)
	}
	if got.LockTimeout != DefaultLockTimeout {
		t.Errorf("LockTimeout = %v, muốn %v", got.LockTimeout, DefaultLockTimeout)
	}
	if got.Logger == nil {
		t.Error("Logger vẫn nil")
	}
}

func TestOptionsNormalize_KhongGhiDe(t *testing.T) {
	got := Options{Table: "t", LockTimeout: 1}.normalize()

	if got.Table != "t" || got.LockTimeout != 1 {
		t.Errorf("normalize ghi đè giá trị đã khai: %+v", got)
	}
}
