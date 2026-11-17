package taskbook

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestParseWhen(t *testing.T) {
	year := fmt.Sprintf("%d", time.Now().Year())
	cases := []struct {
		in   string
		date string
		hm   string
		ok   bool
	}{
		{"08:00", "", "08:00", true},
		{"8:00", "", "08:00", true},
		{"8：30", "", "08:30", true},
		{"14:05", "", "14:05", true},
		{" 9:5 ", "", "", false},
		{"abc", "", "", false},
		{"25:00", "", "", false},
		{"如 08:00 的提示文字", "", "", false},
		{"2026-11-20 09:00", "2026-11-20", "09:00", true},
		{"2026-11-20  09:00", "2026-11-20", "09:00", true},
		{"11-20 09:00", year + "-11-20", "09:00", true},
		{"2026-2-3 7:05", "2026-02-03", "07:05", true},
		{"2026-13-01 09:00", "", "", false},
		{"2026-02-30 09:00", "", "", false},
		{"13-40 09:00", "", "", false},
		{"09:00:30", "", "", false},
	}
	for _, c := range cases {
		date, hm, ok := parseWhen(c.in)
		if ok != c.ok || hm != c.hm || (ok && date != c.date) {
			t.Errorf("parseWhen(%q) = (%q, %q, %v), want (%q, %q, %v)", c.in, date, hm, ok, c.date, c.hm, c.ok)
		}
	}
}

func TestWriteTemplateAndParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "任务表.xlsx")
	if err := WriteTemplate(path); err != nil {
		t.Fatalf("WriteTemplate: %v", err)
	}

	tasks, notes, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %v", notes)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 example rows, got %d", len(tasks))
	}
	want := TaskRow{
		Date: "", Time: "08:00", Name: "David Lee", Email: "david.lee@example.com",
		CC: "sue@pak-eco.com", RowNum: 3,
	}
	got := tasks[0]
	if got.Date != want.Date || got.Time != want.Time || got.Name != want.Name ||
		got.Email != want.Email || got.CC != want.CC || got.RowNum != want.RowNum {
		t.Errorf("row0 mismatch:\n got %+v\nwant %+v", got, want)
	}
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	nextweek := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	if tasks[1].Date != tomorrow || tasks[1].Time != "09:00" {
		t.Errorf("row1 mismatch: got date=%s time=%s, want %s 09:00", tasks[1].Date, tasks[1].Time, tomorrow)
	}
	if tasks[2].Date != nextweek || tasks[2].Time != "14:30" {
		t.Errorf("row2 mismatch: got date=%s time=%s, want %s 14:30", tasks[2].Date, tasks[2].Time, nextweek)
	}
	if tasks[0].Body == "" || tasks[2].Image != "" {
		t.Errorf("example body/image columns wrong: %+v", tasks[2])
	}
}

func TestParseRejectsBadRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.xlsx")
	f := excelize.NewFile()
	sheet := "任务表"
	f.SetSheetName("Sheet1", sheet)
	f.SetCellValue(sheet, "A1", "时间")
	f.SetCellValue(sheet, "B1", "客户名字")
	f.SetCellValue(sheet, "C1", "客户邮箱")
	f.SetCellValue(sheet, "D1", "抄送邮箱")
	f.SetCellValue(sheet, "E1", "正文内容")
	f.SetCellValue(sheet, "F1", "配图")
	f.SetCellValue(sheet, "A2", "9点") // 时间无法识别
	f.SetCellValue(sheet, "C2", "a@example.com")
	f.SetCellValue(sheet, "A3", "08:00")
	f.SetCellValue(sheet, "C3", "bad-email") // 邮箱无效
	f.SetCellValue(sheet, "A4", "10:00")
	f.SetCellValue(sheet, "C4", "ok@example.com")
	f.SetCellValue(sheet, "E4", "hello")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tasks, notes, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Email != "ok@example.com" {
		t.Fatalf("expected only the valid row, got %+v", tasks)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 skip notes, got %v", notes)
	}
}

// newTestFile opens an existing xlsx for editing in tests.
func newTestFile(t *testing.T, path string) *excelize.File {
	t.Helper()
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return f
}
