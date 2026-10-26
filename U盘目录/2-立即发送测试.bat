@echo off
title 立即发送测试（group=A 的客户）
cd /d "%~dp0"
echo 即刻向 recipients.csv 中 group=A 的客户发送邮件（不等待定时）...
echo.
mailgrid.exe --env config.json --csv recipients.csv --template template.html --embed poster.jpg,logo.png,badge.gif --signature 签名.txt --subject "TEST 2026" --cc "sue.s@pak-eco.com,1430404507@qq.com" --filter "group == \"A\"" --concurrency 1 --retries 2
echo.
echo 发送结束。发送失败的收件人记录见 failed.csv。
pause
