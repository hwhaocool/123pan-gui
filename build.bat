@echo off
setlocal
cd /d "%~dp0"

rem mygo 需要 Go 1.27.3+；如果本机 Go 较旧，工具链会按 go.mod 自动下载。
set GOTOOLCHAIN=auto

echo [1/2] gofmt...
gofmt -w .
if errorlevel 1 goto :err

echo [2/2] 编译...
go build -trimpath -ldflags "-s -w -H windowsgui" -o 123pan-client.exe .
if errorlevel 1 goto :err

echo.
echo 构建完成: %CD%\123pan-client.exe
exit /b 0

:err
echo.
echo 构建失败。
exit /b 1
