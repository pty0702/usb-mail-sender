# 定时发送邮件（腾讯企业邮箱 · U 盘便携版）

基于开源项目 [mailgrid](https://github.com/bravo1goingdark/mailgrid)（BSD-3）二次开发的本地客户邮件定时群发工具。

## 功能特性

- 本地运行，不依赖云端：腾讯企业邮箱 SMTP（smtp.exmail.qq.com，SSL 465）
- 每天固定时间（cron 表达式）自动发送，支持按组轮换（`--filter`）
- HTML 模板 + `{{.name}}` 客户称呼占位符
- **内嵌配图**（`--embed`：正文内嵌显示，multipart/related + cid，支持多张）
- **签名文件**（`--signature`：纯文本签名自动追加正文底部，UTF-8/GBK 自动识别）
- **按行抄送**（CSV 的 `cc` 列每个客户可指定不同抄送人，与全局 `--cc` 叠加）
- 隐式 SSL 端口支持（config `"ssl": true`，适配 465 端口）
- 修复调度器生命周期（注册 cron 任务后进程常驻，Ctrl+C 优雅退出）
- 并发限速、失败重试、断点续发、去重、失败记录 failed.csv
- 工具文件夹整体便携（U 盘/任意位置），换电脑拷走文件夹即用；交付为 zip 微信发送

## 目录结构

- `mailgrid/` — mailgrid 源码（含本项目修改）
- `U盘目录/` — 交付给客户的工具文件夹（exe + 配置 + 模板 + 名单 + 签名 + bat 脚本 + 使用说明）
- `邮件发送工具.zip` — 上述文件夹的打包（微信直接发送此文件）
- `bat-src/` — bat 脚本的 UTF-8 源文件（交付版已转 GBK+CRLF 编码）

## 编译

```powershell
# 需 Go 1.19+
cd mailgrid
go build -o mailgrid.exe ./cmd/mailgrid
```

## 快速上手

见 [U盘目录/使用说明.md](U盘目录/使用说明.md)。
