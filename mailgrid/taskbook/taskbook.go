// Package taskbook implements the Excel task table mode: customers describe
// what to send and when in a spreadsheet, and the scheduler delivers each
// time-group through the regular mailgrid sending pipeline.
package taskbook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bravo1goingdark/mailgrid/parser"
	"github.com/xuri/excelize/v2"
)

// TaskRow is one row of the task spreadsheet: one recipient, one send.
type TaskRow struct {
	Time   string // "HH:MM" local time
	Date   string // "2006-01-02" for one-shot scheduling; empty = every day
	Name   string
	Email  string
	CC     string // optional, comma-separated
	Body   string // plain text body (multi-line); escaped to HTML at send time
	Image  string // optional image file name(s), comma-separated, resolved against the tool folder
	RowNum int    // 1-based spreadsheet row, for log messages
}

// headerAliases maps accepted header spellings (lowercase) to canonical keys.
// English and Chinese headers are both accepted so customers can rename them.
var headerAliases = map[string]string{
	"time": "time", "时间": "time", "发送时间": "time",
	"name": "name", "客户名字": "name", "客户名称": "name", "称呼": "name",
	"email": "email", "客户邮箱": "email", "邮箱": "email", "收件邮箱": "email",
	"cc": "cc", "抄送邮箱": "cc", "抄送": "cc",
	"body": "body", "正文内容": "body", "正文": "body", "邮件正文": "body",
	"image": "image", "配图": "image", "图片": "image",
}

var (
	hmRe     = regexp.MustCompile(`^\s*(\d{1,2})\s*[:：]\s*(\d{2})\s*$`)
	fullRe   = regexp.MustCompile(`^\s*(\d{4})-(\d{1,2})-(\d{1,2})\s+(\d{1,2})\s*[:：]\s*(\d{2})\s*$`)
	mdRe     = regexp.MustCompile(`^\s*(\d{1,2})-(\d{1,2})\s+(\d{1,2})\s*[:：]\s*(\d{2})\s*$`)
	sepSpace = regexp.MustCompile(`\s+`)
)

// pad2 prefixes a single digit with zero ("8" -> "08").
func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

// validHM checks hour<=23 and minute<=59 on an "HH:MM" string.
func validHM(hm string) bool {
	return hm[:2] <= "23" && hm[3:] <= "59"
}

// validDate verifies a "2006-01-02" string is a real calendar date.
func validDate(date string) bool {
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}

// normalizeTime accepts a cell that is exactly a time ("8:00", "08：30") and
// rejects prose cells — hint text containing examples like "如 08:00" must not
// be mistaken for a send time.
func normalizeTime(cell string) (string, bool) {
	m := hmRe.FindStringSubmatch(cell)
	if m == nil {
		return "", false
	}
	hm := pad2(m[1]) + ":" + m[2]
	if !validHM(hm) {
		return "", false
	}
	return hm, true
}

// parseWhen accepts three spellings for the 时间 column and returns
// (date, "HH:MM", ok). An empty date means "every day".
//
//	"08:00"                -> ("", "08:00")      every day
//	"11-20 09:00"          -> ("2006-11-20", …)  this year, one shot
//	"2026-11-20 09:00"     -> ("2026-11-20", …)  explicit date, one shot
func parseWhen(cell string) (string, string, bool) {
	cell = sepSpace.ReplaceAllString(strings.TrimSpace(cell), " ")
	if hm, ok := normalizeTime(cell); ok {
		return "", hm, true
	}
	if m := fullRe.FindStringSubmatch(cell); m != nil {
		date := m[1] + "-" + pad2(m[2]) + "-" + pad2(m[3])
		hm := pad2(m[4]) + ":" + m[5]
		if !validDate(date) || !validHM(hm) {
			return "", "", false
		}
		return date, hm, true
	}
	if m := mdRe.FindStringSubmatch(cell); m != nil {
		date := fmt.Sprintf("%d-%s-%s", time.Now().Year(), pad2(m[1]), pad2(m[2]))
		if !validDate(date) {
			return "", "", false
		}
		hm := pad2(m[3]) + ":" + m[4]
		if !validHM(hm) {
			return "", "", false
		}
		return date, hm, true
	}
	return "", "", false
}

// Parse reads the first worksheet of the task spreadsheet. The first non-empty
// row must be the header; every following non-empty row is one task. Rows with
// an empty or invalid email are skipped and reported in the returned notes.
func Parse(path string) ([]TaskRow, []string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("打开任务表失败: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, nil, fmt.Errorf("读取任务表失败: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("任务表是空的")
	}

	// Locate the header row: the first row containing a recognizable email
	// column. Column letters vary when customers insert columns, so map by
	// header text rather than fixed positions.
	col := map[string]int{}
	headerRow := -1
	for i, row := range rows {
		for j, cell := range row {
			key, ok := headerAliases[strings.ToLower(strings.TrimSpace(cell))]
			if ok {
				col[key] = j
			}
		}
		if _, ok := col["email"]; ok {
			headerRow = i
			break
		}
		for k := range col {
			delete(col, k)
		}
	}
	if headerRow == -1 {
		return nil, nil, fmt.Errorf("任务表缺少表头：第一行需包含「客户邮箱」列")
	}

	cell := func(row []string, key string) string {
		idx, ok := col[key]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	var (
		tasks []TaskRow
		notes []string
	)
	for i := headerRow + 1; i < len(rows); i++ {
		row := rows[i]
		email := cell(row, "email")
		if email == "" {
			continue // blank separator row
		}
		rowNum := i + 1
		date, hm, timeOK := parseWhen(cell(row, "time"))
		// Hint rows from the generated template (grey instructions under the
		// header) have neither a valid time nor a valid email — skip silently
		// so customers never see warnings about the template itself.
		if !timeOK && !parser.IsValidEmail(email) {
			continue
		}
		if !parser.IsValidEmail(email) {
			notes = append(notes, fmt.Sprintf("第 %d 行邮箱格式无效，已跳过: %s", rowNum, email))
			continue
		}
		if !timeOK {
			notes = append(notes, fmt.Sprintf("第 %d 行时间无法识别（应为 HH:MM，或带日期如 2026-11-20 09:00），已跳过", rowNum))
			continue
		}
		tasks = append(tasks, TaskRow{
			Time:   hm,
			Date:   date,
			Name:   cell(row, "name"),
			Email:  email,
			CC:     cell(row, "cc"),
			Body:   cell(row, "body"),
			Image:  cell(row, "image"),
			RowNum: rowNum,
		})
	}
	if len(tasks) == 0 {
		return nil, notes, fmt.Errorf("任务表里没有可发送的任务")
	}
	return tasks, notes, nil
}

// WriteTemplate creates a fresh, customer-fillable task spreadsheet that
// mirrors the original requirement sheet: every column carries a grey hint
// row under the header, plus worked examples and a full instruction sheet.
func WriteTemplate(path string) error {
	f := excelize.NewFile()
	sheet := "任务表"
	f.SetSheetName("Sheet1", sheet)

	headers := []string{"时间", "客户名字", "客户邮箱", "抄送邮箱", "正文内容", "配图"}
	hints := []string{
		"发送时间：每天发填 08:00；指定日期发填 2026-11-20 09:00 或 11-20 09:00",
		"客户称呼，程序自动填进开头的「Hi 名字,」",
		"客户邮箱（必填）",
		"抄送邮箱，可留空；多人用英文逗号隔开",
		"正文内容：从「Hi 名字,」之后写起，可在单元格内换行（Alt+Enter）分段；每行正文可以完全不同",
		"配图文件名（如 poster.jpg），可留空；图片文件放在本软件同一文件夹",
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	for i, hint := range hints {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		f.SetCellValue(sheet, cell, hint)
	}

	examples := [][]string{
		{"08:00", "David Lee", "david.lee@example.com", "sue@pak-eco.com",
			"PACK EXPO 2026 is coming — and we'd love to see you there!\n\nJoin Pak2Eco in Chicago and explore our latest paper packaging solutions, including Retail Packaging, Food Packaging, E-commerce Packaging, Custom Packaging, and New Insulated Bags.\n\nOct. 18–21, 2026 · McCormick Place, Chicago, IL · West Hall, Booth W-46076", "poster.jpg"},
		{"08:00", "Sarah Chen", "sarah.chen@example.com", "",
			"PACK EXPO 2026 is coming — see you in Chicago!", "poster.jpg"},
		{"09:00", "Mike Wang", "mike.wang@example.com", "",
			"Hi 之后的正文从这里写，想写几段写几段。", ""},
	}
	for r, row := range examples {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+3)
			f.SetCellValue(sheet, cell, v)
		}
	}

	// Styling: bold header, grey hint row, usable column widths.
	if style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 12},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#DDEBF7"}, Pattern: 1},
	}); err == nil {
		f.SetCellStyle(sheet, "A1", "F1", style)
	}
	if style, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Color: "#808080", Size: 9},
	}); err == nil {
		f.SetCellStyle(sheet, "A2", "F2", style)
	}
	widths := []float64{22, 14, 26, 22, 60, 16}
	for i, w := range widths {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, colName, colName, w)
	}

	// Instruction sheet, same content spirit as the customer's original
	// requirement table (正文1~正文6).
	const info = "任务表填写说明\n\n" +
		"1. 时间：两种写法。\n" +
		"   a) 只写几点（如 08:00）——每天到这个点检查发送；由于每个客户只发一次，实际每个客户只会收到一次。\n" +
		"   b) 写日期+时间（如 2026-11-20 09:00 或 11-20 09:00）——只在那一时刻发送，适合提前把一周的计划一次排完。\n" +
		"   同一时间的多行客户会在到点时一起发出。已过期的日期会自动跳过（不补发）。\n" +
		"2. 客户名字：写客户称呼（如 David、王经理），程序自动拼成邮件开头的「Hi 名字,」（Hi 加空格、名字、英文逗号都由程序生成，正文里不用再写）。\n" +
		"3. 客户邮箱：收件邮箱，必填，格式要正确。\n" +
		"4. 抄送邮箱：这封邮件抄送给谁，可留空；多人用英文逗号分隔。另外每封邮件都会固定抄送软件里设置的地址。\n" +
		"5. 正文内容：从「Hi 名字,」之后写起，可分多段（单元格内 Alt+Enter 换行）。每个客户每行正文可以不同。\n" +
		"6. 配图：写图片文件名（如 poster.jpg），图片文件要和本软件放在同一个文件夹，可留空。图片会内嵌显示在正文底部（不是附件）。\n" +
		"7. 签名：每封邮件末尾固定带名片签名（Best Regards / logo / 联系方式 / 徽章），内容由 base_template.html 中的签名区控制。\n" +
		"8. 每个客户只发一次：发送过的客户记录在 任务表.state.json，即使软件一直开着也不会重复发送；全部发完后程序自动停止。想重发请删除该文件。\n" +
		"9. 修改任务表后保存即可，软件一分钟内自动生效，无需重启。\n" +
		"10. 请把本文件保存为 .xlsx 格式（WPS/Excel 另存为时选择 xlsx）。"
	f.NewSheet("填写说明")
	f.SetCellValue("填写说明", "A1", info)
	f.SetColWidth("填写说明", "A", "A", 110)

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("保存范本失败: %w", err)
	}
	return f.Close()
}

// TaskJSON mirrors TaskRow for the 任务.json task list (GUI-managed).
type TaskJSON struct {
	Date  string `json:"date"` // "2006-01-02" or "" for every day
	Time  string `json:"time"` // "HH:MM"
	Name  string `json:"name"`
	Email string `json:"email"`
	CC    string `json:"cc,omitempty"`
	Body  string `json:"body"`
	Image string `json:"image,omitempty"`
}

type tasksFile struct {
	Tasks []TaskJSON `json:"tasks"`
}

// ParseTasksJSON loads the GUI-managed 任务.json task list. Row numbers are
// synthetic (index+1) because there is no spreadsheet anymore.
func ParseTasksJSON(path string) ([]TaskRow, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("打开任务文件失败: %w", err)
	}
	var tf tasksFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return nil, nil, fmt.Errorf("任务文件格式错误: %w", err)
	}
	var (
		tasks []TaskRow
		notes []string
	)
	for i, t := range tf.Tasks {
		rowNum := i + 1
		email := strings.TrimSpace(t.Email)
		if email == "" {
			continue
		}
		if !parser.IsValidEmail(email) {
			notes = append(notes, fmt.Sprintf("第 %d 条邮箱格式无效，已跳过: %s", rowNum, email))
			continue
		}
		date, hm, ok := parseWhen(t.Date + " " + t.Time)
		if !ok {
			if hmOnly, ok2 := normalizeTime(t.Time); ok2 {
				date, hm, ok = "", hmOnly, true
			}
		}
		if !ok {
			notes = append(notes, fmt.Sprintf("第 %d 条时间无法识别: %q，已跳过", rowNum, t.Time))
			continue
		}
		if t.Date != "" && !validDate(t.Date) {
			notes = append(notes, fmt.Sprintf("第 %d 条日期无效: %s，已跳过", rowNum, t.Date))
			continue
		}
		if t.Date == "" {
			date = ""
		}
		tasks = append(tasks, TaskRow{
			Time:   hm,
			Date:   date,
			Name:   t.Name,
			Email:  email,
			CC:     t.CC,
			Body:   t.Body,
			Image:  t.Image,
			RowNum: rowNum,
		})
	}
	if len(tasks) == 0 {
		return nil, notes, fmt.Errorf("任务列表是空的")
	}
	return tasks, notes, nil
}

// ParseAny dispatches by file extension: .json for the GUI task list,
// anything else falls back to the spreadsheet parser.
func ParseAny(path string) ([]TaskRow, []string, error) {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return ParseTasksJSON(path)
	}
	return Parse(path)
}
