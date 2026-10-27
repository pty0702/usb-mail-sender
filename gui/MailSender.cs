// 邮件定时发送器 —— 客户邮件任务表工具的图形界面。
// 只是薄壳：启停 mailgrid.exe 的任务表调度进程，收集输出到日志框。
using System;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;

namespace MailSenderGui
{
    public class MainForm : Form
    {
        Process worker;                 // mailgrid.exe 任务表调度进程
        TextBox logBox;
        Button startBtn, stopBtn, tplBtn, openBtn, testBtn;
        Label statusLabel;
        TextBox subjectBox, ccBox;

        string ExeDir { get { return AppDomain.CurrentDomain.BaseDirectory; } }
        string Mailgrid { get { return Path.Combine(ExeDir, "mailgrid.exe"); } }
        string Taskbook { get { return Path.Combine(ExeDir, "任务表.xlsx"); } }
        string Template { get { return Path.Combine(ExeDir, "base_template.html"); } }

        public MainForm()
        {
            Text = "客户邮件定时发送器";
            Font = new Font("微软雅黑", 9F);
            Size = new Size(760, 560);
            StartPosition = FormStartPosition.CenterScreen;
            FormClosing += OnClosing;

            var top = new FlowLayoutPanel
            {
                Dock = DockStyle.Top,
                Height = 40,
                Padding = new Padding(8, 8, 0, 0),
            };
            statusLabel = new Label
            {
                Text = "状态：未运行",
                Width = 200,
                ForeColor = Color.Gray,
                TextAlign = ContentAlignment.MiddleLeft,
            };
            top.Controls.Add(statusLabel);

            var cfg = new FlowLayoutPanel
            {
                Dock = DockStyle.Top,
                Height = 66,
                Padding = new Padding(8, 4, 0, 0),
            };
            cfg.Controls.Add(new Label { Text = "邮件主题：", AutoSize = true, Margin = new Padding(3, 6, 0, 0) });
            subjectBox = new TextBox { Width = 180, Text = "TEST 2026" };
            cfg.Controls.Add(subjectBox);
            cfg.Controls.Add(new Label { Text = "  每封固定抄送：", AutoSize = true, Margin = new Padding(3, 6, 0, 0) });
            ccBox = new TextBox { Width = 330, Text = "sue@pak-eco.com,1430404507@qq.com" };
            cfg.Controls.Add(ccBox);

            var buttons = new FlowLayoutPanel
            {
                Dock = DockStyle.Top,
                Height = 48,
                Padding = new Padding(8, 8, 0, 0),
            };
            startBtn = MakeButton("▶ 启动每日定时", StartClicked);
            stopBtn = MakeButton("■ 停止", StopClicked); stopBtn.Enabled = false;
            tplBtn = MakeButton("生成任务表范本", TemplateClicked);
            openBtn = MakeButton("打开任务表", OpenClicked);
            testBtn = MakeButton("立即测试发送…", TestClicked);
            buttons.Controls.Add(startBtn);
            buttons.Controls.Add(stopBtn);
            buttons.Controls.Add(tplBtn);
            buttons.Controls.Add(openBtn);
            buttons.Controls.Add(testBtn);

            logBox = new TextBox
            {
                Multiline = true,
                ReadOnly = true,
                ScrollBars = ScrollBars.Vertical,
                Dock = DockStyle.Fill,
                Font = new Font("Consolas", 9F),
                BackColor = Color.White,
            };

            Controls.Add(logBox);
            Controls.Add(buttons);
            Controls.Add(cfg);
            Controls.Add(top);
            AppendLog("提示：先点「生成任务表范本」填写时间和客户，再点「启动每日定时」。");
        }

        Button MakeButton(string text, EventHandler onClick)
        {
            var b = new Button { Text = text, Width = 132, Height = 30 };
            b.Click += onClick;
            return b;
        }

        void AppendLog(string line)
        {
            if (InvokeRequired)
            {
                BeginInvoke((Action)(() => AppendLog(line)));
                return;
            }
            logBox.AppendText("[" + DateTime.Now.ToString("HH:mm:ss") + "] " + line + Environment.NewLine);
        }

        void StartClicked(object sender, EventArgs e)
        {
            if (worker != null && !worker.HasExited) return;
            if (!File.Exists(Mailgrid)) { Warn("找不到 mailgrid.exe，请把它和本程序放在同一文件夹。"); return; }
            if (!File.Exists(Taskbook)) { Warn("还没有任务表：请先点「生成任务表范本」，填写后保存为 任务表.xlsx。"); return; }

            var psi = new ProcessStartInfo
            {
                FileName = Mailgrid,
                Arguments = "--taskbook \"" + Taskbook + "\" --template \"" + Template + "\"" +
                            " --env \"" + Path.Combine(ExeDir, "config.json") + "\"" +
                            " --subject \"" + subjectBox.Text.Trim() + "\"" +
                            " --cc \"" + ccBox.Text.Trim() + "\"",
                WorkingDirectory = ExeDir,
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true,
                StandardOutputEncoding = Encoding.UTF8,
                StandardErrorEncoding = Encoding.UTF8,
            };
            worker = new Process { StartInfo = psi, EnableRaisingEvents = true };
            worker.OutputDataReceived += (s, ev) => { if (ev.Data != null) AppendLog(ev.Data); };
            worker.ErrorDataReceived += (s, ev) => { if (ev.Data != null) AppendLog(ev.Data); };
            worker.Exited += (s, ev) => AppendLog("定时程序已退出。");
            worker.Start();
            worker.BeginOutputReadLine();
            worker.BeginErrorReadLine();

            statusLabel.Text = "状态：运行中（关窗即停止）";
            statusLabel.ForeColor = Color.Green;
            startBtn.Enabled = false; stopBtn.Enabled = true;
            AppendLog("已启动：到任务表设定的时间会自动发送；修改任务表保存后一分钟内生效。");
        }

        void StopClicked(object sender, EventArgs e)
        {
            StopWorker("已停止定时发送。");
        }

        void StopWorker(string reason)
        {
            if (worker == null || worker.HasExited)
            {
                SetStopped();
                return;
            }
            try { worker.Kill(); } catch { }
            worker.WaitForExit(3000);
            SetStopped();
            AppendLog(reason);
        }

        void SetStopped()
        {
            statusLabel.Text = "状态：未运行";
            statusLabel.ForeColor = Color.Gray;
            startBtn.Enabled = true; stopBtn.Enabled = false;
        }

        void TemplateClicked(object sender, EventArgs e)
        {
            try
            {
                var psi = new ProcessStartInfo
                {
                    FileName = Mailgrid,
                    Arguments = "--taskbook-template \"" + Taskbook + "\"",
                    WorkingDirectory = ExeDir,
                    UseShellExecute = false,
                    CreateNoWindow = true,
                };
                using (Process.Start(psi)) { }
                AppendLog("范本已生成：" + Taskbook);
                if (MessageBox.Show("范本已生成，现在用 Excel/WPS 打开填写吗？\n（同一时间多行客户会一起发送，详见范本里的「填写说明」）",
                    "生成成功", MessageBoxButtons.YesNo, MessageBoxIcon.Information) == DialogResult.Yes)
                {
                    OpenWithDefaultApp(Taskbook);
                }
            }
            catch (Exception ex) { Warn("生成范本失败：" + ex.Message); }
        }

        void OpenClicked(object sender, EventArgs e)
        {
            if (!File.Exists(Taskbook)) { Warn("还没有任务表，请先点「生成任务表范本」。"); return; }
            OpenWithDefaultApp(Taskbook);
        }

        void TestClicked(object sender, EventArgs e)
        {
            if (!File.Exists(Taskbook)) { Warn("还没有任务表，请先点「生成任务表范本」。"); return; }
            string time = Prompt.Show("要立即发送任务表里几点的那一批？（如 08:00）", "立即测试", "08:00");
            if (time == null) return;
            time = time.Trim();
            if (time == "") { Warn("时间不能为空。"); return; }

            var psi = new ProcessStartInfo
            {
                FileName = Mailgrid,
                Arguments = "--taskbook \"" + Taskbook + "\" --template \"" + Template + "\"" +
                            " --env \"" + Path.Combine(ExeDir, "config.json") + "\"" +
                            " --subject \"" + subjectBox.Text.Trim() + "\"" +
                            " --cc \"" + ccBox.Text.Trim() + "\"" +
                            " --taskbook-once \"" + time + "\"",
                WorkingDirectory = ExeDir,
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true,
            };
            AppendLog("立即测试：发送 " + time + " 批次…");
            ThreadPool.QueueUserWorkItem(_ =>
            {
                try
                {
                    using (var p = Process.Start(psi))
                    {
                        stringOut(p, () => p.StandardOutput.ReadLine(), s => AppendLog(s));
                        stringOut(p, () => p.StandardError.ReadLine(), s => AppendLog(s));
                        p.WaitForExit();
                        AppendLog("立即测试结束。");
                    }
                }
                catch (Exception ex) { AppendLog("立即测试失败：" + ex.Message); }
            });
        }

        void stringOut(Process p, Func<string> read, Action<string> write)
        {
            new Thread(() =>
            {
                try { string line; while ((line = read()) != null) write(line); }
                catch { }
            }) { IsBackground = true }.Start();
        }

        void OpenWithDefaultApp(string path)
        {
            try { Process.Start(new ProcessStartInfo(path) { UseShellExecute = true }); }
            catch (Exception ex) { Warn("打开失败：" + ex.Message); }
        }

        void Warn(string msg) { MessageBox.Show(msg, "提示", MessageBoxButtons.OK, MessageBoxIcon.Warning); }

        void OnClosing(object sender, FormClosingEventArgs e)
        {
            if (worker != null && !worker.HasExited)
            {
                var r = MessageBox.Show("定时发送正在运行，关闭窗口会停止今天的自动发送。\n确定退出吗？",
                    "确认退出", MessageBoxButtons.YesNo, MessageBoxIcon.Question);
                if (r != DialogResult.Yes) { e.Cancel = true; return; }
                try { worker.Kill(); } catch { }
            }
        }

        [STAThread]
        static void Main()
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.Run(new MainForm());
        }
    }

    // 极简输入对话框（避免引入 VisualBasic 依赖）。
    public static class Prompt
    {
        public static string Show(string text, string title, string defaultValue)
        {
            string result = null;
            var t = new Thread(() =>
            {
                var form = new Form
                {
                    Text = title,
                    Width = 360,
                    Height = 150,
                    FormBorderStyle = FormBorderStyle.FixedDialog,
                    StartPosition = FormStartPosition.CenterParent,
                    MaximizeBox = false, MinimizeBox = false,
                    Font = new Font("微软雅黑", 9F),
                };
                var label = new Label { Text = text, Dock = DockStyle.Top, Padding = new Padding(10, 12, 10, 0) };
                var box = new TextBox { Dock = DockStyle.Top, Text = defaultValue, Margin = new Padding(10, 8, 10, 0) };
                var ok = new Button { Text = "确定", DialogResult = DialogResult.OK, Left = 150, Top = 78, Width = 80 };
                var cancel = new Button { Text = "取消", DialogResult = DialogResult.Cancel, Left = 240, Top = 78, Width = 80 };
                form.Controls.Add(ok);
                form.Controls.Add(cancel);
                form.Controls.Add(box);
                form.Controls.Add(label);
                form.AcceptButton = ok;
                form.CancelButton = cancel;
                result = form.ShowDialog() == DialogResult.OK ? box.Text : null;
            });
            t.SetApartmentState(ApartmentState.STA);
            t.Start();
            t.Join();
            return result;
        }
    }
}
