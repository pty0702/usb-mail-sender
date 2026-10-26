@echo off
title 预览邮件渲染效果（不实际发送）
cd /d "%~dp0"
mailgrid.exe --env config.json --csv recipients.csv --template template.html --embed poster.png --subject "Pak2Eco at PACK EXPO 2026 - Let's meet in Chicago!" --dry-run
echo.
echo 以上为每封邮件的渲染结果（未实际发送）。
echo Embeds 一行表示内嵌配图已成功挂载。
pause
