package main

import (
	"embed"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// buildVersion 用于前端展示与 WebView2 用户数据目录隔离（防缓存串旧）。
const buildVersion = "v1.0.0"

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// 独立 WebView2 用户数据目录：不同版本用不同缓存，避免读到旧打包的前端
	wvData := filepath.Join(os.TempDir(), "digdom-wv", buildVersion)

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "域探 · DigDom 子域名爆破",
		Width:     1040,
		Height:    680,
		MinWidth:  1040,
		MinHeight: 680,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 18, G: 18, B: 22, A: 1},
		OnStartup:        app.startup,
		Windows: &windows.Options{
			WebviewUserDataPath: wvData,
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
