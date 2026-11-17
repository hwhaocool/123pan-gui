// 123 云盘个人客户端：用树形控件展示云盘目录，并支持按需同步（带本地缓存）。
//
// 构建（Windows）：
//
//	go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -o rsrc.syso
//	go build -ldflags "-H windowsgui" -o 123pan-client.exe .
package main

import (
	"log"
	"os"

	"github.com/lxn/walk"

	"pan123/internal/ui"
)

func main() {
	// 日志里已经带了时间，这里不再让标准库重复加时间前缀。
	log.SetFlags(0)

	if err := ui.Run(); err != nil {
		log.Printf("启动失败: %v", err)
		walk.MsgBox(nil, "启动失败", err.Error(), walk.MsgBoxIconError)
		os.Exit(1)
	}
}
