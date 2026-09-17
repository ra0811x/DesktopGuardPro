package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const defaultSystemAssetInterval = 30 * time.Second

type systemAssetSnapshot func(context.Context) (domain.AssetBaseline, error)

type SystemAssetCollector struct {
	snapshot         systemAssetSnapshot
	interval         time.Duration
	systemInterval   time.Duration
	softwareInterval time.Duration
	now              func() time.Time
	policy           domain.MonitoringPolicy
}

func NewSystemAssetCollector() (*SystemAssetCollector, error) {
	return newSystemAssetCollector(SnapshotSystemAssetBaseline, defaultSystemAssetInterval, time.Now)
}

func NewSystemAssetCollectorWithPolicy(policy domain.MonitoringPolicy) (*SystemAssetCollector, error) {
	policy = policy.Resolved()
	var systemInterval time.Duration
	if policy.SystemAndNetworkEnabled {
		systemInterval = time.Duration(policy.SystemAndNetwork.SnapshotIntervalSeconds) * time.Second
	}
	var softwareInterval time.Duration
	if policy.ProcessAndSoftwareEnabled {
		softwareInterval = time.Duration(policy.ProcessAndSoftware.SnapshotIntervalSeconds) * time.Second
	}
	return newSystemAssetCollectorWithCadence(SnapshotSystemAssetBaseline, systemInterval, softwareInterval, time.Now, policy)
}

func newSystemAssetCollector(snapshot systemAssetSnapshot, interval time.Duration, now func() time.Time) (*SystemAssetCollector, error) {
	return newSystemAssetCollectorWithPolicy(snapshot, interval, now, domain.DefaultMonitoringPolicy())
}

func newSystemAssetCollectorWithPolicy(snapshot systemAssetSnapshot, interval time.Duration, now func() time.Time, policy domain.MonitoringPolicy) (*SystemAssetCollector, error) {
	systemInterval := time.Duration(0)
	softwareInterval := time.Duration(0)
	if policy.SystemAndNetworkEnabled {
		systemInterval = interval
	}
	if policy.ProcessAndSoftwareEnabled {
		softwareInterval = interval
	}
	return newSystemAssetCollectorWithCadence(snapshot, systemInterval, softwareInterval, now, policy)
}

func newSystemAssetCollectorWithCadence(snapshot systemAssetSnapshot, systemInterval, softwareInterval time.Duration, now func() time.Time, policy domain.MonitoringPolicy) (*SystemAssetCollector, error) {
	interval := minimumActiveInterval(systemInterval, softwareInterval)
	if snapshot == nil || now == nil || interval <= 0 {
		return nil, errors.New("system asset collector dependency is invalid")
	}
	return &SystemAssetCollector{
		snapshot: snapshot, interval: interval, systemInterval: systemInterval,
		softwareInterval: softwareInterval, now: now, policy: policy.Resolved(),
	}, nil
}

func (*SystemAssetCollector) Name() string { return "windows_system_asset_snapshot" }

func (collector *SystemAssetCollector) Run(ctx context.Context, sink Sink) error {
	var previousSystem domain.AssetBaseline
	var previousSoftware domain.AssetBaseline
	hasPreviousSystem := false
	hasPreviousSoftware := false
	var lastSystemSnapshot time.Time
	var lastSoftwareSnapshot time.Time
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()
	for {
		observedUTC := collector.now().UTC()
		systemDue := cadenceDue(lastSystemSnapshot, collector.systemInterval, observedUTC)
		softwareDue := cadenceDue(lastSoftwareSnapshot, collector.softwareInterval, observedUTC)
		current, err := collector.snapshot(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if emitErr := collector.emitUnavailable(ctx, sink, err); emitErr != nil {
				return emitErr
			}
		} else {
			currentSoftware, currentSystem := splitAssetBaselineCadence(current)
			if systemDue {
				if hasPreviousSystem {
					if err := collector.emitDifferences(ctx, sink, previousSystem, currentSystem); err != nil {
						return err
					}
				}
				previousSystem, hasPreviousSystem = currentSystem, true
				lastSystemSnapshot = observedUTC
			}
			if softwareDue {
				if hasPreviousSoftware {
					if err := collector.emitDifferences(ctx, sink, previousSoftware, currentSoftware); err != nil {
						return err
					}
				}
				previousSoftware, hasPreviousSoftware = currentSoftware, true
				lastSoftwareSnapshot = observedUTC
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (collector *SystemAssetCollector) emitDifferences(ctx context.Context, sink Sink, previous, current domain.AssetBaseline) error {
	differences, err := domain.CompareAssetBaselines(previous, current)
	if err != nil {
		return err
	}
	for _, difference := range differences {
		if err := collector.emitDifference(ctx, sink, difference); err != nil {
			return err
		}
	}
	return nil
}

func cadenceDue(previous time.Time, interval time.Duration, now time.Time) bool {
	return interval > 0 && (previous.IsZero() || now.Sub(previous) >= interval)
}

func minimumActiveInterval(intervals ...time.Duration) time.Duration {
	var minimum time.Duration
	for _, interval := range intervals {
		if interval <= 0 || minimum > 0 && interval >= minimum {
			continue
		}
		minimum = interval
	}
	return minimum
}

func splitAssetBaselineCadence(baseline domain.AssetBaseline) (software domain.AssetBaseline, system domain.AssetBaseline) {
	for _, asset := range baseline.Assets {
		if asset.Category == domain.AssetCategorySoftware {
			switch asset.Attributes["inventoryKind"] {
			case "service", "driver", "scheduledTask", "startup":
				system.Assets = append(system.Assets, asset)
			default:
				software.Assets = append(software.Assets, asset)
			}
			continue
		}
		system.Assets = append(system.Assets, asset)
	}
	return software, system
}

func (collector *SystemAssetCollector) emitUnavailable(ctx context.Context, sink Sink, cause error) error {
	payload, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: cause.Error()})
	return sink.Emit(ctx, Observation{
		Category: domain.EventCategoryHealth, Action: "system_asset_snapshot_unavailable", Severity: domain.EventSeverityHigh,
		ObservedUTC: collector.now().UTC(), Source: collector.Name(), Confidence: domain.EventConfidenceDirect, Payload: payload,
	})
}

func (collector *SystemAssetCollector) emitDifference(ctx context.Context, sink Sink, difference domain.AssetDifference) error {
	asset := difference.After
	if asset == nil {
		asset = difference.Before
	}
	if asset == nil {
		return nil
	}
	if !collector.allowsDifference(asset, assetDifferenceAction(difference)) {
		return nil
	}
	payloadValue := struct {
		Difference      domain.AssetDifference `json:"difference"`
		Source          string                 `json:"source"`
		Name            string                 `json:"name,omitempty"`
		Version         string                 `json:"version,omitempty"`
		Publisher       string                 `json:"publisher,omitempty"`
		InstallScope    string                 `json:"installScope,omitempty"`
		InstallLocation string                 `json:"installLocation,omitempty"`
	}{Difference: difference, Source: collector.Name()}
	if asset.Category == domain.AssetCategorySoftware {
		payloadValue.Name = asset.DisplayName
		payloadValue.Version = asset.Attributes["version"]
		payloadValue.Publisher = asset.Attributes["publisher"]
		payloadValue.InstallScope = asset.Attributes["installScope"]
		payloadValue.InstallLocation = asset.Attributes["installLocation"]
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return fmt.Errorf("encode system asset difference: %w", err)
	}
	return sink.Emit(ctx, Observation{
		Category: assetEventCategory(asset.Category), Action: assetDifferenceAction(difference), Severity: domain.EventSeverityMedium,
		ObservedUTC: collector.now().UTC(), ObjectKey: string(asset.Category) + ":" + asset.Identifier,
		Source: collector.Name(), Confidence: domain.EventConfidenceSnapshotDiff, Payload: payload,
	})
}

func (collector *SystemAssetCollector) allowsDifference(asset *domain.Asset, action string) bool {
	inventoryKind := asset.Attributes["inventoryKind"]
	system := collector.policy.SystemAndNetwork
	switch inventoryKind {
	case "service":
		return collector.policy.SystemAndNetworkEnabled && system.MonitorServices
	case "driver":
		return collector.policy.SystemAndNetworkEnabled && system.MonitorDrivers
	case "scheduledTask":
		return collector.policy.SystemAndNetworkEnabled && system.MonitorScheduledTasks
	case "startup":
		return collector.policy.SystemAndNetworkEnabled && system.MonitorStartupItems
	}
	switch asset.Category {
	case domain.AssetCategorySoftware:
		return collector.policy.ProcessAndSoftwareEnabled
	case domain.AssetCategoryDevice:
		// Runtime device changes are emitted by DeviceCollector. Keeping a
		// second source here creates duplicate connect and disconnect events.
		return false
	case domain.AssetCategoryNetwork:
		// Runtime interface changes are emitted by NetworkCollector.
		return false
	case domain.AssetCategoryAccount:
		return collector.policy.SystemAndNetworkEnabled && system.MonitorAccounts
	case domain.AssetCategorySystem:
		if !collector.policy.SystemAndNetworkEnabled {
			return false
		}
		switch action {
		case "time_configuration_changed":
			return system.MonitorClock
		case "audit_policy_changed":
			return system.MonitorAuditPolicy
		case "firewall_configuration_changed", "security_center_status_changed":
			return system.MonitorFirewall
		case "remote_desktop_configuration_changed":
			return system.MonitorRemoteDesktop
		case "proxy_configuration_changed":
			return system.MonitorProxy
		default:
			return system.MonitorAccounts || system.MonitorNetwork || system.MonitorProxy || system.MonitorFirewall ||
				system.MonitorRemoteDesktop || system.MonitorAuditPolicy || system.MonitorClock
		}
	default:
		return false
	}
}

func assetEventCategory(category domain.AssetCategory) domain.EventCategory {
	switch category {
	case domain.AssetCategorySoftware:
		return domain.EventCategorySoftware
	case domain.AssetCategoryDevice:
		return domain.EventCategoryDevice
	default:
		return domain.EventCategorySystem
	}
}

func assetDifferenceAction(difference domain.AssetDifference) string {
	asset := difference.After
	if asset == nil {
		asset = difference.Before
	}
	if asset == nil {
		return "asset_changed"
	}
	kind := asset.Attributes["inventoryKind"]
	if kind == "service" || kind == "driver" {
		return kind + "_" + string(difference.Kind)
	}
	if kind == "scheduledTask" {
		return "scheduled_task_" + string(difference.Kind)
	}
	if kind == "startup" {
		return "startup_item_" + string(difference.Kind)
	}
	switch asset.Category {
	case domain.AssetCategorySoftware:
		switch difference.Kind {
		case domain.AssetDifferenceAdded:
			return "software_installed"
		case domain.AssetDifferenceRemoved:
			return "software_uninstalled"
		default:
			for _, attribute := range difference.ChangedAttributes {
				if attribute == "version" {
					return "software_upgraded"
				}
			}
			return "software_inventory_modified"
		}
	case domain.AssetCategoryDevice:
		if difference.Kind == domain.AssetDifferenceAdded {
			return "device_connected"
		}
		if difference.Kind == domain.AssetDifferenceRemoved {
			return "device_disconnected"
		}
		return "device_configuration_changed"
	case domain.AssetCategoryNetwork:
		return "network_configuration_changed"
	case domain.AssetCategoryAccount:
		return "account_configuration_changed"
	default:
		for _, attribute := range difference.ChangedAttributes {
			switch {
			case strings.HasPrefix(attribute, "timezone") || strings.HasPrefix(attribute, "timeService") || attribute == "timeServer" || attribute == "timeSyncStatus":
				return "time_configuration_changed"
			case strings.HasPrefix(attribute, "auditPolicy"):
				return "audit_policy_changed"
			case strings.HasPrefix(attribute, "firewall"):
				return "firewall_configuration_changed"
			case strings.HasPrefix(attribute, "remoteDesktop"):
				return "remote_desktop_configuration_changed"
			case strings.HasPrefix(attribute, "securityCenter"):
				return "security_center_status_changed"
			case strings.HasPrefix(attribute, "proxy") || strings.HasPrefix(attribute, "winHTTPProxy") || attribute == "autoConfigURL":
				return "proxy_configuration_changed"
			}
		}
		return "system_configuration_changed"
	}
}
