package collector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/domain"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const maximumRegistryValueBytes = 1 << 20

type RegistryTarget struct {
	Name         string
	Root         registry.Key
	Path         string
	View         uint32
	WatchSubtree bool
	Category     domain.EventCategory
	Severity     domain.EventSeverity
}

func NewRegistryCollector(target RegistryTarget) (*RegistryCollector, error) {
	if target.Root == 0 || strings.TrimSpace(target.Path) == "" {
		return nil, errors.New("registry target root and path are required")
	}
	name := "windows_registry:" + strings.TrimSpace(target.Name)
	snapshot := func() (map[string]RegistryValue, error) {
		return snapshotRegistryTarget(target)
	}
	wait := func(ctx context.Context) error {
		return waitForRegistryChange(ctx, target)
	}
	return newRegistryCollector(name, target.Category, target.Severity, snapshot, wait, time.Now)
}

func DefaultRegistryCollectors() ([]Collector, error) {
	return RegistryCollectorsForMonitoringPolicy(domain.DefaultMonitoringPolicy())
}

func RegistryCollectorsForMonitoringPolicy(policy domain.MonitoringPolicy) ([]Collector, error) {
	policy = policy.Resolved()
	targets := defaultRegistryTargets()
	collectors := make([]Collector, 0, len(targets))
	for _, target := range targets {
		if !registryTargetEnabled(target, policy) {
			continue
		}
		current, err := NewRegistryCollector(target)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, current)
	}
	return collectors, nil
}

func registryTargetEnabled(target RegistryTarget, policy domain.MonitoringPolicy) bool {
	name := strings.ToLower(target.Name)
	system := policy.SystemAndNetwork
	switch {
	case strings.Contains(name, "software"):
		return policy.ProcessAndSoftwareEnabled
	case strings.Contains(name, "startup"):
		return policy.SystemAndNetworkEnabled && system.MonitorStartupItems
	case strings.Contains(name, "firewall"):
		return policy.SystemAndNetworkEnabled && system.MonitorFirewall
	case strings.Contains(name, "remote_desktop"):
		return policy.SystemAndNetworkEnabled && system.MonitorRemoteDesktop
	case strings.Contains(name, "login_"):
		return policy.SystemAndNetworkEnabled && system.MonitorAccounts
	case strings.Contains(name, "network_"):
		return policy.SystemAndNetworkEnabled && system.MonitorNetwork
	default:
		return false
	}
}

func defaultRegistryTargets() []RegistryTarget {
	targets := []RegistryTarget{
		{Name: "machine_startup_64", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, View: registry.WOW64_64KEY, Category: domain.EventCategorySystem, Severity: domain.EventSeverityMedium},
		{Name: "machine_startup_32", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, View: registry.WOW64_32KEY, Category: domain.EventCategorySystem, Severity: domain.EventSeverityMedium},
		{Name: "machine_startup_once_64", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows\CurrentVersion\RunOnce`, View: registry.WOW64_64KEY, Category: domain.EventCategorySystem, Severity: domain.EventSeverityMedium},
		{Name: "machine_startup_once_32", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows\CurrentVersion\RunOnce`, View: registry.WOW64_32KEY, Category: domain.EventCategorySystem, Severity: domain.EventSeverityMedium},
		{Name: "machine_software_64", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, View: registry.WOW64_64KEY, WatchSubtree: true, Category: domain.EventCategorySoftware, Severity: domain.EventSeverityLow},
		{Name: "machine_software_32", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`, View: registry.WOW64_32KEY, WatchSubtree: true, Category: domain.EventCategorySoftware, Severity: domain.EventSeverityLow},
		{Name: "firewall_policy", Root: registry.LOCAL_MACHINE, Path: `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy`, View: registry.WOW64_64KEY, WatchSubtree: true, Category: domain.EventCategorySystem, Severity: domain.EventSeverityHigh},
		{Name: "remote_desktop", Root: registry.LOCAL_MACHINE, Path: `SYSTEM\CurrentControlSet\Control\Terminal Server`, View: registry.WOW64_64KEY, WatchSubtree: true, Category: domain.EventCategorySystem, Severity: domain.EventSeverityHigh},
		{Name: "login_winlogon", Root: registry.LOCAL_MACHINE, Path: `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`, View: registry.WOW64_64KEY, WatchSubtree: true, Category: domain.EventCategorySystem, Severity: domain.EventSeverityHigh},
		{Name: "login_lsa", Root: registry.LOCAL_MACHINE, Path: `SYSTEM\CurrentControlSet\Control\Lsa`, View: registry.WOW64_64KEY, WatchSubtree: true, Category: domain.EventCategorySystem, Severity: domain.EventSeverityHigh},
		{Name: "network_tcpip", Root: registry.LOCAL_MACHINE, Path: `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`, View: registry.WOW64_64KEY, WatchSubtree: true, Category: domain.EventCategorySystem, Severity: domain.EventSeverityMedium},
	}
	userSIDs, _ := registry.USERS.ReadSubKeyNames(-1)
	return appendUserStartupRegistryTargets(targets, userSIDs)
}

func appendUserStartupRegistryTargets(targets []RegistryTarget, userSIDs []string) []RegistryTarget {
	for _, sid := range cleanSortedValues(userSIDs) {
		if !strings.HasPrefix(sid, "S-1-") || strings.HasSuffix(strings.ToLower(sid), "_classes") {
			continue
		}
		for _, current := range []struct{ suffix, path string }{
			{"run", `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`},
			{"run_once", `SOFTWARE\Microsoft\Windows\CurrentVersion\RunOnce`},
		} {
			targets = append(targets, RegistryTarget{
				Name: "user_startup_" + sid + "_" + current.suffix, Root: registry.USERS,
				Path: sid + `\` + current.path, View: registry.WOW64_64KEY,
				Category: domain.EventCategorySystem, Severity: domain.EventSeverityMedium,
			})
		}
	}
	return targets
}

func snapshotRegistryTarget(target RegistryTarget) (map[string]RegistryValue, error) {
	key, err := registry.OpenKey(target.Root, target.Path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS|target.View)
	if errors.Is(err, registry.ErrNotExist) {
		return map[string]RegistryValue{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer key.Close()
	values := make(map[string]RegistryValue)
	if err := walkRegistryValues(key, target.Path, target.WatchSubtree, target.View, values); err != nil {
		return nil, err
	}
	return values, nil
}

func walkRegistryValues(
	key registry.Key,
	path string,
	recurse bool,
	view uint32,
	values map[string]RegistryValue,
) error {
	names, err := key.ReadValueNames(-1)
	if err != nil {
		return fmt.Errorf("enumerate registry values for %s: %w", path, err)
	}
	for _, name := range names {
		size, valueType, err := key.GetValue(name, nil)
		if err != nil {
			return fmt.Errorf("size registry value %s: %w", name, err)
		}
		if size > maximumRegistryValueBytes {
			continue
		}
		buffer := make([]byte, size)
		read, valueType, err := key.GetValue(name, buffer)
		if err != nil {
			return fmt.Errorf("read registry value %s: %w", name, err)
		}
		value := RegistryValue{KeyPath: path, ValueName: name, ValueType: valueType, Data: buffer[:read]}
		values[path+"\x00"+name] = value
	}
	if !recurse {
		return nil
	}
	subkeys, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return fmt.Errorf("enumerate registry subkeys for %s: %w", path, err)
	}
	for _, name := range subkeys {
		subkey, err := registry.OpenKey(key, name, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS|view)
		if err != nil {
			if errors.Is(err, registry.ErrNotExist) {
				continue
			}
			return err
		}
		subpath := filepath.Join(path, name)
		err = walkRegistryValues(subkey, subpath, true, view, values)
		_ = subkey.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func waitForRegistryChange(ctx context.Context, target RegistryTarget) error {
	key, err := registry.OpenKey(target.Root, target.Path, registry.NOTIFY|target.View)
	if errors.Is(err, registry.ErrNotExist) {
		<-ctx.Done()
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	defer key.Close()
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	filter := uint32(windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_SECURITY)
	if err := windows.RegNotifyChangeKeyValue(windows.Handle(key), target.WatchSubtree, filter, event, true); err != nil {
		return fmt.Errorf("register registry notification: %w", err)
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, err := windows.WaitForSingleObject(event, 100)
		if err != nil {
			return err
		}
		if result == windows.WAIT_OBJECT_0 {
			return nil
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			return fmt.Errorf("unexpected registry wait result %d", result)
		}
	}
}
