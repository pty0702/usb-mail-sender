# 定时发送邮件（腾讯企业邮箱 · U 盘便携版）

基于开源项目 [mailgrid](https://github.com/bravo1goingdark/mailgrid)（BSD-3）二次开发的本地客户邮件定时群发工具。

## 功能特性

- 本地运行，不依赖云端：腾讯企业邮箱 SMTP（smtp.exmail.qq.com，SSL 465）
- 每天固定时间（cron 表达式）自动发送，支持按组轮换（`--filter`）
- HTML 模板 + `{{.name}}` 客户称呼占位符
- **内嵌配图**（本项目新增 `--embed`：正文底部内嵌显示，multipart/related + cid）
- 隐式 SSL 端口支持（本项目新增 config `"ssl": true`，适配 465 端口）
- 修复调度器生命周期（本项目新增：注册 cron 任务后进程常驻，Ctrl+C 优雅退出）
- 并发限速、失败重试、断点续发、去重、失败记录 failed.csv
- 全部配置/名单/模板/记录存 U 盘，换电脑即插即用

## 目录结构

- `mailgrid/` — mailgrid 源码（含本项目修改）
- `U盘目录/` — 交付给客户的 U 盘内容（exe + 配置 + 模板 + 名单 + bat 脚本 + 使用说明）
- `bat-src/` — bat 脚本的 UTF-8 源文件（交付版已转 GBK 编码）

## 编译

```powershell
# 需 Go 1.19+
cd mailgrid
go build -o mailgrid.exe ./cmd/mailgrid
```

## 快速上手

见 [U盘目录/使用说明.md](U盘目录/使用说明.md)。
