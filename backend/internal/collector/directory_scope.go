package collector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const MaximumMonitoredDirectories = 16

// NormalizeDirectoryRoots keeps recursive watches disjoint and excludes the
// application's audit database so its own writes cannot generate more writes.
func NormalizeDirectoryRoots(roots []string, dataDirectory string) ([]string, error) {
	if len(roots) > MaximumMonitoredDirectories {
		return nil, fmt.Errorf("最多可配置 %d 个重点目录", MaximumMonitoredDirectories)
	}
	if dataDirectory != "" {
		resolved, err := filepath.EvalSymlinks(dataDirectory)
		if err != nil {
			return nil, fmt.Errorf("resolve application data directory: %w", err)
		}
		dataDirectory = resolved
	}
	normalized := []string{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if !filepath.IsAbs(root) || len(filepath.VolumeName(root)) != 2 {
			return nil, errors.New("重点目录必须使用本地磁盘的完整路径")
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, fmt.Errorf("无法访问重点目录 %q: %w", root, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("重点目录 %q 必须是已存在的文件夹", root)
		}
		if dataDirectory != "" && (directoryContains(resolved, dataDirectory) || directoryContains(dataDirectory, resolved)) {
			return nil, errors.New("重点目录不能包含应用数据目录或位于其中")
		}
		covered := false
		for _, existing := range normalized {
			if directoryContains(existing, resolved) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		kept := normalized[:0]
		for _, existing := range normalized {
			if !directoryContains(resolved, existing) {
				kept = append(kept, existing)
			}
		}
		normalized = append(kept, resolved)
	}
	return normalized, nil
}

// NormalizeMonitoringTargets resolves configured objects before a session
// starts so the operator gets an actionable error for unavailable paths and
// reparse points instead of a generic collector failure later.
func NormalizeMonitoringTargets(
	targets []domain.MonitoringTarget,
	dataDirectory string,
) ([]domain.MonitoringTarget, error) {
	if err := domain.ValidateMonitoringTargets(targets); err != nil {
		return nil, err
	}
	directoryRoots := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Kind == domain.MonitoringTargetKindDirectory || target.Kind == domain.MonitoringTargetKindRemovableVolume {
			directoryRoots = append(directoryRoots, target.Path)
		}
	}
	if _, err := NormalizeDirectoryRoots(directoryRoots, dataDirectory); err != nil {
		return nil, err
	}
	normalized := domain.CloneMonitoringTargets(targets)
	for index := range normalized {
		target := &normalized[index]
		configuredPath := strings.TrimSpace(target.Path)
		target.ResolutionStatus = domain.MonitoringTargetResolutionAvailable
		target.ConfiguredPath = ""
		target.ResolutionDetail = "路径已验证，可以监控"
		if target.Kind == domain.MonitoringTargetKindDirectory || target.Kind == domain.MonitoringTargetKindRemovableVolume {
			resolved, err := filepath.EvalSymlinks(configuredPath)
			if err != nil {
				return nil, fmt.Errorf("无法访问监控路径 %q: %w", target.Path, err)
			}
			target.Path = resolved
			if !strings.EqualFold(filepath.Clean(configuredPath), filepath.Clean(resolved)) {
				target.ConfiguredPath = configuredPath
				target.ResolutionStatus = domain.MonitoringTargetResolutionReparseResolved
				target.ResolutionDetail = fmt.Sprintf("重解析路径已解析为 %s", resolved)
			}
			continue
		}
		path := configuredPath
		if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 {
			return nil, errors.New("重点文件必须使用本地磁盘的完整路径")
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return nil, fmt.Errorf("无法访问重点文件的目录 %q: %w", path, err)
		}
		info, err := os.Stat(parent)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("重点文件 %q 的目录不可用", path)
		}
		if dataDirectory != "" && directoryContains(parent, dataDirectory) {
			return nil, errors.New("重点文件不能位于应用数据目录中")
		}
		resolved := filepath.Join(parent, filepath.Base(path))
		target.Path = resolved
		if !strings.EqualFold(filepath.Clean(configuredPath), filepath.Clean(resolved)) {
			target.ConfiguredPath = configuredPath
			target.ResolutionStatus = domain.MonitoringTargetResolutionReparseResolved
			target.ResolutionDetail = fmt.Sprintf("重点文件目录的重解析路径已解析为 %s", resolved)
		}
	}
	return normalized, nil
}

func directoryContains(root, path string) bool {
	root = strings.ToLower(filepath.Clean(root))
	path = strings.ToLower(filepath.Clean(path))
	return root == path || strings.HasPrefix(path, strings.TrimRight(root, `\`)+`\`)
}

func SystemCollectorsForDirectories(roots []string) ([]Collector, error) {
	targets := make([]domain.MonitoringTarget, 0, len(roots))
	for _, root := range roots {
		targets = append(targets, domain.MonitoringTarget{
			Path: root, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
		})
	}
	return SystemCollectorsForMonitoringTargets(targets)
}

func SystemCollectorsForMonitoringTargets(targets []domain.MonitoringTarget) ([]Collector, error) {
	return SystemCollectorsForMonitoringTargetsWithExclusions(targets, nil)
}

func SystemCollectorsForMonitoringTargetsWithExclusions(
	targets []domain.MonitoringTarget,
	exclusions []domain.MonitoringExclusion,
) ([]Collector, error) {
	return SystemCollectorsForMonitoringProfile(targets, exclusions, domain.MonitoringLevelStandard)
}

func SystemCollectorsForMonitoringProfile(
	targets []domain.MonitoringTarget,
	exclusions []domain.MonitoringExclusion,
	level domain.MonitoringLevel,
) ([]Collector, error) {
	policy := domain.DefaultMonitoringPolicy()
	policy.StrictReadAuditEnabled = level == domain.MonitoringLevelStrict
	return SystemCollectorsForMonitoringPolicy(targets, exclusions, policy)
}

func SystemCollectorsForMonitoringPolicy(
	targets []domain.MonitoringTarget,
	exclusions []domain.MonitoringExclusion,
	policy domain.MonitoringPolicy,
) ([]Collector, error) {
	if err := domain.ValidateMonitoringTargets(targets); err != nil {
		return nil, err
	}
	if err := domain.ValidateMonitoringExclusions(exclusions); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	collectors := make([]Collector, 0)
	if policy.ProcessAndSoftwareEnabled || systemPolicyNeedsAssetSnapshot(policy) {
		assets, err := NewSystemAssetCollectorWithPolicy(policy)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, assets)
	}
	if policy.SystemAndNetworkEnabled && policy.SystemAndNetwork.MonitorNetwork {
		network, err := NewNetworkCollector(time.Duration(policy.SystemAndNetwork.SnapshotIntervalSeconds) * time.Second)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, network)
	}
	if policy.ProcessAndSoftwareEnabled || policy.SystemAndNetworkEnabled {
		registryCollectors, err := RegistryCollectorsForMonitoringPolicy(policy)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, registryCollectors...)
	}
	if policy.ProcessAndSoftwareEnabled {
		processes, err := NewProcessCollectorWithOptions(ProcessCollectorOptions{
			Interval: time.Duration(policy.ProcessAndSoftware.SnapshotIntervalSeconds) * time.Second,
		})
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, processes)
	}
	if policy.ExternalDevicesEnabled {
		devices, err := NewDeviceCollectorWithOptions(DeviceCollectorOptions{
			Interval:         time.Duration(policy.ExternalDevices.SnapshotIntervalSeconds) * time.Second,
			RecordConnect:    policy.ExternalDevices.RecordConnect,
			RecordDisconnect: policy.ExternalDevices.RecordDisconnect,
		})
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, devices)
	}
	if policy.SystemAndNetworkEnabled {
		if policy.SystemAndNetwork.MonitorSecurityLog {
			securityLog, err := NewSecurityLogCollector()
			if err != nil {
				return nil, err
			}
			collectors = append(collectors, securityLog)
		}
		if policy.SystemAndNetwork.MonitorClock {
			clockJump, err := NewClockJumpCollector()
			if err != nil {
				return nil, err
			}
			collectors = append(collectors, clockJump)
		}
	}
	if policy.FileActivityEnabled {
		for _, target := range targets {
			var directory *DirectoryCollector
			var err error
			switch target.Kind {
			case domain.MonitoringTargetKindDirectory:
				directory, err = NewDirectoryCollector(target.Path, target.Recursive)
			case domain.MonitoringTargetKindFile:
				directory, err = NewFileCollector(target.Path)
			case domain.MonitoringTargetKindRemovableVolume:
				directory, err = NewDirectoryCollector(target.Path, true)
			default:
				return nil, fmt.Errorf("当前采集器暂不支持监控目标类型 %q", target.Kind)
			}
			if err != nil {
				return nil, err
			}
			if target.Kind == domain.MonitoringTargetKindRemovableVolume {
				directory.removableVolume = true
			}
			directory.SetAuditPolicy(policy.File)
			directory.exclusions = append([]domain.MonitoringExclusion(nil), exclusions...)
			collectors = append(collectors, directory)
		}
	}
	if policy.StrictReadAuditEnabled {
		strictAudit, err := NewStrictReadAuditCollector(targets, exclusions)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, strictAudit)
	}
	return collectors, nil
}

func systemPolicyNeedsAssetSnapshot(policy domain.MonitoringPolicy) bool {
	if !policy.SystemAndNetworkEnabled {
		return false
	}
	system := policy.SystemAndNetwork
	return system.MonitorAccounts || system.MonitorProxy || system.MonitorFirewall || system.MonitorRemoteDesktop ||
		system.MonitorAuditPolicy || system.MonitorClock || system.MonitorServices || system.MonitorDrivers ||
		system.MonitorScheduledTasks || system.MonitorStartupItems
}
