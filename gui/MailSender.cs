// 邮件定时发送器 —— 客户邮件任务表工具的图形界面。
// 只是薄壳：启停 mailgrid.exe 的任务表调度进程，收集输出到日志框。
// 启动前先做卡密在线验证（LICENSE_INTEGRATION_MODE=server-verify-only，
// 无本地时间凭证：不保存卡密、不缓存成功状态，网络失败一律拒绝进入）。
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Net;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Windows.Forms;

namespace MailSenderGui
{
    internal static class License
    {
        public const string ServerUrl = "http://82.157.64.196:8848";
        // 简单模式兼容 HMAC 密钥：仅用于验证服务器响应，可能被逆向提取，
        // 不拥有任何管理权限（skill 边界）。
        public const string HmacSecret = "fcd83908ed4547412b6089f9044bef931de3cfe64fa170111bcec7c92accb8f4";

        public static string MachineId()
        {
            object guid = Microsoft.Win32.Registry.GetValue(
                @"HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Cryptography", "MachineGuid", null);
            string s = guid as string;
            if (!string.IsNullOrEmpty(s)) return s;
            return Environment.MachineName + "-" + Environment.UserName;
        }

        // 结果对象：Ok=true 时含到期时间；失败时 Message 为可直接展示的中文。
        public class VerifyResult
        {
            public bool Ok;
            public string Message;
            public DateTime? ExpireAt;
        }

        public static VerifyResult Verify(string key)
        {
            key = (key ?? "").Trim();
            if (key.Length == 0)
                return new VerifyResult { Ok = false, Message = "请输入卡密。" };

            string payload = "{\"key\":\"" + EscapeJson(key) + "\",\"machine_id\":\"" +
                             EscapeJson(MachineId()) + "\"}";
            byte[] body = Encoding.UTF8.GetBytes(payload);

            try
            {
                HttpWebRequest req = (HttpWebRequest)WebRequest.Create(ServerUrl + "/api/verify");
                req.Method = "POST";
                req.ContentType = "application/json";
                req.Timeout = 15000;
                req.ReadWriteTimeout = 15000;
                using (var stream = req.GetRequestStream()) stream.Write(body, 0, body.Length);
                using (var resp = (HttpWebResponse)req.GetResponse())
                using (var ms = new MemoryStream())
                {
                    resp.GetResponseStream().CopyTo(ms);
                    byte[] raw = ms.ToArray();
                    // 成功响应必须验签：以服务器原始字节恒定时间比较 X-Sig。
                    var sigCheck = CheckSignature(raw, resp.Headers["X-Sig"],
                        Encoding.UTF8.GetString(raw));
                    if (sigCheck != null) return sigCheck;
                    return ParseResponse(Encoding.UTF8.GetString(raw));
                }
            }
            catch (WebException ex)
            {
                // 业务失败也可能以 HTTP 4xx/5xx 返回：读取错误体解析。
                try
                {
                    var resp = ex.Response as HttpWebResponse;
                    if (resp != null)
                    {
                        using (var sr = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                            return ParseResponse(sr.ReadToEnd());
                    }
                }
                catch { }
                return new VerifyResult { Ok = false, Message = "无法连接授权服务器，请检查网络后重试。" };
            }
            catch
            {
                return new VerifyResult { Ok = false, Message = "无法连接授权服务器，请检查网络后重试。" };
            }
        }

        // 返回 null 表示签名有效、可继续 ParseResponse；否则返回拒绝结果。
        static VerifyResult CheckSignature(byte[] rawBody, string sigHeader, string bodyText)
        {
            if (sigHeader == null || !System.Text.RegularExpressions.Regex.IsMatch(
                    sigHeader, "^[0-9a-fA-F]{64}$"))
                return new VerifyResult { Ok = false, Message = "授权响应签名无效，拒绝进入。" };

            byte[] expected = new HMACSHA256(Encoding.UTF8.GetBytes(HmacSecret)).ComputeHash(rawBody);
            byte[] received = HexToBytes(sigHeader);
            if (received.Length != expected.Length) return new VerifyResult { Ok = false, Message = "授权响应签名无效，拒绝进入。" };
            int diff = 0;
            for (int i = 0; i < expected.Length; i++) diff |= expected[i] ^ received[i];
            if (diff != 0)
                return new VerifyResult { Ok = false, Message = "授权响应签名无效，拒绝进入。" };
            return null;
        }

        static VerifyResult ParseResponse(string raw)
        {
            if (string.IsNullOrEmpty(raw))
                return new VerifyResult { Ok = false, Message = "授权服务器返回为空，拒绝进入。" };

            var dict = ParseJson(raw);
            if (dict == null)
                return new VerifyResult { Ok = false, Message = "授权服务器响应格式错误，拒绝进入。" };

            bool ok = GetField(dict, "ok") == "True" || GetField(dict, "ok") == "true";
            if (!ok)
            {
                string err = GetField(dict, "error");
                return new VerifyResult
                {
                    Ok = false,
                    Message = string.IsNullOrEmpty(err) ? "卡密验证失败。" : "验证失败：" + err,
                };
            }

            long expire;
            long.TryParse(GetField(dict, "expire_at"), out expire);
            if (expire <= 0)
                return new VerifyResult { Ok = false, Message = "授权服务器响应缺少有效期，拒绝进入。" };
            if (expire <= EpochNow())
                return new VerifyResult { Ok = false, Message = "验证失败：卡密已过期。" };

            return new VerifyResult { Ok = true, ExpireAt = EpochToDateTime(expire) };
        }

        static long EpochNow()
        {
            return (long)(DateTime.UtcNow - new DateTime(1970, 1, 1)).TotalSeconds;
        }

        static DateTime EpochToDateTime(long epoch)
        {
            return new DateTime(1970, 1, 1).AddSeconds(epoch).ToLocalTime();
        }

        static string EscapeJson(string s)
        {
            var sb = new StringBuilder();
            foreach (char c in s)
            {
                if (c == '"' || c == '\\') { sb.Append('\\'); sb.Append(c); }
                else if (c < 32) sb.AppendFormat("\\u{0:x4}", (int)c);
                else sb.Append(c);
            }
            return sb.ToString();
        }

        static byte[] HexToBytes(string hex)
        {
            var bytes = new byte[hex.Length / 2];
            for (int i = 0; i < bytes.Length; i++)
                bytes[i] = Convert.ToByte(hex.Substring(i * 2, 2), 16);
            return bytes;
        }

        // 极简扁平 JSON 对象解析（服务端响应都是一层 string/number/bool）。
        static Dictionary<string, string> ParseJson(string json)
        {
            try
            {
                var serializer = new System.Web.Script.Serialization.JavaScriptSerializer();
                var obj = serializer.Deserialize<Dictionary<string, object>>(json);
                if (obj == null) return null;
                var result = new Dictionary<string, string>();
                foreach (var kv in obj) result[kv.Key] = kv.Value == null ? "" : kv.Value.ToString();
                return result;
            }
            catch { return null; }
        }

        static string GetField(Dictionary<string, string> dict, string key)
        {
            string v;
            return dict.TryGetValue(key, out v) ? v : "";
        }
    }

    // 启动卡密验证窗口：通过前不出现任何业务功能。
    public class LicenseForm : Form
    {
        TextBox keyBox;
        Button okBtn;
        Label status;

        public MainForm NextForm { get; private set; }

        public LicenseForm()
        {
            Text = "客户邮件定时发送器 · 授权验证 by 西塔 | ptyo7o2";
            Font = new Font("微软雅黑", 9F);
            Size = new Size(420, 230);
            StartPosition = FormStartPosition.CenterScreen;
            FormBorderStyle = FormBorderStyle.FixedDialog;
            MaximizeBox = false; MinimizeBox = false;

            var label = new Label { Text = "请输入卡密（见随包 卡密.txt）：", Left = 20, Top = 18, Width = 360 };
            keyBox = new TextBox { Left = 20, Top = 44, Width = 350, Font = new Font("Consolas", 11F) };
            okBtn = new Button { Text = "验证并进入", Left = 20, Top = 78, Width = 350, Height = 32 };
            okBtn.Click += OnVerify;
            status = new Label { Left = 20, Top = 118, Width = 360, Height = 40, ForeColor = Color.Gray };

            Controls.Add(label);
            Controls.Add(keyBox);
            Controls.Add(okBtn);
            Controls.Add(status);
            AcceptButton = okBtn;
        }

        void OnVerify(object sender, EventArgs e)
        {
            string key = keyBox.Text;
            okBtn.Enabled = false;
            status.ForeColor = Color.Gray;
            status.Text = "正在验证…";

            new Thread(() =>
            {
                var result = License.Verify(key);
                BeginInvoke((Action)(() =>
                {
                    okBtn.Enabled = true;
                    if (result.Ok)
                    {
                        status.ForeColor = Color.Green;
                        status.Text = "验证通过，正在进入…";
                        NextForm = new MainForm();
                        NextForm.Text += "（授权至 " + result.ExpireAt.Value.ToString("yyyy-MM-dd HH:mm") + "）";
                        DialogResult = DialogResult.OK;
                        Close();
                    }
                    else
                    {
                        status.ForeColor = Color.Red;
                        status.Text = result.Message;
                    }
                }));
            }) { IsBackground = true }.Start();
        }
    }

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
            Text = "客户邮件定时发送器 by 西塔 | ptyo7o2";
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
            startBtn = MakeButton("▶ 启动定时发送", StartClicked);
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
                            " --env \"" + Path.Combine(ExeDir, "smtp.json") + "\"" +
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
                            " --env \"" + Path.Combine(ExeDir, "smtp.json") + "\"" +
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
            // 先卡密验证：明确成功才进入主界面；失败/网络不通一律退出。
            var license = new LicenseForm();
            if (license.ShowDialog() != DialogResult.OK || license.NextForm == null) return;
            Application.Run(license.NextForm);
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
