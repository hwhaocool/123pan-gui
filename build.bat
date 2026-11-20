@echo off
setlocal
cd /d "%~dp0"

rem mygo needs Go 1.27.3+; an older local Go downloads the toolchain from go.mod.
set GOTOOLCHAIN=auto

if /i "%~1"=="res" goto res
if not exist "rsrc.syso" goto res
goto build

:res
echo [res] pack app.ico into rsrc.syso...
go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico app.ico -o rsrc.syso
if errorlevel 1 goto :err

:build
echo [1/2] gofmt...
gofmt -w .
if errorlevel 1 goto :err

echo [2/2] build...
go build -trimpath -ldflags "-s -w -H windowsgui" -o 123pan-client.exe .
if errorlevel 1 goto :err

echo.
echo Build OK: %CD%\123pan-client.exe
exit /b 0

:err
echo.
echo Build FAILED.
exit /b 1
