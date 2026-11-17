@echo off
setlocal
cd /d "%~dp0"

echo [1/3] gofmt...
gofmt -w .
if errorlevel 1 goto :err

echo [2/3] 生成资源文件（manifest/DPI 感知）...
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -o rsrc.syso
if errorlevel 1 (
    echo 提示：资源生成失败（缺少网络或 rsrc），将跳过，程序仍可编译但界面没有视觉样式。
)

echo [3/3] 编译...
go build -trimpath -ldflags "-s -w -H windowsgui" -o 123pan-client.exe .
if errorlevel 1 goto :err

echo.
echo 构建完成：%~dp0123pan-client.exe
exit /b 0

:err
echo.
echo 构建失败。
exit /b 1
