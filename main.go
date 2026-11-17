// 123 云盘客户端（WebDAV 版）：mygo 原生 UI。
//
//	go run .
//	go build -ldflags "-H windowsgui" -o 123pan-client.exe .
package main

import (
	"log"

	"github.com/egoist/mygo"
	myui "github.com/egoist/mygo/ui"

	appui "pan123/internal/ui"
)

func main() {
	app := appui.NewApp()

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "123 云盘客户端（WebDAV）",
			Width:     1040,
			Height:    760,
			MinWidth:  760,
			MinHeight: 520,
			StateKey:  "main",
			Content:   myui.View(app.View),
		})
		app.Attach(win)
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	app.Shutdown()
}
