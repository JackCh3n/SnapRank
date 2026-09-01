//go:build desktop

// Wails 桌面壳（可选形态）：`wails build` 或 `go build -tags desktop` 时启用。
// 与 serve 形态共用 internal/core 的同一套用例与内嵌前端。
package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"snaprank/bind"
	"snaprank/web"
)

const desktopBuild = true

//go:embed all:web/dist
var assets embed.FS

func runDesktop() {
	b, err := bind.New()
	if err != nil {
		panic(err)
	}
	defer b.Close()

	err = wails.Run(&options.App{
		Title:     "帧选 SnapRank",
		Width:     1080,
		Height:    720,
		MinWidth:  860,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  b.Startup,
		OnShutdown: func(ctx context.Context) { b.Shutdown() },
		Bind:       []interface{}{b},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Light,
		},
	})
	_ = web.Dist
	if err != nil {
		panic(err)
	}
}
