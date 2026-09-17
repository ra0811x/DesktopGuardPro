package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const diagnosticDirectoryName = "DesktopGuardPro"

var ErrWebView2RuntimeUnavailable = errors.New("Microsoft Edge WebView2 Runtime is unavailable")

const webView2ClientKey = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

func DetectWebView2Runtime() (string, error) {
	return selectWebView2Version(
		registryVersionReader(registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\`+webView2ClientKey),
		registryVersionReader(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\`+webView2ClientKey),
		registryVersionReader(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\`+webView2ClientKey),
	)
}

func registryVersionReader(root registry.Key, path string) func() (string, error) {
	return func() (string, error) {
		key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
		if err != nil {
			return "", err
		}
		defer key.Close()
		version, _, err := key.GetStringValue("pv")
		return version, err
	}
}

func selectWebView2Version(readers ...func() (string, error)) (string, error) {
	for _, read := range readers {
		version, err := read()
		version = strings.TrimSpace(version)
		if err == nil && version != "" && version != "0.0.0.0" {
			return version, nil
		}
	}
	return "", ErrWebView2RuntimeUnavailable
}

func ReportStartupFailure(startupError error) {
	path, pathError := startupLogPath()
	if pathError == nil {
		pathError = WriteStartupFailure(path, startupError, time.Now().UTC())
	}
	if pathError != nil {
		path = "诊断日志写入失败：" + pathError.Error()
	}

	message, messageError := windows.UTF16PtrFromString(StartupFailureMessage(path))
	title, titleError := windows.UTF16PtrFromString("Desktop Guard Pro 启动失败")
	if messageError != nil || titleError != nil {
		return
	}
	_, _ = windows.MessageBox(0, message, title, windows.MB_OK|windows.MB_ICONERROR)
}

func WriteStartupFailure(path string, startupError error, at time.Time) error {
	if startupError == nil {
		return errors.New("startup error is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create diagnostic directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open startup diagnostic log: %w", err)
	}
	defer file.Close()

	message := strings.NewReplacer("\r\n", " | ", "\n", " | ", "\r", " | ").Replace(startupError.Error())
	if _, err := fmt.Fprintf(file, "%s startup_failure %s\n", at.UTC().Format(time.RFC3339Nano), message); err != nil {
		return fmt.Errorf("write startup diagnostic log: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush startup diagnostic log: %w", err)
	}
	return nil
}

func StartupFailureMessage(logPath string) string {
	return "Desktop Guard Pro 无法启动界面。\n\n" +
		"请安装或更新 Microsoft Edge WebView2 Runtime，然后重新启动软件。\n\n" +
		"诊断日志：\n" + logPath
}

func startupLogPath() (string, error) {
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("resolve LocalAppData directory: %w", err)
	}
	return filepath.Join(localAppData, diagnosticDirectoryName, "logs", "desktop-ui.log"), nil
}
