package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"desktopguardpro/internal/domain"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func SnapshotSystemAssetBaseline(ctx context.Context) (domain.AssetBaseline, error) {
	if err := ctx.Err(); err != nil {
		return domain.AssetBaseline{}, err
	}
	software, err := snapshotInstalledSoftware()
	if err != nil {
		return domain.AssetBaseline{}, err
	}
	supplementalSoftware, err := snapshotSupplementalSoftwareInventory()
	if err != nil {
		return domain.AssetBaseline{}, err
	}
	software = append(software, supplementalSoftware...)
	sort.Slice(software, func(left, right int) bool { return software[left].Identifier < software[right].Identifier })
	devices, err := snapshotWindowsDevices()
	if err != nil {
		return domain.AssetBaseline{}, err
	}
	interfaces, err := snapshotNetworkInterfaces()
	if err != nil {
		return domain.AssetBaseline{}, err
	}
	account, err := snapshotAccount()
	if err != nil {
		return domain.AssetBaseline{}, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return domain.AssetBaseline{}, fmt.Errorf("read system hostname: %w", err)
	}
	system, err := snapshotSystemInfo(hostname)
	if err != nil {
		return domain.AssetBaseline{}, err
	}
	return buildAssetBaseline(software, devices, interfaces, account, system)
}

func snapshotSystemInfo(hostname string) (SystemInfo, error) {
	versionKey, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return SystemInfo{}, fmt.Errorf("open Windows version registry: %w", err)
	}
	defer versionKey.Close()

	productName, _, err := versionKey.GetStringValue("ProductName")
	if err != nil {
		return SystemInfo{}, fmt.Errorf("read Windows product name: %w", err)
	}
	displayVersion, _, _ := versionKey.GetStringValue("DisplayVersion")
	build, _, buildErr := versionKey.GetStringValue("CurrentBuildNumber")
	if buildErr != nil {
		build, _, _ = versionKey.GetStringValue("CurrentBuild")
	}
	if revision, _, revisionErr := versionKey.GetIntegerValue("UBR"); revisionErr == nil && strings.TrimSpace(build) != "" {
		build = strings.TrimSpace(build) + "." + strconv.FormatUint(revision, 10)
	}

	processor, processorIdentifier, processorMHz := "", "", ""
	processorKey, processorErr := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if processorErr == nil {
		processor, _, _ = processorKey.GetStringValue("ProcessorNameString")
		processorIdentifier, _, _ = processorKey.GetStringValue("Identifier")
		if mhz, _, mhzErr := processorKey.GetIntegerValue("~MHz"); mhzErr == nil {
			processorMHz = strconv.FormatUint(mhz, 10)
		}
		_ = processorKey.Close()
	}
	proxy, err := snapshotMachineProxySettings()
	if err != nil {
		return SystemInfo{}, err
	}
	winHTTPProxy, winHTTPProxyStatus := snapshotWinHTTPProxy()
	timeService, err := snapshotWindowsTimeService()
	if err != nil {
		return SystemInfo{}, err
	}
	security, err := snapshotWindowsSecuritySettings()
	if err != nil {
		return SystemInfo{}, err
	}
	securityCenterProducts, securityCenterStatus := snapshotWindowsSecurityCenter()
	hardware := snapshotWindowsHardwareInventory()
	now := time.Now()
	localNow := now.In(time.Local)
	timezoneName, timezoneOffset := localNow.Zone()

	return SystemInfo{
		Identifier: "local-system", Name: hostname, OS: runtime.GOOS, Architecture: runtime.GOARCH,
		ProductName: productName, DisplayVersion: displayVersion, Build: build, Processor: processor,
		ProcessorIdentifier: processorIdentifier, ProcessorMHz: processorMHz,
		ProxyEnabled: proxy.enabled, ProxyServer: proxy.server, ProxyOverride: proxy.override,
		AutoConfigURL: proxy.autoConfigURL,
		WinHTTPProxy:  winHTTPProxy, WinHTTPProxyStatus: winHTTPProxyStatus,
		CapturedUTC: now.UTC().Format(time.RFC3339Nano), CapturedLocal: localNow.Format(time.RFC3339Nano),
		TimezoneName: timezoneName, TimezoneOffset: strconv.FormatInt(int64(timezoneOffset), 10),
		TimeServiceType: timeService.kind, TimeServer: timeService.server, TimeSyncStatus: timeService.syncStatus,
		FirewallDomain: security.firewallDomain, FirewallPrivate: security.firewallPrivate, FirewallPublic: security.firewallPublic,
		RemoteDesktop: security.remoteDesktop, RemoteDesktopNLA: security.remoteDesktopNLA, RemoteDesktopPort: security.remoteDesktopPort,
		AuditPolicyDigest: security.auditPolicyDigest, AuditPolicyStatus: security.auditPolicyStatus,
		SecurityCenterProducts: securityCenterProducts, SecurityCenterStatus: securityCenterStatus,
		HardwareStatus: hardware.status, Mainboards: hardware.mainboards, Firmware: hardware.firmware,
		MemoryModules: hardware.memoryModules, Disks: hardware.disks, GraphicsAdapters: hardware.graphicsAdapters,
		NetworkAdapters: hardware.networkAdapters, Monitors: hardware.monitors,
	}, nil
}

func snapshotWinHTTPProxy() (string, string) {
	output, err := exec.Command("netsh.exe", "winhttp", "show", "proxy").Output()
	if err != nil {
		return "", "unavailable: " + err.Error()
	}
	return strings.Join(strings.Fields(string(output)), " "), "reported"
}

func snapshotWindowsSecurityCenter() (string, string) {
	const script = `$ErrorActionPreference='Stop'; @(Get-CimInstance -Namespace root/SecurityCenter2 -ClassName AntiVirusProduct | Sort-Object displayName | ForEach-Object { [ordered]@{ name=$_.displayName; productState=$_.productState; path=$_.pathToSignedProductExe } }) | ConvertTo-Json -Compress`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return "", "unavailable: " + err.Error()
	}
	value := strings.TrimSpace(string(output))
	if value == "" || value == "null" {
		return "[]", "reported"
	}
	if !json.Valid([]byte(value)) {
		return "", "unavailable: invalid response"
	}
	return value, "reported"
}

type windowsHardwareInventory struct {
	status           string
	mainboards       string
	firmware         string
	memoryModules    string
	disks            string
	graphicsAdapters string
	networkAdapters  string
	monitors         string
}

func snapshotWindowsHardwareInventory() windowsHardwareInventory {
	script := `$result = [pscustomobject]@{ Mainboards = @(Get-CimInstance Win32_BaseBoard | ForEach-Object { "$($_.Manufacturer)|$($_.Product)|$($_.SerialNumber)" }); Firmware = @(Get-CimInstance Win32_BIOS | ForEach-Object { "$($_.Manufacturer)|$($_.SMBIOSBIOSVersion)|$($_.SerialNumber)" }); MemoryModules = @(Get-CimInstance Win32_PhysicalMemory | ForEach-Object { "$($_.Manufacturer)|$($_.PartNumber)|$($_.Capacity)" }); Disks = @(Get-CimInstance Win32_DiskDrive | ForEach-Object { "$($_.Model)|$($_.SerialNumber)|$($_.Size)" }); GraphicsAdapters = @(Get-CimInstance Win32_VideoController | ForEach-Object { "$($_.Name)|$($_.DriverVersion)" }); NetworkAdapters = @(Get-CimInstance Win32_NetworkAdapter | Where-Object PhysicalAdapter | ForEach-Object { "$($_.Name)|$($_.MACAddress)" }); Monitors = @(Get-CimInstance Win32_DesktopMonitor | ForEach-Object { "$($_.Name)|$($_.PNPDeviceID)" }) }; ConvertTo-Json -InputObject $result -Compress -Depth 3`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script).Output()
	if err != nil {
		return windowsHardwareInventory{status: "unavailable: " + err.Error()}
	}
	var result struct {
		Mainboards       []string
		Firmware         []string
		MemoryModules    []string
		Disks            []string
		GraphicsAdapters []string
		NetworkAdapters  []string
		Monitors         []string
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return windowsHardwareInventory{status: "unavailable: " + err.Error()}
	}
	join := func(values []string) string { return strings.Join(cleanSortedValues(values), ";") }
	return windowsHardwareInventory{
		status: "reported", mainboards: join(result.Mainboards), firmware: join(result.Firmware),
		memoryModules: join(result.MemoryModules), disks: join(result.Disks), graphicsAdapters: join(result.GraphicsAdapters),
		networkAdapters: join(result.NetworkAdapters), monitors: join(result.Monitors),
	}
}

type windowsSecuritySettings struct {
	firewallDomain    string
	firewallPrivate   string
	firewallPublic    string
	remoteDesktop     string
	remoteDesktopNLA  string
	remoteDesktopPort string
	auditPolicyDigest string
	auditPolicyStatus string
}

func snapshotWindowsSecuritySettings() (windowsSecuritySettings, error) {
	settings := windowsSecuritySettings{
		firewallDomain:  firewallProfileState(`SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\DomainProfile`),
		firewallPrivate: firewallProfileState(`SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\StandardProfile`),
		firewallPublic:  firewallProfileState(`SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\PublicProfile`),
	}
	terminalServer, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Terminal Server`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		if denied, _, valueErr := terminalServer.GetIntegerValue("fDenyTSConnections"); valueErr == nil {
			settings.remoteDesktop = map[bool]string{true: "disabled", false: "enabled"}[denied != 0]
		}
		if nla, _, valueErr := terminalServer.GetIntegerValue("UserAuthentication"); valueErr == nil {
			settings.remoteDesktopNLA = map[bool]string{true: "required", false: "not_required"}[nla != 0]
		}
		_ = terminalServer.Close()
	}
	rdp, rdpErr := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if rdpErr == nil {
		if port, _, valueErr := rdp.GetIntegerValue("PortNumber"); valueErr == nil {
			settings.remoteDesktopPort = strconv.FormatUint(port, 10)
		}
		_ = rdp.Close()
	}
	policy, policyErr := exec.Command("auditpol.exe", "/get", "/category:*").Output()
	if policyErr != nil {
		settings.auditPolicyStatus = "unavailable"
	} else {
		digest := sha256.Sum256(policy)
		settings.auditPolicyDigest = hex.EncodeToString(digest[:])
		settings.auditPolicyStatus = "reported"
	}
	return settings, nil
}

func firewallProfileState(path string) string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "unavailable"
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("EnableFirewall")
	if err != nil {
		return "unavailable"
	}
	if enabled == 0 {
		return "disabled"
	}
	return "enabled"
}

type windowsTimeService struct {
	kind       string
	server     string
	syncStatus string
}

func snapshotWindowsTimeService() (windowsTimeService, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\W32Time\Parameters`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return windowsTimeService{syncStatus: "unavailable"}, nil
	}
	if err != nil {
		return windowsTimeService{}, fmt.Errorf("open Windows Time service settings: %w", err)
	}
	defer key.Close()
	service := windowsTimeService{}
	service.kind, _, _ = key.GetStringValue("Type")
	service.server, _, _ = key.GetStringValue("NtpServer")
	if err := exec.Command("w32tm.exe", "/query", "/status").Run(); err == nil {
		service.syncStatus = "reported"
	} else {
		service.syncStatus = "unavailable"
	}
	return service, nil
}

type machineProxySettings struct {
	enabled       string
	server        string
	override      string
	autoConfigURL string
}

func snapshotMachineProxySettings() (machineProxySettings, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return machineProxySettings{}, nil
	}
	if err != nil {
		return machineProxySettings{}, fmt.Errorf("open machine proxy settings: %w", err)
	}
	defer key.Close()

	settings := machineProxySettings{}
	if enabled, _, enabledErr := key.GetIntegerValue("ProxyEnable"); enabledErr == nil {
		settings.enabled = strconv.FormatBool(enabled != 0)
	}
	settings.server, _, _ = key.GetStringValue("ProxyServer")
	settings.override, _, _ = key.GetStringValue("ProxyOverride")
	settings.autoConfigURL, _, _ = key.GetStringValue("AutoConfigURL")
	return settings, nil
}

func snapshotInstalledSoftware() ([]InstalledSoftware, error) {
	userSIDs, _ := registry.USERS.ReadSubKeyNames(-1)
	targets := installedSoftwareTargets(userSIDs)
	software := make([]InstalledSoftware, 0, 128)
	for _, target := range targets {
		key, err := registry.OpenKey(target.root, target.path, registry.READ|target.view)
		if err != nil {
			if target.scope == "user" && errors.Is(err, registry.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("open %s registry: %w", target.name, err)
		}
		names, err := key.ReadSubKeyNames(-1)
		if err != nil {
			_ = key.Close()
			return nil, fmt.Errorf("enumerate %s registry: %w", target.name, err)
		}
		for _, name := range names {
			entry, err := registry.OpenKey(key, name, registry.QUERY_VALUE|target.view)
			if err != nil {
				continue
			}
			displayName, _, displayErr := entry.GetStringValue("DisplayName")
			version, _, _ := entry.GetStringValue("DisplayVersion")
			publisher, _, _ := entry.GetStringValue("Publisher")
			installLocation, _, _ := entry.GetStringValue("InstallLocation")
			_ = entry.Close()
			if displayErr != nil || strings.TrimSpace(displayName) == "" {
				continue
			}
			software = append(software, InstalledSoftware{
				Identifier: target.name + `\` + name, Name: displayName, Version: version, Publisher: publisher, InventoryKind: "uninstall",
				Attributes: nonEmptyAttributes(map[string]string{"installScope": target.scope, "installUserSID": target.userSID, "installLocation": installLocation}),
			})
		}
		_ = key.Close()
	}
	sort.Slice(software, func(left, right int) bool { return software[left].Identifier < software[right].Identifier })
	return software, nil
}

type installedSoftwareTarget struct {
	name, path, scope, userSID string
	root                       registry.Key
	view                       uint32
}

func installedSoftwareTargets(userSIDs []string) []installedSoftwareTarget {
	const uninstallPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	targets := []installedSoftwareTarget{
		{name: "machine-uninstall-64", root: registry.LOCAL_MACHINE, path: uninstallPath, view: registry.WOW64_64KEY, scope: "machine"},
		{name: "machine-uninstall-32", root: registry.LOCAL_MACHINE, path: uninstallPath, view: registry.WOW64_32KEY, scope: "machine"},
	}
	for _, sid := range cleanSortedValues(userSIDs) {
		if !strings.HasPrefix(sid, "S-1-") || strings.HasSuffix(strings.ToLower(sid), "_classes") {
			continue
		}
		for _, view := range []struct {
			name string
			flag uint32
		}{{"64", registry.WOW64_64KEY}, {"32", registry.WOW64_32KEY}} {
			targets = append(targets, installedSoftwareTarget{
				name: "user-" + sid + "-uninstall-" + view.name, root: registry.USERS,
				path: sid + `\` + uninstallPath, view: view.flag, scope: "user", userSID: sid,
			})
		}
	}
	return targets
}

func snapshotSupplementalSoftwareInventory() ([]InstalledSoftware, error) {
	return collectSupplementalSoftwareInventory([]supplementalInventorySource{
		{name: "AppX", optional: true, snapshot: snapshotAppXPackages},
		{name: "services", snapshot: snapshotWindowsServices},
		{name: "scheduled tasks", snapshot: snapshotScheduledTasks},
		{name: "startup items", snapshot: snapshotStartupItems},
	})
}

type supplementalInventorySource struct {
	name     string
	optional bool
	snapshot func() ([]InstalledSoftware, error)
}

func collectSupplementalSoftwareInventory(sources []supplementalInventorySource) ([]InstalledSoftware, error) {
	assets := make([]InstalledSoftware, 0, 512)
	for _, source := range sources {
		if source.snapshot == nil {
			return nil, fmt.Errorf("snapshot %s inventory: source is unavailable", source.name)
		}
		items, err := source.snapshot()
		if err != nil {
			if source.optional && errors.Is(err, registry.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("snapshot %s inventory: %w", source.name, err)
		}
		assets = append(assets, items...)
	}
	return assets, nil
}

func snapshotAppXPackages() ([]InstalledSoftware, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages`, registry.READ|registry.WOW64_64KEY)
	if err != nil {
		return nil, fmt.Errorf("open AppX package registry: %w", err)
	}
	defer key.Close()
	names, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("enumerate AppX package registry: %w", err)
	}
	assets := make([]InstalledSoftware, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var displayName, publisher, installLocation string
		entry, openErr := registry.OpenKey(key, name, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if openErr == nil {
			displayName, _, _ = entry.GetStringValue("DisplayName")
			publisher, _, _ = entry.GetStringValue("Publisher")
			installLocation, _, _ = entry.GetStringValue("PackageRootFolder")
			_ = entry.Close()
		}
		assets = append(assets, appXPackageInventory(name, displayName, publisher, installLocation))
	}
	return assets, nil
}

func appXPackageInventory(fullName, displayName, publisher, installLocation string) InstalledSoftware {
	parts := strings.Split(strings.TrimSpace(fullName), "_")
	name, version, publisherID := strings.TrimSpace(fullName), "", ""
	if len(parts) >= 5 {
		name = strings.Join(parts[:len(parts)-4], "_")
		version = parts[len(parts)-4]
		publisherID = parts[len(parts)-1]
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || strings.HasPrefix(displayName, "@") {
		displayName = name
	}
	if strings.TrimSpace(publisher) == "" {
		publisher = publisherID
	}
	return InstalledSoftware{
		Identifier: `appx\` + strings.TrimSpace(fullName), Name: displayName, Version: version,
		Publisher: publisher, InventoryKind: "appx",
		Attributes: nonEmptyAttributes(map[string]string{"installScope": "machine", "installLocation": installLocation}),
	}
}

func snapshotWindowsServices() ([]InstalledSoftware, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.READ|registry.WOW64_64KEY)
	if err != nil {
		return nil, fmt.Errorf("open service registry: %w", err)
	}
	defer key.Close()
	names, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("enumerate service registry: %w", err)
	}
	assets := make([]InstalledSoftware, 0, len(names))
	for _, name := range names {
		entry, openErr := registry.OpenKey(key, name, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if openErr != nil {
			continue
		}
		displayName, _, _ := entry.GetStringValue("DisplayName")
		imagePath, _, _ := entry.GetStringValue("ImagePath")
		startType, _, startErr := entry.GetIntegerValue("Start")
		serviceType, _, serviceTypeErr := entry.GetIntegerValue("Type")
		_ = entry.Close()
		if strings.TrimSpace(displayName) == "" {
			displayName = name
		}
		kind := "service"
		if serviceTypeErr == nil && serviceType&0x3 != 0 {
			kind = "driver"
		}
		attributes := map[string]string{"imagePath": imagePath}
		if startErr == nil {
			attributes["startType"] = strconv.FormatUint(startType, 10)
		}
		if serviceTypeErr == nil {
			attributes["serviceType"] = strconv.FormatUint(serviceType, 10)
		}
		assets = append(assets, InstalledSoftware{
			Identifier: `service\` + name, Name: displayName, InventoryKind: kind, Attributes: attributes,
		})
	}
	return assets, nil
}

func snapshotScheduledTasks() ([]InstalledSoftware, error) {
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, fmt.Errorf("read Windows system directory: %w", err)
	}
	root := filepath.Join(systemDirectory, "Tasks")
	assets := make([]InstalledSoftware, 0, 256)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relativePath, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		taskName := strings.ReplaceAll(relativePath, string(filepath.Separator), `\`)
		assets = append(assets, InstalledSoftware{
			Identifier: `scheduled-task\` + taskName, Name: taskName, InventoryKind: "scheduledTask",
			Attributes: map[string]string{"path": taskName},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate scheduled tasks: %w", err)
	}
	return assets, nil
}

func snapshotStartupItems() ([]InstalledSoftware, error) {
	items := []InstalledSoftware{}
	for _, target := range defaultRegistryTargets() {
		if !strings.Contains(target.Name, "_startup_") {
			continue
		}
		values, err := snapshotRegistryTarget(target)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", target.Name, err)
		}
		items = append(items, startupInventoryFromValues(target, values)...)
	}
	return items, nil
}

func startupInventoryFromValues(target RegistryTarget, values map[string]RegistryValue) []InstalledSoftware {
	identities := make([]string, 0, len(values))
	for identity := range values {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	items := make([]InstalledSoftware, 0, len(identities))
	for _, identity := range identities {
		value := values[identity]
		scope := "machine"
		userSID := ""
		if strings.HasPrefix(target.Name, "user_startup_") {
			scope = "user"
			userSID = strings.TrimPrefix(strings.Split(target.Name, "_run")[0], "user_startup_")
		}
		items = append(items, InstalledSoftware{
			Identifier: "startup\\" + target.Name + "\\" + value.ValueName,
			Name:       value.ValueName, InventoryKind: "startup",
			Attributes: nonEmptyAttributes(map[string]string{
				"command": decodeRegistryText(value.Data), "registryPath": value.KeyPath,
				"scope": scope, "userSID": userSID,
			}),
		})
	}
	return items
}

func decodeRegistryText(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	words := make([]uint16, len(data)/2)
	for index := range words {
		words[index] = uint16(data[index*2]) | uint16(data[index*2+1])<<8
	}
	return windows.UTF16ToString(words)
}

func snapshotNetworkInterfaces() ([]NetworkInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}
	configurations, err := snapshotNetworkAdapterConfigurations()
	if err != nil {
		return nil, err
	}
	assets := make([]NetworkInterface, 0, len(interfaces))
	for _, current := range interfaces {
		if current.Index <= 0 || strings.TrimSpace(current.Name) == "" {
			continue
		}
		addresses, err := current.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read addresses for %s: %w", current.Name, err)
		}
		values := make([]string, 0, len(addresses))
		for _, address := range addresses {
			values = append(values, address.String())
		}
		sort.Strings(values)
		configuration := configurations[current.Index]
		assets = append(assets, NetworkInterface{
			Index: current.Index, Name: current.Name, HardwareAddress: current.HardwareAddr.String(), Flags: current.Flags.String(), Addresses: values,
			GatewayAddresses: configuration.gatewayAddresses, DNSServers: configuration.dnsServers, WiFiSSID: configuration.wifiSSID,
			WiFiSSIDStatus: configuration.wifiSSIDStatus, ConnectionName: current.Name,
		})
	}
	sort.Slice(assets, func(left, right int) bool { return assets[left].Index < assets[right].Index })
	return assets, nil
}

type networkAdapterConfiguration struct {
	gatewayAddresses []string
	dnsServers       []string
	wifiSSID         string
	wifiSSIDStatus   string
}

func snapshotNetworkAdapterConfigurations() (map[int]networkAdapterConfiguration, error) {
	wirelessNetworks, wirelessStatus := snapshotWirelessNetworkNames()
	size := uint32(15 * 1024)
	for attempt := 0; attempt < 2; attempt++ {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(
			windows.AF_UNSPEC,
			windows.GAA_FLAG_INCLUDE_GATEWAYS,
			0,
			first,
			&size,
		)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if errors.Is(err, windows.ERROR_NO_DATA) {
			return map[int]networkAdapterConfiguration{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("enumerate network adapter configuration: %w", err)
		}

		configurations := make(map[int]networkAdapterConfiguration)
		for adapter := first; adapter != nil; adapter = adapter.Next {
			ssid := wirelessNetworks[adapter.NetworkGuid.String()]
			ssidStatus := "not_applicable"
			if adapter.IfType == 71 {
				ssidStatus = wirelessStatus
				if ssid != "" {
					ssidStatus = "reported"
				} else if ssidStatus == "reported" {
					ssidStatus = "not_connected"
				}
			}
			configuration := networkAdapterConfiguration{
				gatewayAddresses: socketAddressStrings(adapter.FirstGatewayAddress),
				dnsServers:       dnsServerStrings(adapter.FirstDnsServerAddress),
				wifiSSID:         ssid,
				wifiSSIDStatus:   ssidStatus,
			}
			configurations[int(adapter.IfIndex)] = configuration
		}
		return configurations, nil
	}
	return nil, fmt.Errorf("enumerate network adapter configuration: buffer size changed repeatedly")
}

var (
	wlanAPILibrary              = windows.NewLazySystemDLL("wlanapi.dll")
	wlanOpenHandle              = wlanAPILibrary.NewProc("WlanOpenHandle")
	wlanCloseHandle             = wlanAPILibrary.NewProc("WlanCloseHandle")
	wlanEnumInterfaces          = wlanAPILibrary.NewProc("WlanEnumInterfaces")
	wlanQueryInterface          = wlanAPILibrary.NewProc("WlanQueryInterface")
	wlanFreeMemory              = wlanAPILibrary.NewProc("WlanFreeMemory")
	wlanInterfaceStateConnected = uint32(1)
	wlanInterfaceOpcodeCurrent  = uint32(7)
)

type wlanInterfaceInfo struct {
	guid        windows.GUID
	state       uint32
	description [256]uint16
}

type wlanInterfaceInfoList struct {
	count      uint32
	index      uint32
	interfaces [1]wlanInterfaceInfo
}

type dot11SSID struct {
	length uint32
	value  [32]byte
}

type wlanAssociationAttributes struct {
	ssid          dot11SSID
	bssType       uint32
	bssid         [6]byte
	phyType       uint32
	phyIndex      uint32
	signalQuality uint32
	rxRate        uint32
	txRate        uint32
}

type wlanSecurityAttributes struct {
	securityEnabled uint32
	oneXEnabled     uint32
	authAlgorithm   uint32
	cipherAlgorithm uint32
}

type wlanConnectionAttributes struct {
	state          uint32
	connectionMode uint32
	profileName    [256]uint16
	association    wlanAssociationAttributes
	security       wlanSecurityAttributes
}

func snapshotWirelessNetworkNames() (map[string]string, string) {
	negotiatedVersion := uint32(0)
	var handle windows.Handle
	status, _, _ := wlanOpenHandle.Call(
		2,
		0,
		uintptr(unsafe.Pointer(&negotiatedVersion)),
		uintptr(unsafe.Pointer(&handle)),
	)
	if status != 0 {
		return map[string]string{}, fmt.Sprintf("wlan_open_failed_%d", status)
	}
	defer wlanCloseHandle.Call(uintptr(handle), 0)

	var list *wlanInterfaceInfoList
	status, _, _ = wlanEnumInterfaces.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&list)))
	if status != 0 || list == nil {
		return map[string]string{}, fmt.Sprintf("wlan_enumeration_failed_%d", status)
	}
	defer wlanFreeMemory.Call(uintptr(unsafe.Pointer(list)))
	if list.count == 0 {
		return map[string]string{}, "reported"
	}

	connections := make(map[string]string, list.count)
	first := unsafe.Pointer(&list.interfaces[0])
	for index := uint32(0); index < list.count; index++ {
		info := (*wlanInterfaceInfo)(unsafe.Add(first, uintptr(index)*unsafe.Sizeof(wlanInterfaceInfo{})))
		if info.state != wlanInterfaceStateConnected {
			continue
		}
		if ssid := queryWirelessNetworkName(handle, &info.guid); ssid != "" {
			connections[info.guid.String()] = ssid
		}
	}
	return connections, "reported"
}

func queryWirelessNetworkName(handle windows.Handle, guid *windows.GUID) string {
	var dataSize uint32
	var data unsafe.Pointer
	var valueType uint32
	status, _, _ := wlanQueryInterface.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(guid)),
		uintptr(wlanInterfaceOpcodeCurrent),
		0,
		uintptr(unsafe.Pointer(&dataSize)),
		uintptr(unsafe.Pointer(&data)),
		uintptr(unsafe.Pointer(&valueType)),
	)
	if status != 0 || data == nil || dataSize < uint32(unsafe.Sizeof(wlanConnectionAttributes{})) {
		return ""
	}
	defer wlanFreeMemory.Call(uintptr(data))

	attributes := (*wlanConnectionAttributes)(data)
	if attributes.state != wlanInterfaceStateConnected || attributes.association.ssid.length > uint32(len(attributes.association.ssid.value)) {
		return ""
	}
	return strings.TrimSpace(string(attributes.association.ssid.value[:attributes.association.ssid.length]))
}

func socketAddressStrings(first *windows.IpAdapterGatewayAddress) []string {
	values := make(map[string]struct{})
	for current := first; current != nil; current = current.Next {
		if value := current.Address.IP().String(); value != "<nil>" {
			values[value] = struct{}{}
		}
	}
	return sortedStrings(values)
}

func dnsServerStrings(first *windows.IpAdapterDnsServerAdapter) []string {
	values := make(map[string]struct{})
	for current := first; current != nil; current = current.Next {
		if value := current.Address.IP().String(); value != "<nil>" {
			values[value] = struct{}{}
		}
	}
	return sortedStrings(values)
}

func sortedStrings(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}

func snapshotAccount() (AccountInfo, error) {
	current, err := user.Current()
	if err != nil {
		return AccountInfo{}, fmt.Errorf("read current account: %w", err)
	}
	identifier := strings.TrimSpace(current.Uid)
	if identifier == "" {
		identifier = strings.TrimSpace(current.Username)
	}
	name := strings.TrimSpace(current.Name)
	if name == "" {
		name = strings.TrimSpace(current.Username)
	}
	localUsers, localUsersStatus := snapshotPowerShellNames(`Get-LocalUser | Sort-Object Name | ForEach-Object { $_.Name }`)
	administrators, administratorsStatus := snapshotPowerShellNames(`Get-LocalGroup -SID 'S-1-5-32-544' | Get-LocalGroupMember | Sort-Object Name | ForEach-Object { $_.Name }`)
	return AccountInfo{
		Identifier: identifier, Name: name, Username: current.Username,
		LocalUsers: localUsers, LocalUsersStatus: localUsersStatus,
		AdministratorMembers: administrators, AdministratorsStatus: administratorsStatus,
	}, nil
}

func snapshotPowerShellNames(script string) ([]string, string) {
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; "+script).Output()
	if err != nil {
		return nil, "unavailable: " + err.Error()
	}
	values := map[string]struct{}{}
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if value := strings.TrimSpace(line); value != "" {
			values[value] = struct{}{}
		}
	}
	return sortedStrings(values), "reported"
}
