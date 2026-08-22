package query_test

import (
	"strings"
	"testing"

	"github.com/cqt002/gokit/core/errs"
	"github.com/cqt002/gokit/db/query"
)

func TestEncodeDecodeCursor(t *testing.T) {
	in := map[string]any{"createdAt": "2026-08-21T00:00:00Z", "id": "u-1"}

	c := query.EncodeCursor(in)
	if c == "" {
		t.Fatal("EncodeCursor trả về rỗng")
	}
	// Cursor phải đặt được thẳng vào query string.
	if strings.ContainsAny(string(c), "+/=") {
		t.Errorf("cursor chứa ký tự cần escape trong URL: %q", c)
	}

	out, err := query.DecodeCursor(c)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	for k, want := range in {
		if out[k] != want {
			t.Errorf("out[%q] = %v, muốn %v", k, out[k], want)
		}
	}
}

// Cursor rỗng nghĩa là trang đầu, không phải lỗi của client.
func TestCursor_RongLaTrangDau(t *testing.T) {
	if got := query.EncodeCursor(nil); got != "" {
		t.Errorf("EncodeCursor(nil) = %q, muốn rỗng", got)
	}
	if got := query.EncodeCursor(map[string]any{}); got != "" {
		t.Errorf("EncodeCursor(map rỗng) = %q, muốn rỗng", got)
	}

	out, err := query.DecodeCursor("")
	if err != nil {
		t.Fatalf("DecodeCursor(\"\"): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("out = %v, muốn map rỗng", out)
	}
}

func TestDecodeCursor_KhongHopLe(t *testing.T) {
	tests := map[string]query.Cursor{
		"không phải base64": "!!!không-phải-base64!!!",
		"không phải JSON":   query.Cursor("bm90IGpzb24"), // "not json"
		"JSON null":         query.Cursor("bnVsbA"),      // "null"
	}
	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := query.DecodeCursor(c)
			if err == nil {
				t.Fatal("cursor rác mà không báo lỗi")
			}
			if !errs.Is(err, errs.CodeBadRequest) {
				t.Errorf("mã lỗi = %v, muốn %v", err, errs.CodeBadRequest)
			}
			// Nội dung cursor là chuỗi của client — không được vọng lại nguyên
			// văn trong thông báo lỗi trả cho client.
			if strings.Contains(err.Error(), string(c)) {
				t.Errorf("thông báo lỗi chứa nguyên văn cursor: %v", err)
			}
		})
	}
}

// Cursor đến từ client nên nó là input không tin được: phải có trần kích thước.
func TestDecodeCursor_QuaDai(t *testing.T) {
	_, err := query.DecodeCursor(query.Cursor(strings.Repeat("a", query.MaxCursorBytes+1)))
	if err == nil {
		t.Fatal("cursor quá dài mà không báo lỗi")
	}
}

func TestApplyCursor_TrangDauChiCoOrderBy(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.ApplyCursor(gdb.Model(&user{}), "",
		[]query.Sort{{Field: "createdAt", Desc: true}, {Field: "id"}}, allowed)
	if err != nil {
		t.Fatalf("ApplyCursor: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if strings.Contains(sql, "WHERE") {
		t.Errorf("trang đầu mà có WHERE: %q", sql)
	}
	if !strings.Contains(sql, `ORDER BY "created_at" DESC,"id"`) {
		t.Errorf("SQL = %q", sql)
	}
}

// Một tiêu chí sắp xếp → điều kiện đơn, không bọc OR.
func TestApplyCursor_MotTieuChi(t *testing.T) {
	gdb := newDB(t, "postgres")
	c := query.EncodeCursor(map[string]any{"id": "u-10"})

	tx, err := query.ApplyCursor(gdb.Model(&user{}), c,
		[]query.Sort{{Field: "id"}}, allowed)
	if err != nil {
		t.Fatalf("ApplyCursor: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	if !strings.Contains(sql, `WHERE "id" > $1`) {
		t.Errorf("SQL = %q", sql)
	}
	if len(vars) != 1 || vars[0] != "u-10" {
		t.Errorf("vars = %v", vars)
	}
}

// Đây là bẫy thật của GORM: một OrConditions chỉ có một phần tử được nối vào
// các điều kiện khác bằng OR chứ không phải AND. Nếu ApplyCursor bọc điều kiện
// một nhánh vào clause.Or thì filter phía trước sẽ mất tác dụng.
func TestApplyCursor_MotTieuChiKhongLamMatFilterKhac(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.Apply(gdb.Model(&user{}),
		[]query.Filter{{Field: "age", Op: query.Gte, Value: 18}}, allowed)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	tx, err = query.ApplyCursor(tx, query.EncodeCursor(map[string]any{"id": "u-10"}),
		[]query.Sort{{Field: "id"}}, allowed)
	if err != nil {
		t.Fatalf("ApplyCursor: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if strings.Contains(sql, " OR ") {
		t.Fatalf("điều kiện cursor bị nối bằng OR: %q", sql)
	}
	if !strings.Contains(sql, `"age" >= $1 AND "id" > $2`) {
		t.Errorf("SQL = %q", sql)
	}
}

// Điều kiện keyset là dạng khai triển của so sánh từ điển. Chiều của từng cột
// quyết định dấu so sánh ở nhánh tương ứng.
func TestApplyCursor_NhieuTieuChiKhaiTrienTuDien(t *testing.T) {
	gdb := newDB(t, "postgres")
	c := query.EncodeCursor(map[string]any{
		"createdAt": "2026-08-21",
		"id":        "u-10",
	})

	tx, err := query.ApplyCursor(gdb.Model(&user{}), c,
		[]query.Sort{{Field: "createdAt", Desc: true}, {Field: "id"}}, allowed)
	if err != nil {
		t.Fatalf("ApplyCursor: %v", err)
	}

	sql, vars := buildSQL(t, tx)
	want := `WHERE ("created_at" < $1 OR ("created_at" = $2 AND "id" > $3))`
	if !strings.Contains(sql, want) {
		t.Errorf("SQL = %q\nmuốn chứa %q", sql, want)
	}
	if len(vars) != 3 {
		t.Errorf("vars = %v, muốn 3 tham số", vars)
	}
	if !strings.Contains(sql, `ORDER BY "created_at" DESC,"id"`) {
		t.Errorf("thiếu ORDER BY đúng chiều: %q", sql)
	}
}

func TestApplyCursor_KhongCoSort(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.ApplyCursor(gdb.Model(&user{}), "", nil, allowed)
	if err == nil {
		t.Fatal("cursor không có sort mà không báo lỗi")
	}
}

func TestApplyCursor_SortNgoaiWhitelist(t *testing.T) {
	gdb := newDB(t, "postgres")

	_, err := query.ApplyCursor(gdb.Model(&user{}), "",
		[]query.Sort{{Field: "password"}}, allowed)
	if err == nil {
		t.Fatal("sort ngoài whitelist mà không báo lỗi")
	}
	if !errs.Is(err, errs.CodeBadRequest) {
		t.Errorf("mã lỗi sai: %v", err)
	}
}

func TestApplyCursor_CursorThieuGiaTri(t *testing.T) {
	gdb := newDB(t, "postgres")
	c := query.EncodeCursor(map[string]any{"id": "u-10"})

	_, err := query.ApplyCursor(gdb.Model(&user{}), c,
		[]query.Sort{{Field: "createdAt"}, {Field: "id"}}, allowed)
	if err == nil {
		t.Fatal("cursor thiếu giá trị cho createdAt mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "createdAt") {
		t.Errorf("lỗi không nói field nào bị thiếu: %v", err)
	}
}

// Mọi phép so sánh với NULL đều cho UNKNOWN, nên nhánh chứa nó lặng lẽ không
// khớp dòng nào và trang sau trả về rỗng. Phải báo lỗi thay vì mất dữ liệu.
func TestApplyCursor_GiaTriNull(t *testing.T) {
	gdb := newDB(t, "postgres")
	c := query.EncodeCursor(map[string]any{"id": nil})

	_, err := query.ApplyCursor(gdb.Model(&user{}), c,
		[]query.Sort{{Field: "id"}}, allowed)
	if err == nil {
		t.Fatal("cursor có giá trị null mà không báo lỗi")
	}
	if !strings.Contains(err.Error(), "null") {
		t.Errorf("lỗi không nói rõ nguyên nhân: %v", err)
	}
}

// ApplyCursor không áp LIMIT: chỗ gọi tự quyết, thường là limit+1 để biết còn
// trang sau hay không.
func TestApplyCursor_KhongApLimit(t *testing.T) {
	gdb := newDB(t, "postgres")

	tx, err := query.ApplyCursor(gdb.Model(&user{}), "",
		[]query.Sort{{Field: "id"}}, allowed)
	if err != nil {
		t.Fatalf("ApplyCursor: %v", err)
	}

	sql, _ := buildSQL(t, tx)
	if strings.Contains(sql, "LIMIT") {
		t.Errorf("ApplyCursor tự áp LIMIT: %q", sql)
	}
}
