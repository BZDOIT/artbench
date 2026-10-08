@echo off
rem ============================================================
rem  workbench-desktop one-click build
rem  1) probe portable Go  2) go test  3) vite build  4) go build
rem ============================================================
setlocal enabledelayedexpansion
set PROJ=%~dp0
set GO_HOME=E:\workbuddy\_tools\go
set PATH=%GO_HOME%\bin;%PATH%
set GOPROXY=https://goproxy.cn,direct

rem ---- probe go ----
where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] go not found. Expected portable Go at %GO_HOME%
  echo Install: download go1.22.10.windows-amd64.zip from golang.google.cn and unzip to E:\workbuddy\_tools\go
  pause
  exit /b 1
)
for /f "delims=" %%v in ('go version') do echo [1/4] %%v

rem ---- go test ----
echo [2/4] go test ...
cd /d %PROJ%
go test ./...
if errorlevel 1 (
  echo [ERROR] go test failed
  pause
  exit /b 1
)

rem ---- vite build ----
echo [3/4] vite build ...
cd /d %PROJ%frontend
call npx vite build
if errorlevel 1 (
  echo [ERROR] vite build failed
  pause
  exit /b 1
)

rem ---- go build production ----
echo [4/4] go build production exe ...
cd /d %PROJ%
rem -H=windowsgui: 隐藏控制台黑窗（双击只出应用窗口）
rem 图标说明：rsrc_windows_amd64.syso 已含 图标(组ID=3, wails 硬编码读ID3)+manifest(ID=1)，go build 自动链接。
rem 改图标：① 改 build/_make_icon.py 并跑它 → ② 复制新 png 到 .winres\icon.png → ③ 执行：
rem   E:\workbuddy\_tools\gopath\bin\go-winres.exe make --in .winres\winres.json --arch amd64
rem 注意：不要用 rsrc.exe（图标组 ID 会是 1/2，wails 认 3，窗口和任务栏会没图标）；
rem      也不要直接嵌 build\windows\wails.exe.manifest（内含 {{.Name}} 模板占位符，会导致"并行配置不正确"无法启动）。
go build -tags desktop,production -ldflags "-w -s -H=windowsgui" -o build\bin\workbench-desktop.exe .
if errorlevel 1 (
  echo [ERROR] go build failed
  pause
  exit /b 1
)

echo.
echo [OK] build done: build\bin\workbench-desktop.exe
pause
