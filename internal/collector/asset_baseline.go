package collector

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"desktopguardpro/internal/domain"
)

type InstalledSoftware struct {
	Identifier    string
	Name          string
	Version       string
	Publisher     string
	InventoryKind string
	Attributes    map[string]string
}

type NetworkInterface struct {
	Index            int
	Name             string
	HardwareAddress  string
	Flags            string
	Addresses        []string
	GatewayAddresses []string
	DNSServers       []string
	WiFiSSID         string
	WiFiSSIDStatus   string
	ConnectionName   string
}

type AccountInfo struct {
	Identifier           string
	Name                 string
	Username             string
	LocalUsers           []string
	LocalUsersStatus     string
	AdministratorMembers []string
	AdministratorsStatus string
}

type SystemInfo struct {
	Identifier             string
	Name                   string
	OS                     string
	Architecture           string
	ProductName            string
	DisplayVersion         string
	Build                  string
	Processor              string
	ProcessorIdentifier    string
	ProcessorMHz           string
	ProxyEnabled           string
	ProxyServer            string
	ProxyOverride          string
	AutoConfigURL          string
	WinHTTPProxy           string
	WinHTTPProxyStatus     string
	CapturedUTC            string
	CapturedLocal          string
	TimezoneName           string
	TimezoneOffset         string
	TimeServiceType        string
	TimeServer             string
	TimeSyncStatus         string
	FirewallDomain         string
	FirewallPrivate        string
	FirewallPublic         string
	RemoteDesktop          string
	RemoteDesktopNLA       string
	RemoteDesktopPort      string
	AuditPolicyDigest      string
	AuditPolicyStatus      string
	SecurityCenterProducts string
	SecurityCenterStatus   string
	HardwareStatus         string
	Mainboards             string
	Firmware               string
	MemoryModules          string
	Disks                  string
	GraphicsAdapters       string
	NetworkAdapters        string
	Monitors               string
}

func buildAssetBaseline(
	software []InstalledSoftware,
	devices []DeviceInfo,
	interfaces []NetworkInterface,
	account AccountInfo,
	system SystemInfo,
) (domain.AssetBaseline, error) {
	assets := make([]domain.Asset, 0, len(software)+len(devices)+len(interfaces)+2)
	for _, item := range software {
		attributes := make(map[string]string, len(item.Attributes)+3)
		for name, value := range item.Attributes {
			attributes[name] = value
		}
		attributes["version"] = item.Version
		attributes["publisher"] = item.Publisher
		attributes["inventoryKind"] = item.InventoryKind
		assets = append(assets, domain.Asset{
			Category: domain.AssetCategorySoftware, Identifier: strings.TrimSpace(item.Identifier), DisplayName: strings.TrimSpace(item.Name),
			Attributes: nonEmptyAttributes(attributes),
		})
	}
	for _, device := range devices {
		assets = append(assets, domain.Asset{
			Category: domain.AssetCategoryDevice, Identifier: strings.TrimSpace(device.InstanceID), DisplayName: strings.TrimSpace(device.Name),
			Attributes: nonEmptyAttributes(map[string]string{
				"class": device.Class, "manufacturer": device.Manufacturer, "model": device.Model,
				"serialNumber": device.SerialNumber, "volumeLabels": device.VolumeLabels, "driveLetters": device.DriveLetters,
			}),
		})
	}
	for _, item := range interfaces {
		assets = append(assets, domain.Asset{
			Category: domain.AssetCategoryNetwork, Identifier: strconv.Itoa(item.Index), DisplayName: strings.TrimSpace(item.Name),
			Attributes: nonEmptyAttributes(map[string]string{
				"hardwareAddress": item.HardwareAddress, "flags": item.Flags, "addresses": strings.Join(item.Addresses, ","),
				"gatewayAddresses": strings.Join(item.GatewayAddresses, ","), "dnsServers": strings.Join(item.DNSServers, ","),
				"wifiSSID":       item.WiFiSSID,
				"wifiSSIDStatus": item.WiFiSSIDStatus, "connectionName": item.ConnectionName,
			}),
		})
	}
	assets = append(assets,
		domain.Asset{Category: domain.AssetCategoryAccount, Identifier: strings.TrimSpace(account.Identifier), DisplayName: strings.TrimSpace(account.Name), Attributes: nonEmptyAttributes(map[string]string{
			"username": account.Username, "localUsers": strings.Join(account.LocalUsers, ","), "localUsersStatus": account.LocalUsersStatus,
			"administratorMembers": strings.Join(account.AdministratorMembers, ","), "administratorsStatus": account.AdministratorsStatus,
		})},
		domain.Asset{Category: domain.AssetCategorySystem, Identifier: strings.TrimSpace(system.Identifier), DisplayName: strings.TrimSpace(system.Name), Attributes: nonEmptyAttributes(map[string]string{
			"os": system.OS, "architecture": system.Architecture, "productName": system.ProductName,
			"displayVersion": system.DisplayVersion, "build": system.Build, "processor": system.Processor,
			"processorIdentifier": system.ProcessorIdentifier, "processorMHz": system.ProcessorMHz,
			"proxyEnabled": system.ProxyEnabled, "proxyServer": system.ProxyServer,
			"proxyOverride": system.ProxyOverride, "autoConfigURL": system.AutoConfigURL,
			"winHTTPProxy": system.WinHTTPProxy, "winHTTPProxyStatus": system.WinHTTPProxyStatus,
			"capturedUtc": system.CapturedUTC, "capturedLocal": system.CapturedLocal,
			"timezoneName": system.TimezoneName, "timezoneOffset": system.TimezoneOffset,
			"timeServiceType": system.TimeServiceType, "timeServer": system.TimeServer,
			"timeSyncStatus": system.TimeSyncStatus,
			"firewallDomain": system.FirewallDomain, "firewallPrivate": system.FirewallPrivate,
			"firewallPublic": system.FirewallPublic, "remoteDesktop": system.RemoteDesktop,
			"remoteDesktopNLA": system.RemoteDesktopNLA, "remoteDesktopPort": system.RemoteDesktopPort,
			"auditPolicyDigest": system.AuditPolicyDigest, "auditPolicyStatus": system.AuditPolicyStatus,
			"securityCenterProducts": system.SecurityCenterProducts, "securityCenterStatus": system.SecurityCenterStatus,
			"hardwareStatus": system.HardwareStatus, "mainboards": system.Mainboards, "firmware": system.Firmware,
			"memoryModules": system.MemoryModules, "disks": system.Disks, "graphicsAdapters": system.GraphicsAdapters,
			"networkAdapters": system.NetworkAdapters, "monitors": system.Monitors,
		})},
	)

	sort.Slice(assets, func(left, right int) bool {
		leftKey := string(assets[left].Category) + "\x00" + assets[left].Identifier
		rightKey := string(assets[right].Category) + "\x00" + assets[right].Identifier
		return leftKey < rightKey
	})
	baseline := domain.AssetBaseline{Assets: assets}
	if err := baseline.Validate(); err != nil {
		return domain.AssetBaseline{}, fmt.Errorf("validate collected asset baseline: %w", err)
	}
	return baseline, nil
}

func nonEmptyAttributes(attributes map[string]string) map[string]string {
	for name, value := range attributes {
		value = strings.TrimSpace(value)
		if value == "" {
			delete(attributes, name)
			continue
		}
		attributes[name] = value
	}
	if len(attributes) == 0 {
		return nil
	}
	return attributes
}
