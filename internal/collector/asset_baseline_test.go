package collector

import (
	"reflect"
	"testing"

	"desktopguardpro/internal/domain"
)

func TestBuildAssetBaselineProducesSortedFiveCategoryAssets(t *testing.T) {
	baseline, err := buildAssetBaseline(
		[]InstalledSoftware{
			{Identifier: "uninstall-2", Name: "Zulu", Version: "2.0", Publisher: "Example", InventoryKind: "uninstall", Attributes: map[string]string{"installScope": "machine", "installLocation": `C:\\Program Files\\Zulu`}},
			{Identifier: "uninstall-1", Name: "Alpha", Version: "1.0"},
		},
		[]DeviceInfo{{InstanceID: "USB\\B", Name: "USB B", Class: "USB"}, {
			InstanceID: "USB\\A", Name: "USB A", Manufacturer: "Example", Model: "Evidence Disk",
			SerialNumber: "SERIAL1", VolumeLabels: "Evidence", DriveLetters: "E:",
		}},
		[]NetworkInterface{{
			Index: 4, Name: "Ethernet", HardwareAddress: "00-11-22", Flags: "up", Addresses: []string{"10.0.0.2"},
			GatewayAddresses: []string{"10.0.0.1"}, DNSServers: []string{"1.1.1.1", "8.8.8.8"}, WiFiSSID: "Audit Network",
			WiFiSSIDStatus: "reported", ConnectionName: "Ethernet",
		}},
		AccountInfo{
			Identifier: "S-1-5-21-test", Name: "Raymond", Username: "PC\\Raymond",
			LocalUsers: []string{"Administrator", "Raymond"}, LocalUsersStatus: "reported",
			AdministratorMembers: []string{"PC\\Raymond"}, AdministratorsStatus: "reported",
		},
		SystemInfo{
			Identifier: "local-system", Name: "DESKTOP-1", OS: "windows", Architecture: "amd64",
			ProductName: "Windows 11 Pro", DisplayVersion: "24H2", Build: "26100.1",
			Processor: "Example CPU", ProcessorIdentifier: "x64 Family 6", ProcessorMHz: "3600",
			ProxyEnabled: "true", ProxyServer: "proxy.example:8080", ProxyOverride: "localhost;<local>", AutoConfigURL: "https://proxy.example/config.pac", WinHTTPProxy: "proxy.example:8080", WinHTTPProxyStatus: "reported",
			CapturedUTC: "2026-09-14T04:00:00Z", CapturedLocal: "2026-09-14T12:00:00+08:00",
			TimezoneName: "China Standard Time", TimezoneOffset: "28800", TimeServiceType: "NTP", TimeServer: "time.example,0x9", TimeSyncStatus: "reported",
			FirewallDomain: "enabled", FirewallPrivate: "enabled", FirewallPublic: "disabled", RemoteDesktop: "disabled", RemoteDesktopNLA: "required", RemoteDesktopPort: "3389", AuditPolicyDigest: "aabb", AuditPolicyStatus: "reported", SecurityCenterProducts: `[{"name":"Windows Defender","productState":397568}]`, SecurityCenterStatus: "reported",
			HardwareStatus: "reported", Mainboards: "Example|Board|1", Firmware: "Example|1.0|2",
			MemoryModules: "Example|RAM|17179869184", Disks: "Example SSD|3|1000", GraphicsAdapters: "Example GPU|1.0",
			NetworkAdapters: "Example NIC|00-11-22", Monitors: "Example Display|DISPLAY1",
		},
	)
	if err != nil {
		t.Fatalf("buildAssetBaseline() error = %v", err)
	}

	if err := baseline.Validate(); err != nil {
		t.Fatalf("baseline.Validate() error = %v", err)
	}
	if len(baseline.Assets) != 7 {
		t.Fatalf("asset count = %d, want 7", len(baseline.Assets))
	}
	if got := baseline.Assets[0]; got.Category != domain.AssetCategoryAccount || got.Identifier != "S-1-5-21-test" {
		t.Fatalf("first asset = %#v, want account asset", got)
	}
	if got := baseline.Assets[0]; got.Attributes["localUsers"] != "Administrator,Raymond" ||
		got.Attributes["administratorMembers"] != "PC\\Raymond" || got.Attributes["administratorsStatus"] != "reported" {
		t.Fatalf("account inventory = %#v", got.Attributes)
	}
	if got := baseline.Assets[1]; got.Category != domain.AssetCategoryDevice || got.Identifier != "USB\\A" || got.Attributes["manufacturer"] != "Example" {
		t.Fatalf("second asset = %#v, want sorted USB A device", got)
	}
	if got := baseline.Assets[1]; got.Attributes["model"] != "Evidence Disk" || got.Attributes["serialNumber"] != "SERIAL1" ||
		got.Attributes["volumeLabels"] != "Evidence" || got.Attributes["driveLetters"] != "E:" {
		t.Fatalf("USB storage attributes = %#v", got.Attributes)
	}
	if got := baseline.Assets[3]; got.Category != domain.AssetCategoryNetwork || !reflect.DeepEqual(got.Attributes, map[string]string{
		"addresses": "10.0.0.2", "flags": "up", "hardwareAddress": "00-11-22",
		"gatewayAddresses": "10.0.0.1", "dnsServers": "1.1.1.1,8.8.8.8", "wifiSSID": "Audit Network",
		"wifiSSIDStatus": "reported", "connectionName": "Ethernet",
	}) {
		t.Fatalf("network asset = %#v, want normalized network data", got)
	}
	if got := baseline.Assets[6]; got.Category != domain.AssetCategorySystem || !reflect.DeepEqual(got.Attributes, map[string]string{
		"architecture": "amd64", "os": "windows", "productName": "Windows 11 Pro", "displayVersion": "24H2",
		"build": "26100.1", "processor": "Example CPU", "processorIdentifier": "x64 Family 6", "processorMHz": "3600",
		"proxyEnabled": "true", "proxyServer": "proxy.example:8080", "proxyOverride": "localhost;<local>", "autoConfigURL": "https://proxy.example/config.pac", "winHTTPProxy": "proxy.example:8080", "winHTTPProxyStatus": "reported",
		"capturedUtc": "2026-09-14T04:00:00Z", "capturedLocal": "2026-09-14T12:00:00+08:00",
		"timezoneName": "China Standard Time", "timezoneOffset": "28800", "timeServiceType": "NTP", "timeServer": "time.example,0x9", "timeSyncStatus": "reported",
		"firewallDomain": "enabled", "firewallPrivate": "enabled", "firewallPublic": "disabled", "remoteDesktop": "disabled", "remoteDesktopNLA": "required", "remoteDesktopPort": "3389", "auditPolicyDigest": "aabb", "auditPolicyStatus": "reported", "securityCenterProducts": `[{"name":"Windows Defender","productState":397568}]`, "securityCenterStatus": "reported",
		"hardwareStatus": "reported", "mainboards": "Example|Board|1", "firmware": "Example|1.0|2",
		"memoryModules": "Example|RAM|17179869184", "disks": "Example SSD|3|1000", "graphicsAdapters": "Example GPU|1.0",
		"networkAdapters": "Example NIC|00-11-22", "monitors": "Example Display|DISPLAY1",
	}) {
		t.Fatalf("last asset = %#v, want system asset with Windows and processor inventory", got)
	}
	if got := baseline.Assets[5]; got.Category != domain.AssetCategorySoftware || !reflect.DeepEqual(got.Attributes, map[string]string{
		"version": "2.0", "publisher": "Example", "inventoryKind": "uninstall", "installScope": "machine", "installLocation": `C:\\Program Files\\Zulu`,
	}) {
		t.Fatalf("software asset = %#v, want inventory kind with package metadata", got)
	}
}

func TestBuildAssetBaselineRejectsInvalidSourceAsset(t *testing.T) {
	_, err := buildAssetBaseline(
		[]InstalledSoftware{{Identifier: "software-1", Name: "Valid"}},
		[]DeviceInfo{{InstanceID: "USB\\1"}},
		nil,
		AccountInfo{Identifier: "user-1", Name: "Raymond"},
		SystemInfo{Identifier: "local-system", Name: "DESKTOP-1"},
	)
	if err == nil {
		t.Fatal("buildAssetBaseline() error = nil, want validation error for unnamed device")
	}
}
