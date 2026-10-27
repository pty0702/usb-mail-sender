package taskbook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bravo1goingdark/mailgrid/config"
	"github.com/bravo1goingdark/mailgrid/email"
	"github.com/bravo1goingdark/mailgrid/parser"
	"github.com/bravo1goingdark/mailgrid/utils"
)

// Scheduler watches the task spreadsheet once a minute and delivers each
// due time-group through the regular mailgrid dispatch pipeline.
type Scheduler struct {
	TaskbookPath string   // path to 任务表.xlsx
	TemplatePath string   // base HTML template (Hi {{.name}} + {{.body}} + signature)
	Subject      string   // email subject for taskbook sends
	GlobalCC     []string // fixed CC for every taskbook email
	FixedEmbeds  []string // signature images, referenced as cid:embed1..N
	SMTP         config.SMTPConfig
	statePath    string
	state        map[string]map[string]bool // date -> "HH:MM" -> sent
}

// NewScheduler prepares the scheduler and loads its anti-resend state file
// (任务表.state.json next to the spreadsheet).
func NewScheduler(taskbookPath, templatePath, subject string, globalCC, fixedEmbeds []string, smtp config.SMTPConfig) *Scheduler {
	s := &Scheduler{
		TaskbookPath: taskbookPath,
		TemplatePath: templatePath,
		Subject:      subject,
		GlobalCC:     globalCC,
		FixedEmbeds:  fixedEmbeds,
		SMTP:         smtp,
		statePath:    filepath.Join(filepath.Dir(taskbookPath), "任务表.state.json"),
		state:        map[string]map[string]bool{},
	}
	if raw, err := os.ReadFile(s.statePath); err == nil {
		_ = json.Unmarshal(raw, &s.state)
	}
	// Drop records older than 7 days so the file stays tiny.
	cutoff := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	for d := range s.state {
		if d < cutoff {
			delete(s.state, d)
		}
	}
	return s
}

func (s *Scheduler) markDone(date, key string) {
	if s.state[date] == nil {
		s.state[date] = map[string]bool{}
	}
	s.state[date][key] = true
	if raw, err := json.Marshal(s.state); err == nil {
		_ = os.WriteFile(s.statePath, raw, 0o644)
	}
}

func (s *Scheduler) isDone(date, key string) bool {
	return s.state[date] != nil && s.state[date][key]
}

// Run blocks until ctx is cancelled, checking the spreadsheet at every whole
// minute. Re-reading the file each tick means customers can edit it live.
func (s *Scheduler) Run(ctx context.Context) error {
	log.Printf("[任务表] 调度已启动，每分钟检查 %s（修改后保存即自动生效）", filepath.Base(s.TaskbookPath))
	for {
		now := time.Now()
		s.tick(now)
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			log.Printf("[任务表] 调度已停止")
			return nil
		case <-time.After(next.Sub(time.Now())):
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	tasks, notes, err := Parse(s.TaskbookPath)
	if err != nil {
		log.Printf("[任务表] 读取跳过: %v", err)
		return
	}
	for _, n := range notes {
		log.Printf("[任务表] %s", n)
	}

	date, key := now.Format("2006-01-02"), now.Format("15:04")
	if s.isDone(date, key) {
		return
	}
	var due []TaskRow
	for _, t := range tasks {
		if t.Time == key {
			due = append(due, t)
		}
	}
	if len(due) == 0 {
		return
	}
	s.sendBatch(due, key)
	// Mark the batch done even if some emails failed (they are retried at the
	// email level and recorded in failed.csv); otherwise a hard SMTP failure
	// would re-send the whole batch every minute.
	s.markDone(date, key)
}

// RunBatch sends the batch for the given HH:MM right now (the "立即测试" path).
func (s *Scheduler) RunBatch(timeStr string) error {
	t, ok := normalizeTime(timeStr)
	if !ok {
		return fmt.Errorf("时间格式应为 HH:MM，如 08:00（收到 %q）", timeStr)
	}
	tasks, notes, err := Parse(s.TaskbookPath)
	if err != nil {
		return err
	}
	for _, n := range notes {
		log.Printf("[任务表] %s", n)
	}
	var due []TaskRow
	for _, row := range tasks {
		if row.Time == t {
			due = append(due, row)
		}
	}
	if len(due) == 0 {
		return fmt.Errorf("任务表里没有 %s 的任务", t)
	}
	s.sendBatch(due, t)
	s.markDone(time.Now().Format("2006-01-02"), t)
	return nil
}

// sendBatch renders and dispatches one time-group. Every row becomes one
// personalized email; row images are embedded after the fixed signature
// images so the template can hard-code cid:embed1/embed2 for the signature.
func (s *Scheduler) sendBatch(rows []TaskRow, key string) {
	folder := filepath.Dir(s.TaskbookPath)
	log.Printf("[任务表] %s 到点，开始发送 %d 封…", key, len(rows))

	var tasks []email.Task
	for _, row := range rows {
		embeds := append([]string{}, s.FixedEmbeds...)
		bodyHTML, rowImages := s.buildBody(row, folder, len(embeds)+1)
		for _, img := range rowImages {
			if abs, err := filepath.Abs(filepath.Join(folder, img)); err == nil {
				embeds = append(embeds, abs)
			}
		}

		// The task body is pre-escaped, trusted HTML (buildBody); render it
		// through html/template with template.HTML so {{.body}} is inserted
		// verbatim while {{.name}} stays escaped against injection.
		tmpl, err := utils.LoadTemplate(s.TemplatePath)
		if err != nil {
			log.Printf("[任务表] 第 %d 行模板加载失败，已跳过 (%v)", row.RowNum, err)
			continue
		}
		var rendered bytes.Buffer
		renderData := map[string]any{
			"email": row.Email,
			"name":  row.Name,
			"body":  template.HTML(bodyHTML),
		}
		if err := tmpl.Execute(&rendered, renderData); err != nil {
			log.Printf("[任务表] 第 %d 行模板渲染失败，已跳过 (%v)", row.RowNum, err)
			continue
		}
		body := rendered.String()

		rec := parser.Recipient{
			Email: row.Email,
			Data:  map[string]string{"name": row.Name, "body": bodyHTML},
		}

		tasks = append(tasks, email.Task{
			Recipient: rec,
			Subject:   s.Subject,
			Body:      body,
			Embeds:    embeds,
			CC:        mergeCC(s.GlobalCC, row.CC),
			Retries:   0,
			Index:     len(tasks),
		})
	}
	if len(tasks) == 0 {
		return
	}

	email.SetRetryLimit(2)
	result := email.StartDispatcher(tasks, s.SMTP, 1, 1, &email.DispatchOptions{})
	log.Printf("[任务表] %s 批次完成：成功 %d，失败 %d（失败明细见 failed.csv）", key, result.Sent, result.Failed)
}

// buildBody escapes the plain-text body to HTML and appends inline images as
// cid references starting at imageStart (after the fixed signature images).
func (s *Scheduler) buildBody(row TaskRow, folder string, imageStart int) (string, []string) {
	var sb strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(row.Body, "\r\n", "\n"), "\n") {
		sb.WriteString(html.EscapeString(line))
		sb.WriteString("<br>\n")
	}
	var images []string
	n := imageStart
	for _, img := range strings.Split(row.Image, ",") {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(folder, img)); err != nil {
			log.Printf("[任务表] 第 %d 行配图不存在，已跳过: %s", row.RowNum, img)
			continue
		}
		images = append(images, img)
		fmt.Fprintf(&sb, "<img src=\"cid:embed%d\" border=\"0\" style=\"max-width:892px;width:100%%;height:auto;display:block;margin-top:16px;\">\n", n)
		n++
	}
	return sb.String(), images
}

func mergeCC(global []string, rowCC string) []string {
	merged := append([]string{}, global...)
	for _, addr := range strings.Split(rowCC, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			merged = append(merged, addr)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}
