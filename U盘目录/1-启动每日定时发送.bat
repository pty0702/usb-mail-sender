@echo off
title 客户邮件每日定时发送 - 请保持本窗口开启
cd /d "%~dp0"
echo ==================================================
echo   客户邮件每日定时发送器
echo   计划：每天 09:00 自动发送客户邮件
echo   提示：请保持本窗口开启（可最小化）
echo         关闭窗口或按 Ctrl+C 即停止定时
echo ==================================================
echo.
mailgrid.exe --scheduler-run --env config.json --csv recipients.csv --template template.html --embed poster.jpg,logo.png,badge.gif --signature 签名.txt --subject "TEST 2026" --cc "sue.s@pak-eco.com,1430404507@qq.com" --cron "0 9 * * *" --concurrency 1 --batch-size 1 --retries 2
echo.
echo 定时器已停止。
pause
