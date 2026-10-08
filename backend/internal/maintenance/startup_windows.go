package maintenance

import (
	"errors"
	"fmt"
	"strings"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	startupRegistryPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	startupValueName    = "DesktopGuardProAgent"
	uiStartupValueName  = "DesktopGuardProUI"
)

type startupValueStore interface {
	SetStringValue(name, value string) error
	DeleteValue(name string) error
	Close() error
}

type uiStartupValueStore interface {
	GetStringValue(name string) (string, uint32, error)
	DeleteValue(name string) error
}

// The elevated MSI runner must target the verified installation owner, rather
// than HKCU (which would be SYSTEM). Only this installation's UI entry is removed.
func UnregisterUIStartup(ownerSID, uiExecutable string) error {
	sid, err := winapi.StringToSid(ownerSID)
	if err != nil || sid.String() != ownerSID {
		return errors.New("invalid UI startup owner SID")
	}
	key, err := registry.OpenKey(registry.USERS, ownerSID+`\`+startupRegistryPath,
		registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open owner UI startup registry: %w", err)
	}
	defer key.Close()
	return unregisterUIStartup(key, uiExecutable)
}

func unregisterUIStartup(store uiStartupValueStore, uiExecutable string) error {
	value, _, err := store.GetStringValue(uiStartupValueName)
	if errors.Is(err, registry.ErrNotExist) || errors.Is(err, registry.ErrUnexpectedType) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read UI startup entry: %w", err)
	}
	quotedCommand := `"` + uiExecutable + `" --autostart`
	command := winapi.ComposeCommandLine([]string{uiExecutable, "--autostart"})
	if !strings.EqualFold(strings.TrimSpace(value), quotedCommand) &&
		!strings.EqualFold(strings.TrimSpace(value), command) {
		return nil
	}
	if err := store.DeleteValue(uiStartupValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("delete UI startup entry: %w", err)
	}
	return nil
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
