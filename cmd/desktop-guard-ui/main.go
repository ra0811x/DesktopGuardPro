package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"desktopguardpro/frontend"
	"desktopguardpro/internal/appbridge"
	"desktopguardpro/internal/desktop"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

var singleInstanceEncryptionKey = [32]byte{
	0x9f, 0x4b, 0x21, 0x7c, 0xd8, 0x36, 0xa2, 0x55,
	0x68, 0xee, 0x13, 0x90, 0x47, 0xba, 0x6d, 0x0c,
	0x32, 0xf1, 0x85, 0x5a, 0xc7, 0x09, 0xde, 0x74,
	0x1b, 0x63, 0xa8, 0x2d, 0xf5, 0x40, 0x96, 0xcb,
}

const applicationIconFileName = "desktop-guard-pro.ico"

func main() {
	if _, err := desktop.DetectWebView2Runtime(); err != nil {
		log.Printf("detect WebView2 Runtime: %v", err)
		desktop.ReportStartupFailure(err)
		return
	}

	bridge := appbridge.New()
	iconData := loadApplicationIcon()
	var lifecycle *desktop.Lifecycle
	app := application.New(application.Options{
		Name:        "Desktop Guard Pro",
		Description: "Windows desktop security audit",
		Icon:        iconData,
		Services: []application.Service{
			application.NewService(bridge),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(frontend.Assets),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:      "com.desktopguardpro.desktop-ui",
			EncryptionKey: singleInstanceEncryptionKey,
			ExitCode:      0,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if lifecycle != nil {
					lifecycle.ShowWindow()
				}
			},
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Desktop Guard Pro",
		Width:            1180,
		Height:           760,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: application.NewRGB(244, 247, 251),
		URL:              "/",
	})

	lifecycle = desktop.NewLifecycle(
		func() {
			window.Restore()
			window.Show()
			window.Focus()
		},
		func() { window.Hide() },
		func() { app.Quit() },
	)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		lifecycle.HandleWindowClose(event.Cancel)
	})

	tray := app.SystemTray.New()
	tray.SetIcon(iconData)
	tray.SetTooltip("Desktop Guard Pro")
	tray.OnClick(lifecycle.ShowWindow)
	menu := app.NewMenu()
	statusItem := menu.Add("状态：正在连接").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("打开控制台").OnClick(func(*application.Context) {
		lifecycle.ShowWindow()
	})
	menu.Add("退出").OnClick(func(*application.Context) {
		lifecycle.Quit()
	})
	tray.SetMenu(menu)

	pollContext, stopPolling := context.WithCancel(context.Background())
	defer stopPolling()
	go pollTrayStatus(pollContext, bridge, func(label string) {
		statusItem.SetLabel(label)
	})

	if err := app.Run(); err != nil {
		log.Printf("run Desktop Guard Pro UI: %v", err)
		desktop.ReportStartupFailure(err)
	}
}

func loadApplicationIcon() []byte {
	executable, err := os.Executable()
	if err != nil {
		return icons.DefaultWindowsIcon
	}
	workingDirectory, _ := os.Getwd()
	iconPath := applicationIconPath(executable, workingDirectory, func(path string) bool {
		info, statErr := os.Stat(path)
		return statErr == nil && !info.IsDir()
	})
	if iconPath == "" {
		return icons.DefaultWindowsIcon
	}
	iconData, err := os.ReadFile(iconPath)
	if err != nil || len(iconData) == 0 {
		return icons.DefaultWindowsIcon
	}
	return iconData
}

func applicationIconPath(executable, workingDirectory string, fileExists func(string) bool) string {
	candidates := []string{filepath.Join(filepath.Dir(executable), applicationIconFileName)}
	if workingDirectory != "" {
		candidates = append(candidates, filepath.Join(workingDirectory, "assets", applicationIconFileName))
	}
	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func pollTrayStatus(
	ctx context.Context,
	bridge *appbridge.Bridge,
	update func(string),
) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		health, err := bridge.GetHealth()
		update(desktop.TrayStatusLabel(health, err))
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
