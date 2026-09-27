package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestSystemAssetCollectorEmitsSoftwareUpgradeAndServiceChange(t *testing.T) {
	baseline := func(version, startType string) domain.AssetBaseline {
		return domain.AssetBaseline{Assets: []domain.Asset{
			{Category: domain.AssetCategorySoftware, Identifier: "app", DisplayName: "App", Attributes: map[string]string{
				"version": version, "publisher": "Contoso", "inventoryKind": "uninstall",
				"installScope": "machine", "installLocation": `C:\Program Files\App`,
			}},
			{Category: domain.AssetCategorySoftware, Identifier: "service", DisplayName: "Service", Attributes: map[string]string{"inventoryKind": "service", "startType": startType}},
		}}
	}
	call := 0
	snapshot := func(context.Context) (domain.AssetBaseline, error) {
		call++
		if call == 1 {
			return baseline("1.0", "2"), nil
		}
		return baseline("2.0", "3"), nil
	}
	collector, err := newSystemAssetCollector(snapshot, time.Millisecond, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sink := &observationSink{onEmit: func(count int) {
		if count == 2 {
			cancel()
		}
	}}
	if err := collector.Run(ctx, sink); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	actions := map[string]bool{}
	for _, observation := range sink.snapshot() {
		actions[observation.Action] = true
		if observation.Confidence != domain.EventConfidenceSnapshotDiff {
			t.Fatalf("observation confidence = %q", observation.Confidence)
		}
		if observation.Action == "software_upgraded" {
			var payload struct {
				Name            string `json:"name"`
				Version         string `json:"version"`
				Publisher       string `json:"publisher"`
				InstallScope    string `json:"installScope"`
				InstallLocation string `json:"installLocation"`
			}
			if err := json.Unmarshal(observation.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Name != "App" || payload.Version != "2.0" || payload.Publisher != "Contoso" ||
				payload.InstallScope != "machine" || payload.InstallLocation != `C:\Program Files\App` {
				t.Fatalf("software event fields = %+v", payload)
			}
		}
	}
	if !actions["software_upgraded"] || !actions["service_changed"] {
		t.Fatalf("system asset actions = %#v", actions)
	}
}

func TestAssetDifferenceActionClassifiesDriverAndScheduledTaskChanges(t *testing.T) {
	for kind, want := range map[string]string{"driver": "driver_added", "scheduledTask": "scheduled_task_added", "startup": "startup_item_added"} {
		action := assetDifferenceAction(domain.AssetDifference{Kind: domain.AssetDifferenceAdded, After: &domain.Asset{
			Category: domain.AssetCategorySoftware, Identifier: kind, DisplayName: kind, Attributes: map[string]string{"inventoryKind": kind},
		}})
		if action != want {
			t.Errorf("assetDifferenceAction(%q) = %q, want %q", kind, action, want)
		}
	}
}

func TestAssetDifferenceActionClassifiesSecurityRelevantSystemChanges(t *testing.T) {
	for attribute, want := range map[string]string{
		"timezoneName":           "time_configuration_changed",
		"auditPolicyDigest":      "audit_policy_changed",
		"firewallPublic":         "firewall_configuration_changed",
		"remoteDesktop":          "remote_desktop_configuration_changed",
		"securityCenterProducts": "security_center_status_changed",
		"winHTTPProxy":           "proxy_configuration_changed",
	} {
		action := assetDifferenceAction(domain.AssetDifference{
			Kind:              domain.AssetDifferenceChanged,
			Before:            &domain.Asset{Category: domain.AssetCategorySystem, Identifier: "local", DisplayName: "PC"},
			After:             &domain.Asset{Category: domain.AssetCategorySystem, Identifier: "local", DisplayName: "PC"},
			ChangedAttributes: []string{attribute},
		})
		if action != want {
			t.Errorf("attribute %q action=%q want=%q", attribute, action, want)
		}
	}
}

func TestSystemAssetCollectorPolicySeparatesModuleCategories(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.ProcessAndSoftwareEnabled = false
	policy.ExternalDevicesEnabled = false
	policy.SystemAndNetwork.MonitorNetwork = false
	collector, err := newSystemAssetCollectorWithPolicy(func(context.Context) (domain.AssetBaseline, error) {
		return domain.AssetBaseline{}, nil
	}, time.Second, time.Now, policy)
	if err != nil {
		t.Fatal(err)
	}
	software := &domain.Asset{Category: domain.AssetCategorySoftware, Attributes: map[string]string{}}
	device := &domain.Asset{Category: domain.AssetCategoryDevice, Attributes: map[string]string{}}
	network := &domain.Asset{Category: domain.AssetCategoryNetwork, Attributes: map[string]string{}}
	account := &domain.Asset{Category: domain.AssetCategoryAccount, Attributes: map[string]string{}}
	if collector.allowsDifference(software, "software_installed") || collector.allowsDifference(device, "device_connected") ||
		collector.allowsDifference(network, "network_configuration_changed") ||
		!collector.allowsDifference(account, "account_configuration_changed") {
		t.Fatalf("module category filtering did not match policy: %+v", policy)
	}
}

func TestSystemAssetCollectorLeavesDeviceEventsToDedicatedCollector(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	collector, err := newSystemAssetCollectorWithPolicy(func(context.Context) (domain.AssetBaseline, error) {
		return domain.AssetBaseline{}, nil
	}, time.Second, time.Now, policy)
	if err != nil {
		t.Fatal(err)
	}
	device := &domain.Asset{Category: domain.AssetCategoryDevice, Attributes: map[string]string{}}
	if collector.allowsDifference(device, "device_connected") {
		t.Fatal("system asset collector accepted a device event already owned by the dedicated device collector")
	}
	network := &domain.Asset{Category: domain.AssetCategoryNetwork, Attributes: map[string]string{}}
	if collector.allowsDifference(network, "network_configuration_changed") {
		t.Fatal("system asset collector accepted a network event already owned by the dedicated network collector")
	}
}

func TestSystemAssetCollectorUsesIndependentSystemAndSoftwareCadence(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.SystemAndNetwork.SnapshotIntervalSeconds = 30
	policy.ProcessAndSoftware.SnapshotIntervalSeconds = 300
	collector, err := NewSystemAssetCollectorWithPolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	if collector.interval != 30*time.Second || collector.systemInterval != 30*time.Second ||
		collector.softwareInterval != 5*time.Minute {
		t.Fatalf("asset snapshot cadence = poll %s, system %s, software %s",
			collector.interval, collector.systemInterval, collector.softwareInterval)
	}
}

func TestSplitAssetBaselineKeepsSoftwareInventoryOutOfSystemCadence(t *testing.T) {
	baseline := domain.AssetBaseline{Assets: []domain.Asset{
		{Category: domain.AssetCategorySoftware, Identifier: "app", DisplayName: "App", Attributes: map[string]string{"inventoryKind": "uninstall"}},
		{Category: domain.AssetCategorySoftware, Identifier: "service", DisplayName: "Service", Attributes: map[string]string{"inventoryKind": "service"}},
		{Category: domain.AssetCategorySystem, Identifier: "system", DisplayName: "System"},
	}}
	software, system := splitAssetBaselineCadence(baseline)
	if len(software.Assets) != 1 || software.Assets[0].Identifier != "app" {
		t.Fatalf("software cadence assets = %#v", software.Assets)
	}
	if len(system.Assets) != 2 || system.Assets[0].Identifier != "service" || system.Assets[1].Identifier != "system" {
		t.Fatalf("system cadence assets = %#v", system.Assets)
	}
}
