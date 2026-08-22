package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/db/query"
)

func TestApply_CacPhepSoSanh(t *testing.T) {
	tests := []struct {
		op      query.Op
		value   any
		wantSQL string
		wantVar any
	}{
		{query.Eq, "an", `"name" = $1`, "an"},
		{query.Ne, "an", `"name" <> $1`, "an"},
		{query.Gt, "an", `"name" > $1`, "an"},
		{query.Gte, "an", `"name" >= $1`, "an"},
		{query.Lt, "an", `"name" < $1`, "an"},
		{query.Lte, "an", `"name" <= $1`, "an"},
	}
	for _, tc := range tests {
		t.Run(string(tc.op), func(t *testing.T) {
			gdb := newDB(t, "postgres")

			tx, err := query.Apply(gdb.Model(&user{}),
				[]query.Filter{{Field: "name", Op: tc.op, Value: tc.value}}, allowed)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			sql, vars := buildSQL(t, tx)
			if !strings.Contains(sql, tc.wantSQL) {
				t.Errorf("SQL = %q, muốn chứa %q", sql, tc.wantSQL)
			}
			if len(vars) != 1 || vars[0] != tc.wantVar {
				t.Errorf("vars = %v, muốn [%v]", vars, tc.wantVar)
			}
		})
	}
}

// Tên field client gửi lên được dịch sang tên cột, nên schema không lộ ra API.
func TestApply_DichTenFieldSangTenCot(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "createdAt", Op: query.Gte, Value: "2026-01-01"}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if !strings.Contains(sql, `"created_at" >=`) {
		t.Errorf("SQL = %q, muốn dùng cột created_at", sql)
	}
	if strings.Contains(sql, "createdAt") {
		t.Errorf("tên field của client lọt vào SQL: %q", sql)
	}
}

// Đây là lớp chặn SQL injection: field lạ trả lỗi, không phải bị bỏ qua.
func TestApply_FieldNgoaiWhitelist(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "password", Op: query.Eq, Value: "x"}}, allowed)
	if err == nil {
		t.Fatal("field ngoài whitelist mà không báo lỗi")
	}
	if !errs.Is(err, errs.CodeBadRequest) {
		t.Errorf("mã lỗi = %v, muốn %v", err, errs.CodeBadRequest)
	}

	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("lỗi không phải *errs.Error: %T", err)
	}
	if len(e.Fields) != 1 || e.Fields[0].Field != "password" {
		t.Errorf("Fields = %+v, muốn chỉ ra field password", e.Fields)
	}
}

func TestApply_FieldTroToChuoiRong(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "x", Op: query.Eq, Value: 1}},
		map[string]string{"x": ""})
	if err == nil {
		t.Fatal("cột rỗng trong whitelist mà không báo lỗi")
	}
}

// Chuỗi injection kinh điển: nếu nó lọt vào câu lệnh thì test này thấy ngay.
func TestApply_GiaTriKhongVaoTextCauLenh(t *testing.T) {
	gdb := newDB(t, "postgres")
	evil := "x'; DROP TABLE users; --"

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.Eq, Value: evil}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if strings.Contains(sql, "DROP TABLE") {
		t.Fatalf("giá trị lọt vào text câu lệnh: %q", sql)
	}
	if len(vars) != 1 || vars[0] != evil {
		t.Errorf("vars = %v, muốn [%q]", vars, evil)
	}
}

func TestApply_NhieuDieuKienNoiBangAnd(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}), []query.Filter{
		{Field: "name", Op: query.Eq, Value: "an"},
		{Field: "age", Op: query.Gte, Value: 18},
	}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if !strings.Contains(sql, `"name" = $1 AND "age" >= $2`) {
		t.Errorf("SQL = %q", sql)
	}
	if len(vars) != 2 {
		t.Errorf("vars = %v, muốn 2 tham số", vars)
	}
}

func TestApply_RongThiKhongThemDieuKien(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}), nil, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if strings.Contains(sql, "WHERE") {
		t.Errorf("filter rỗng mà vẫn có WHERE: %q", sql)
	}
}

func TestApply_Like(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.Like, Value: "an"}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if !strings.Contains(sql, `"name" LIKE $1 ESCAPE '!'`) {
		t.Errorf("SQL = %q", sql)
	}
	if len(vars) != 1 || vars[0] != "%an%" {
		t.Errorf("vars = %v, muốn [%%an%%]", vars)
	}
}

// Ký tự đại diện do client gửi lên phải bị vô hiệu hoá: `%` không kiểm soát
// biến câu query có index thành full scan.
func TestApply_LikeEscapeKyTuDaiDien(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.Like, Value: `a%b_c!d`}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	_, vars := buildSQL(t, tx)
	want := `%a!%b!_c!!d%`
	if len(vars) != 1 || vars[0] != want {
		t.Errorf("vars = %v, muốn [%s]", vars, want)
	}
}

func TestApply_ILikePostgresDungToanTuRieng(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.ILike, Value: "an"}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if !strings.Contains(sql, `"name" ILIKE $1 ESCAPE '!'`) {
		t.Errorf("SQL = %q, muốn dùng ILIKE", sql)
	}
}

// MySQL không có ILIKE. Cùng ngữ nghĩa, khác cú pháp.
func TestApply_ILikeMySQLDungLower(t *testing.T) {
	gdb := newDB(t, "mysql")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.ILike, Value: "an"}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if !strings.Contains(sql, "LOWER(`name`) LIKE LOWER(?)") {
		t.Errorf("SQL = %q, muốn dùng LOWER()", sql)
	}
	if strings.Contains(sql, "ILIKE") {
		t.Errorf("ILIKE lọt vào câu lệnh MySQL: %q", sql)
	}
}

func TestApply_LikeCanGiaTriChuoi(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "age", Op: query.Like, Value: 18}}, allowed)
	if err == nil {
		t.Fatal("Like với giá trị số mà không báo lỗi")
	}
	if !errs.Is(err, errs.CodeBadRequest) {
		t.Errorf("mã lỗi sai: %v", err)
	}
}

func TestApply_In(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "age", Op: query.In, Value: []int{18, 19}}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if !strings.Contains(sql, `"age" IN ($1,$2)`) {
		t.Errorf("SQL = %q", sql)
	}
	if len(vars) != 2 {
		t.Errorf("vars = %v, muốn 2 tham số", vars)
	}
}

// Giá trị từ JSON là []any, từ code Go là []string hay []int. Cả hai phải chạy.
func TestApply_InNhanMoiKieuSlice(t *testing.T) {
	values := []any{
		[]any{"a", "b"},
		[]string{"a", "b"},
		[2]string{"a", "b"},
	}
	for _, v := range values {
		gdb := newDB(t, "postgres")

		tx, err := query.Apply(gdb.Model(&user{}),
			[]query.Filter{{Field: "name", Op: query.In, Value: v}}, allowed)
		if err != nil {
			t.Errorf("Apply với %T: %v", v, err)
			continue
		}
		if _, vars := buildSQL(t, tx); len(vars) != 2 {
			t.Errorf("%T: vars = %v", v, vars)
		}
	}
}

func TestApply_NotIn(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "age", Op: query.NotIn, Value: []int{18, 19}}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if !strings.Contains(sql, `"age" NOT IN ($1,$2)`) {
		t.Errorf("SQL = %q", sql)
	}
	if len(vars) != 2 {
		t.Errorf("vars = %v, muốn 2 tham số", vars)
	}
}

// GORM rút gọn NOT IN một phần tử thành `<>`. Hai dạng tương đương, và test này
// ghim lại hành vi đó để một bản GORM mới đổi cách sinh SQL thì thấy ngay.
func TestApply_NotInMotGiaTri(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "age", Op: query.NotIn, Value: []int{18}}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if !strings.Contains(sql, `"age" <> $1`) {
		t.Errorf("SQL = %q", sql)
	}
	if len(vars) != 1 || vars[0] != 18 {
		t.Errorf("vars = %v, muốn [18]", vars)
	}
}

// `IN ()` là lỗi cú pháp ở cả Postgres và MySQL, nên danh sách rỗng phải được
// xử lý tường minh: IN rỗng khớp 0 dòng, NOT IN rỗng khớp mọi dòng.
func TestApply_InRong(t *testing.T) {
	tests := []struct {
		op   query.Op
		want string
	}{
		{query.In, "1 = 0"},
		{query.NotIn, "1 = 1"},
	}
	for _, tc := range tests {
		for _, empty := range []any{[]any{}, []string(nil)} {
			gdb := newDB(t, "postgres")

			tx, err := query.Apply(gdb.Model(&user{}),
				[]query.Filter{{Field: "age", Op: tc.op, Value: empty}}, allowed)
			if err != nil {
				t.Errorf("%s với %#v: %v", tc.op, empty, err)
				continue
			}

			sql, _ := buildSQL(t, tx)
			if !strings.Contains(sql, tc.want) {
				t.Errorf("%s với %#v: SQL = %q, muốn chứa %q", tc.op, empty, sql, tc.want)
			}
		}
	}
}

func TestApply_InCanSlice(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "age", Op: query.In, Value: 18}}, allowed)
	if err == nil {
		t.Fatal("In với giá trị đơn mà không báo lỗi")
	}
}

func TestApply_IsNull(t *testing.T) {
	tests := []struct {
		value bool
		want  string
	}{
		{true, `"name" IS NULL`},
		{false, `"name" IS NOT NULL`},
	}
	for _, tc := range tests {
		gdb := newDB(t, "postgres")

		tx, err := query.Apply(gdb.Model(&user{}),
			[]query.Filter{{Field: "name", Op: query.Null, Value: tc.value}}, allowed)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}

		sql, vars := buildSQL(t, tx)
		if !strings.Contains(sql, tc.want) {
			t.Errorf("SQL = %q, muốn chứa %q", sql, tc.want)
		}
		if len(vars) != 0 {
			t.Errorf("vars = %v, muốn rỗng", vars)
		}
	}
}

func TestApply_IsNullCanBool(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.Null, Value: "true"}}, allowed)
	if err == nil {
		t.Fatal("is_null với chuỗi mà không báo lỗi")
	}
}

// nil và slice có Op riêng (is_null, in). Để chúng lọt vào phép so sánh hai
// toán hạng thì GORM lặng lẽ diễn giải lại thành IS NULL / IN (...).
func TestApply_SoSanhKhongNhanNilHoacSlice(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"slice", []string{"a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gdb := newDB(t, "postgres")

			_, err := query.Apply(gdb.Model(&user{}),
				[]query.Filter{{Field: "name", Op: query.Eq, Value: tc.value}}, allowed)
			if err == nil {
				t.Fatalf("Eq với %s mà không báo lỗi", tc.name)
			}
		})
	}
}

// []byte là chuỗi byte, không phải danh sách giá trị.
func TestApply_SoSanhNhanBytes(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: query.Eq, Value: []byte("an")}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if !strings.Contains(sql, `"name" = $1`) {
		t.Errorf("SQL = %q", sql)
	}
}

func TestApply_OpLa(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "name", Op: "raw", Value: "1=1"}}, allowed)
	if err == nil {
		t.Fatal("op lạ mà không báo lỗi")
	}
	if !errs.Is(err, errs.CodeBadRequest) {
		t.Errorf("mã lỗi sai: %v", err)
	}
}

func TestPaginate_MacDinh(t *testing.T) {
	gdb := newDB(t, "postgres")

	scope, err := query.Paginate(query.Page{}, allowed)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}

	sql, _ := buildSQL(t, gdb.Model(&user{}).Scopes(scope))
	if !strings.Contains(sql, "LIMIT $1") {
		t.Errorf("SQL = %q, muốn có LIMIT", sql)
	}
	if strings.Contains(sql, "OFFSET") {
		t.Errorf("offset 0 mà vẫn sinh OFFSET: %q", sql)
	}
}

func TestPaginate_LimitBiHaXuongTran(t *testing.T) {
	gdb := newDB(t, "postgres")

	scope, err := query.Paginate(query.Page{Limit: 1_000_000}, allowed)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}

	_, vars := buildSQL(t, gdb.Model(&user{}).Scopes(scope))
	if len(vars) != 1 || vars[0] != query.MaxLimit {
		t.Errorf("vars = %v, muốn LIMIT %d", vars, query.MaxLimit)
	}
}

func TestPaginate_OffsetAmCoiLaKhong(t *testing.T) {
	gdb := newDB(t, "postgres")

	scope, err := query.Paginate(query.Page{Limit: 10, Offset: -5}, allowed)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}

	sql, _ := buildSQL(t, gdb.Model(&user{}).Scopes(scope))
	if strings.Contains(sql, "OFFSET") {
		t.Errorf("offset âm sinh ra OFFSET: %q", sql)
	}
}

func TestPaginate_Sort(t *testing.T) {
	gdb := newDB(t, "postgres")

	scope, err := query.Paginate(query.Page{
		Limit: 10,
		Sort: []query.Sort{
			{Field: "createdAt", Desc: true},
			{Field: "id"},
		},
	}, allowed)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}

	sql, _ := buildSQL(t, gdb.Model(&user{}).Scopes(scope))
	if !strings.Contains(sql, `ORDER BY "created_at" DESC,"id"`) {
		t.Errorf("SQL = %q", sql)
	}
}

func TestPaginate_SortNgoaiWhitelist(t *testing.T) {
	_, err := query.Paginate(query.Page{Sort: []query.Sort{{Field: "password"}}}, allowed)
	if err == nil {
		t.Fatal("sort theo field ngoài whitelist mà không báo lỗi")
	}
	if !errs.Is(err, errs.CodeBadRequest) {
		t.Errorf("mã lỗi sai: %v", err)
	}
}

// Count phải chạy trên truy vấn chưa có LIMIT — đó là lý do Paginate trả scope
// chứ không sửa thẳng *gorm.DB.
func TestPaginate_ScopeKhongDinhVaoTruyVanGoc(t *testing.T) {
	gdb := newDB(t, "postgres")

	scope, err := query.Paginate(query.Page{Limit: 10}, allowed)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}

	tx := gdb.Model(&user{})
	if sql, _ := buildSQL(t, tx.Scopes(scope)); !strings.Contains(sql, "LIMIT") {
		t.Fatalf("scope không áp LIMIT: %q", sql)
	}
	if sql, _ := buildSQL(t, gdb.Model(&user{})); strings.Contains(sql, "LIMIT") {
		t.Errorf("truy vấn mới vẫn mang LIMIT: %q", sql)
	}
}
