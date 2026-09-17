package maintenance

import (
	"fmt"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	startupRegistryPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	startupValueName    = "DesktopGuardProAgent"
)

type startupValueStore interface {
	SetStringValue(name, value string) error
	DeleteValue(name string) error
	Close() error
}

func RegisterAgentStartup(agentExecutable string) error {
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		startupRegistryPath,
		registry.SET_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return fmt.Errorf("open machine startup registry: %w", err)
	}
	defer key.Close()
	return registerAgentStartup(key, agentExecutable)
}

func UnregisterAgentStartup() error {
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		startupRegistryPath,
		registry.SET_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return fmt.Errorf("open machine startup registry: %w", err)
	}
	defer key.Close()
	if err := key.DeleteValue(startupValueName); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("delete agent startup entry: %w", err)
	}
	return nil
}

func registerAgentStartup(store startupValueStore, agentExecutable string) error {
	commandLine := winapi.ComposeCommandLine([]string{agentExecutable, "--autostart"})
	if err := store.SetStringValue(startupValueName, commandLine); err != nil {
		return fmt.Errorf("write agent startup entry: %w", err)
	}
	return nil
}
