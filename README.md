# 定时发送邮件（腾讯企业邮箱 · 任务表驱动 · U 盘便携版）

基于开源项目 [mailgrid](https://github.com/bravo1goingdark/mailgrid)（BSD-3）二次开发的客户邮件定时群发工具。客户在 **Excel 任务表**里写"几点、给谁、发什么内容"，软件到点自动发送。

## 功能特性

- **任务表驱动**：任务表.xlsx 定义 时间/客户名字/邮箱/抄送/正文/配图，到点自动发送；**每个客户只发一次**（按邮箱永久台账去重，忘关软件也不会重发），全部发完自动停止
- **图形界面**：邮件定时发送器.exe（WinForms 壳，启停调度/生成范本/打开任务表/立即测试/实时日志）
- **一键范本**：`--taskbook-template` 生成带灰色填写提示、示例行和说明页的 xlsx 范本
- 本地运行：腾讯企业邮箱 SMTP（smtp.exmail.qq.com，SSL 465，config `"ssl": true` 隐式 TLS）
- HTML 模板：`Hi {{.name}},` 开头 + 任务表正文 + 固定名片签名（logo/联系方式/徽章内嵌）
- **内嵌配图**：`--embed` 正文内嵌（multipart/related + cid），任务表模式支持按行配图
- **签名文件**：`--signature` 纯文本追加正文底部，UTF-8/GBK 自动识别
- **按行抄送**：CSV/任务表的 `cc` 列每客户独立抄送，与全局 `--cc` 叠加
- 修复调度器生命周期（cron 注册后进程常驻）；并发限速、失败重试、断点续发、去重、failed.csv
- 工具文件夹整体便携（U 盘/任意位置），换电脑拷走即用；交付为 zip 微信发送

## 目录结构

- `mailgrid/` — mailgrid 源码（含本项目修改：embed/signature/ssl/scheduler-fix/taskbook 包）
- `gui/` — WinForms 界面源码（MailSender.cs，csc 编译为 邮件定时发送器.exe）
- `U盘目录/` — 交付给客户的工具文件夹（GUI + 引擎 + 配置 + 任务表范本 + 图 + 说明）
- `邮件发送工具.zip` — 上述文件夹打包（微信直接发送；含真实授权码，已被 .gitignore 排除）

## 编译

```powershell
# Go 1.19+（发送引擎）
cd mailgrid; go build -o mailgrid.exe ./cmd/mailgrid

# .NET Framework 4 自带 csc（图形界面）
C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe -nologo -target:winexe `
  -codepage:65001 -out:U盘目录\邮件定时发送器.exe `
  -r:System.dll -r:System.Drawing.dll -r:System.Windows.Forms.dll gui\MailSender.cs
```

## 客户使用流程

见 [U盘目录/使用说明.md](U盘目录/使用说明.md)：生成任务表范本 → Excel 填写 → 点启动，到点自动发。
