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
//
// 防重复语义：每个客户邮箱只发送一次（永久台账，跨天/跨重启有效）。
// 软件忘记关闭也绝不会重复发送；整张表发完后调度器自动停止。
type Scheduler struct {
	TaskbookPath        string   // path to 任务表.xlsx
	TemplatePath        string   // base HTML template (Hi {{.name}} + {{.body}} + signature)
	Subject             string   // email subject for taskbook sends
	GlobalCC            []string // fixed CC for every taskbook email
	FixedEmbeds         []string // signature images, referenced as cid:embed1..N
	SMTP                config.SMTPConfig
	statePath           string
	state               map[string]string // lowercase email -> "2006-01-02 15:04" send time
	completionAnnounced bool
}

// NewScheduler prepares the scheduler and loads its anti-resend ledger
// (任务表.state.json next to the spreadsheet). Old date-keyed state files
// from the previous daily-loop design are discarded: the semantics changed.
func NewScheduler(taskbookPath, templatePath, subject string, globalCC, fixedEmbeds []string, smtp config.SMTPConfig) *Scheduler {
	s := &Scheduler{
		TaskbookPath: taskbookPath,
		TemplatePath: templatePath,
		Subject:      subject,
		GlobalCC:     globalCC,
		FixedEmbeds:  fixedEmbeds,
		SMTP:         smtp,
		statePath:    filepath.Join(filepath.Dir(taskbookPath), "任务表.state.json"),
		state:        map[string]string{},
	}
	if raw, err := os.ReadFile(s.statePath); err == nil {
		_ = json.Unmarshal(raw, &s.state)
	}
	// Drop anything that does not look like the per-email ledger.
	for k, v := range s.state {
		if !strings.Contains(k, "@") || v == "" {
			delete(s.state, k)
		}
	}
	if len(s.state) > 0 {
		log.Printf("[任务表] 已加载发送台账：%d 个客户此前已发送，将自动跳过", len(s.state))
	}
	return s
}

func (s *Scheduler) markSent(email string, when time.Time) {
	s.state[strings.ToLower(strings.TrimSpace(email))] = when.Format("2006-01-02 15:04")
	if raw, err := json.MarshalIndent(s.state, "", "  "); err == nil {
		_ = os.WriteFile(s.statePath, raw, 0o644)
	}
}

func (s *Scheduler) isSent(email string) bool {
	_, ok := s.state[strings.ToLower(strings.TrimSpace(email))]
	return ok
}

// Run blocks until ctx is cancelled or every row of the spreadsheet has been
// sent (auto-stop). Re-reading the file each tick means customers can edit it
// live; once every customer is in the ledger the scheduler shuts down so a
// forgotten open window can never resend anything.
func (s *Scheduler) Run(ctx context.Context) error {
	log.Printf("[任务表] 调度已启动，每分钟检查 %s（修改后保存即自动生效；每个客户只发一次，发完自动停止）", filepath.Base(s.TaskbookPath))
	for {
		now := time.Now()
		done, err := s.tick(now)
		if err != nil {
			log.Printf("[任务表] %v", err)
		}
		if done {
			log.Printf("[任务表] ✅ 任务表中的客户已全部发送完毕，调度自动停止。")
			log.Printf("[任务表] 给新客户发送：请在任务表里填入新客户并保存，然后重新点「启动」。")
			return nil
		}
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			log.Printf("[任务表] 调度已停止（已发送的客户不会重复发送）")
			return nil
		case <-time.After(next.Sub(time.Now())):
		}
	}
}

// tick returns done=true when the spreadsheet is non-empty and every customer
// in it has already been sent.
func (s *Scheduler) tick(now time.Time) (bool, error) {
	tasks, notes, err := Parse(s.TaskbookPath)
	if err != nil {
		return false, err
	}
	for _, n := range notes {
		log.Printf("[任务表] %s", n)
	}
	if len(tasks) == 0 {
		return false, nil
	}

	key := now.Format("15:04")
	var due []TaskRow
	pending := 0
	for _, t := range tasks {
		if s.isSent(t.Email) {
			continue // 永久去重：发过的客户绝不再发
		}
		pending++
		if t.Time == key {
			due = append(due, t)
		}
	}
	if pending == 0 {
		// Every customer in the table has been sent; announce once and stop.
		if !s.completionAnnounced {
			s.completionAnnounced = true
			return true, nil
		}
		return true, nil
	}
	if len(due) == 0 {
		return false, nil
	}
	s.sendBatch(due, key)
	return false, nil
}

// RunBatch sends the batch for the given HH:MM right now (the "立即测试" path).
// Already-sent customers inside the batch are skipped and reported.
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
	skipped := 0
	for _, row := range tasks {
		if row.Time != t {
			continue
		}
		if s.isSent(row.Email) {
			skipped++
			log.Printf("[任务表] 跳过 %s：该客户此前已发送（每个客户只发一次）", row.Email)
			continue
		}
		due = append(due, row)
	}
	if len(due) == 0 {
		if skipped > 0 {
			return fmt.Errorf("%s 批次的客户此前均已发送过（每个客户只发一次）；如需重发请删除 任务表.state.json 后重试", t)
		}
		return fmt.Errorf("任务表里没有 %s 的任务", t)
	}
	s.sendBatch(due, t)
	return nil
}

// sendBatch renders and dispatches one time-group. Every row becomes one
// personalized email; row images are embedded after the fixed signature
// images so the template can hard-code cid:embed1/embed2 for the signature.
//
// Returns true when the SMTP probe succeeded, meaning the batch was genuinely
// attempted and every customer may be marked as sent. A probe failure (network
// down, auth broken) returns false so nothing is marked and the batch is
// retried on a later tick instead of being silently lost.
func (s *Scheduler) sendBatch(rows []TaskRow, key string) bool {
	folder := filepath.Dir(s.TaskbookPath)
	log.Printf("[任务表] %s 到点，开始发送 %d 封…", key, len(rows))

	// SMTP probe: only touch the ledger when we know the server is reachable,
	// so a morning without network cannot silently swallow the whole batch.
	probe, err := email.ConnectSMTP(s.SMTP)
	if err != nil {
		log.Printf("[任务表] SMTP 暂不可达（%v），本批次稍后自动重试，不会标记已发送", err)
		return false
	}
	probe.Close()

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
		return true
	}

	email.SetRetryLimit(2)
	result := email.StartDispatcher(tasks, s.SMTP, 1, 1, &email.DispatchOptions{})
	now := time.Now()
	for _, t := range tasks {
		s.markSent(t.Recipient.Email, now)
	}
	log.Printf("[任务表] %s 批次完成：成功 %d，失败 %d（失败明细见 failed.csv；已发送客户写入防重台账）", key, result.Sent, result.Failed)
	return true
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
