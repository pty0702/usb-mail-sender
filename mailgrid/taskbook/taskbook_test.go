package taskbook

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestNormalizeTime(t *testing.T) {
	cases := map[string]string{
		"08:00": "08:00", "8:00": "08:00", "8：30": "08:30",
		"14:05": "14:05", " 9:5 ": "", "abc": "", "25:00": "25:00",
	}
	for in, want := range cases {
		got, _ := normalizeTime(in)
		if got != want {
			t.Errorf("normalizeTime(%q) = %q, want %q", in, got, want)
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
		Time: "08:00", Name: "David Lee", Email: "david.lee@example.com",
		CC: "sue.s@pak-eco.com", RowNum: 3,
	}
	got := tasks[0]
	if got.Time != want.Time || got.Name != want.Name || got.Email != want.Email ||
		got.CC != want.CC || got.RowNum != want.RowNum {
		t.Errorf("row0 mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(tasks[1].Time, "08:00") || tasks[2].Time != "09:00" {
		t.Errorf("example times wrong: %s %s", tasks[1].Time, tasks[2].Time)
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
