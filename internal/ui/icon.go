package ui

import _ "embed"

// appIconPNG 是窗口标题栏 / 任务栏用的图标（256px PNG）。
// 它和仓库根目录的 app.ico 由同一份绘图代码生成：
//
//	go run ./tools/genicon -o app.ico -pngout internal/ui/appicon.png
//
// exe 文件图标由构建时打包进 PE 资源的 app.ico 决定（rsrc.syso），
// 窗口图标则由这里在运行时通过 mygo 的 Window.SetIcon 设置。
//
//go:embed appicon.png
var appIconPNG []byte
