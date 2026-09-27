package collector

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"desktopguardpro/internal/domain"
)

func TestDirectoryScopeNormalizesAndAvoidsDuplicateWatches(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "工作资料")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := NormalizeDirectoryRoots([]string{child, root, root}, t.TempDir())
	if err != nil || !reflect.DeepEqual(got, []string{root}) {
		t.Fatalf("normalized roots = %v, error = %v", got, err)
	}
	collectors, err := SystemCollectorsForDirectories(got)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, current := range collectors {
		if directory, ok := current.(*DirectoryCollector); ok && directory.root == root && directory.watchSubtree {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("system collector set contains %d configured directory collectors, want 1", found)
	}
}

func TestDirectoryScopeRejectsInvalidAndSelfObservedPaths(t *testing.T) {
	data := t.TempDir()
	for _, root := range []string{"", "relative", `\\server\share`, `\\?\C:\`, filepath.Join(data, "missing"), data, filepath.Dir(data)} {
		if _, err := NormalizeDirectoryRoots([]string{root}, data); err == nil {
			t.Errorf("accepted invalid root %q", root)
		}
	}
}

func TestNormalizeMonitoringTargetsRejectsUnavailablePathsAndPreservesKinds(t *testing.T) {
	directory := t.TempDir()
	targets, err := NormalizeMonitoringTargets([]domain.MonitoringTarget{
		{Path: directory, Kind: domain.MonitoringTargetKindDirectory, Recursive: false},
		{Path: filepath.Join(directory, "focus.txt"), Kind: domain.MonitoringTargetKindFile},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeMonitoringTargets() error = %v", err)
	}
	if len(targets) != 2 || targets[0].Kind != domain.MonitoringTargetKindDirectory ||
		targets[0].Recursive || targets[1].Kind != domain.MonitoringTargetKindFile ||
		targets[0].ResolutionStatus != domain.MonitoringTargetResolutionAvailable ||
		targets[1].ResolutionStatus != domain.MonitoringTargetResolutionAvailable {
		t.Fatalf("normalized targets = %#v", targets)
	}
	if _, err := NormalizeMonitoringTargets([]domain.MonitoringTarget{{
		Path: filepath.Join(directory, "missing", "focus.txt"), Kind: domain.MonitoringTargetKindFile,
	}}, t.TempDir()); err == nil {
		t.Fatal("NormalizeMonitoringTargets() accepted a file whose parent directory is unavailable")
	}
}

func TestNormalizeMonitoringTargetsReturnsResolvedReparsePath(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	normalized, err := NormalizeMonitoringTargets([]domain.MonitoringTarget{{
		Path: link, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 || !sameFilePath(normalized[0].Path, target) {
		t.Fatalf("resolved target = %#v, want %q", normalized, target)
	}
	if !sameFilePath(normalized[0].ConfiguredPath, link) ||
		normalized[0].ResolutionStatus != domain.MonitoringTargetResolutionReparseResolved ||
		normalized[0].ResolutionDetail == "" {
		t.Fatalf("reparse resolution result = %#v", normalized[0])
	}
}

func sameFilePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func TestSystemCollectorsForMonitoringTargetsPreservesRecursion(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	collectors, err := SystemCollectorsForMonitoringTargets([]domain.MonitoringTarget{
		{Path: first, Kind: domain.MonitoringTargetKindDirectory, Recursive: false},
		{Path: second, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
	})
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringTargets() error = %v", err)
	}
	watchModes := make(map[string]bool)
	for _, current := range collectors {
		if directory, ok := current.(*DirectoryCollector); ok {
			watchModes[directory.root] = directory.watchSubtree
		}
	}
	if watchModes[first] || !watchModes[second] {
		t.Fatalf("directory watch modes = %#v", watchModes)
	}
}

func TestSystemCollectorsForMonitoringTargetsCreatesFileCollector(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "focus.txt")
	collectors, err := SystemCollectorsForMonitoringTargets([]domain.MonitoringTarget{
		{Path: target, Kind: domain.MonitoringTargetKindFile},
	})
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringTargets() error = %v", err)
	}
	for _, current := range collectors {
		if fileCollector, ok := current.(*DirectoryCollector); ok && fileCollector.targetFile == "focus.txt" {
			if fileCollector.root != root || fileCollector.watchSubtree {
				t.Fatalf("file collector = %+v", fileCollector)
			}
			return
		}
	}
	t.Fatal("file target did not create a file collector")
}

func TestSystemCollectorsForMonitoringTargetsCreatesRemovableVolumeCollector(t *testing.T) {
	volumeRoot := t.TempDir()
	collectors, err := SystemCollectorsForMonitoringTargets([]domain.MonitoringTarget{{
		Path: volumeRoot, Kind: domain.MonitoringTargetKindRemovableVolume,
	}})
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringTargets() error = %v", err)
	}
	for _, current := range collectors {
		if volumeCollector, ok := current.(*DirectoryCollector); ok && volumeCollector.root == volumeRoot {
			if !volumeCollector.watchSubtree {
				t.Fatal("removable volume collector is not recursive")
			}
			return
		}
	}
	t.Fatal("removable volume target did not create a collector")
}

func TestSystemCollectorsForMonitoringTargetsPassesExclusionsToFileCollectors(t *testing.T) {
	root := t.TempDir()
	exclusions := []domain.MonitoringExclusion{{
		Kind: domain.MonitoringExclusionKindExtension, Pattern: ".tmp",
	}}
	collectors, err := SystemCollectorsForMonitoringTargetsWithExclusions([]domain.MonitoringTarget{
		{Path: root, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
	}, exclusions)
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringTargetsWithExclusions() error = %v", err)
	}
	for _, current := range collectors {
		if directory, ok := current.(*DirectoryCollector); ok && directory.root == root {
			if !reflect.DeepEqual(directory.exclusions, exclusions) {
				t.Fatalf("collector exclusions = %#v", directory.exclusions)
			}
			return
		}
	}
	t.Fatal("directory collector was not created")
}

func TestSystemCollectorsForStrictMonitoringAddsObjectAccessAuditCollector(t *testing.T) {
	collectors, err := SystemCollectorsForMonitoringProfile([]domain.MonitoringTarget{{
		Path: t.TempDir(), Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, nil, domain.MonitoringLevelStrict)
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringProfile() error = %v", err)
	}
	for _, current := range collectors {
		if _, ok := current.(*StrictReadAuditCollector); ok {
			return
		}
	}
	t.Fatal("strict monitoring did not add the object access audit collector")
}

func TestSystemCollectorsForMonitoringPolicyOnlyStartsEnabledCapabilities(t *testing.T) {
	policy := domain.MonitoringPolicy{FileActivityEnabled: true}
	collectors, err := SystemCollectorsForMonitoringPolicy([]domain.MonitoringTarget{{
		Path: t.TempDir(), Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, nil, policy)
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringPolicy() error = %v", err)
	}
	if len(collectors) != 1 {
		t.Fatalf("collector count = %d, want 1", len(collectors))
	}
	if _, ok := collectors[0].(*DirectoryCollector); !ok {
		t.Fatalf("collector type = %T, want *DirectoryCollector", collectors[0])
	}
}

func TestSystemCollectorsForMonitoringPolicyAppliesFileDetail(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.ProcessAndSoftwareEnabled = false
	policy.SystemAndNetworkEnabled = false
	policy.ExternalDevicesEnabled = false
	policy.UserSessionActivityEnabled = false
	policy.File.RecordDelete = false
	policy.File.CaptureContentHash = false
	collectors, err := SystemCollectorsForMonitoringPolicy([]domain.MonitoringTarget{{
		Path: t.TempDir(), Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	directory, ok := collectors[0].(*DirectoryCollector)
	if !ok || directory.policy.RecordDelete || directory.policy.CaptureContentHash {
		t.Fatalf("directory policy = %+v", directory.policy)
	}
}

func TestSystemCollectorsForMonitoringPolicyAddsStrictAuditOnlyWhenEnabled(t *testing.T) {
	policy := domain.MonitoringPolicy{FileActivityEnabled: true, StrictReadAuditEnabled: true}
	collectors, err := SystemCollectorsForMonitoringPolicy([]domain.MonitoringTarget{{
		Path: t.TempDir(), Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}, nil, policy)
	if err != nil {
		t.Fatalf("SystemCollectorsForMonitoringPolicy() error = %v", err)
	}
	if len(collectors) != 2 {
		t.Fatalf("collector count = %d, want 2", len(collectors))
	}
	if _, ok := collectors[1].(*StrictReadAuditCollector); !ok {
		t.Fatalf("collector type = %T, want *StrictReadAuditCollector", collectors[1])
	}
}
