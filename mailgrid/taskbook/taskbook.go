// Package taskbook implements the Excel task table mode: customers describe
// what to send and when in a spreadsheet, and the scheduler delivers each
// time-group through the regular mailgrid sending pipeline.
package taskbook

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bravo1goingdark/mailgrid/parser"
	"github.com/xuri/excelize/v2"
)

// TaskRow is one row of the task spreadsheet: one recipient, one send.
type TaskRow struct {
	Time   string // "HH:MM" local time, matched every day
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

var timeRe = regexp.MustCompile(`^\s*(\d{1,2})\s*[:：]\s*(\d{2})\s*$`)

// normalizeTime accepts a cell that is exactly a time ("8:00", "08：30") and
// rejects prose cells — hint text containing examples like "如 08:00" must not
// be mistaken for a send time.
func normalizeTime(cell string) (string, bool) {
	m := timeRe.FindStringSubmatch(cell)
	if m == nil {
		return "", false
	}
	h, mm := m[1], m[2]
	if len(h) == 1 {
		h = "0" + h
	}
	return h + ":" + mm, true
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
		t, timeOK := normalizeTime(cell(row, "time"))
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
			notes = append(notes, fmt.Sprintf("第 %d 行时间无法识别（应为 HH:MM，如 08:00），已跳过", rowNum))
			continue
		}
		if h, m := t[:2], t[3:]; h > "23" || m > "59" {
			notes = append(notes, fmt.Sprintf("第 %d 行时间超出范围: %s，已跳过", rowNum, t))
			continue
		}
		tasks = append(tasks, TaskRow{
			Time:   t,
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
		"几点发，如 08:00（每天到这个时间自动发）",
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
	widths := []float64{10, 14, 26, 22, 60, 16}
	for i, w := range widths {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(sheet, colName, colName, w)
	}

	// Instruction sheet, same content spirit as the customer's original
	// requirement table (正文1~正文6).
	const info = "任务表填写说明\n\n" +
		"1. 时间：每天几点发送，格式 HH:MM（如 08:00、14:30）。同一时间的多行客户会在到点时一起发出，每天到点都会重复发送。\n" +
		"2. 客户名字：写客户称呼（如 David、王经理），程序自动拼成邮件开头的「Hi 名字,」（Hi 加空格、名字、英文逗号都由程序生成，正文里不用再写）。\n" +
		"3. 客户邮箱：收件邮箱，必填，格式要正确。\n" +
		"4. 抄送邮箱：这封邮件抄送给谁，可留空；多人用英文逗号分隔。另外每封邮件都会固定抄送软件里设置的地址。\n" +
		"5. 正文内容：从「Hi 名字,」之后写起，可分多段（单元格内 Alt+Enter 换行）。每个客户每行正文可以不同。\n" +
		"6. 配图：写图片文件名（如 poster.jpg），图片文件要和本软件放在同一个文件夹，可留空。图片会内嵌显示在正文底部（不是附件）。\n" +
		"7. 签名：每封邮件末尾固定带名片签名（Best Regards / logo / 联系方式 / 徽章），内容由 template.html 中的签名区控制。\n" +
		"8. 修改任务表后保存即可，软件一分钟内自动生效，无需重启。\n" +
		"9. 请把本文件保存为 .xlsx 格式（WPS/Excel 另存为时选择 xlsx）。"
	f.NewSheet("填写说明")
	f.SetCellValue("填写说明", "A1", info)
	f.SetColWidth("填写说明", "A", "A", 110)

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("保存范本失败: %w", err)
	}
	return f.Close()
}
