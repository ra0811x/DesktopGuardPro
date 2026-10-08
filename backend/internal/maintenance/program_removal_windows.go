package maintenance

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	winapi "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type removalScheduler func(string) error

const (
	sessionManagerRegistryPath = `SYSTEM\CurrentControlSet\Control\Session Manager`
	pendingRenameValueName     = "PendingFileRenameOperations"
)

func ScheduleInstalledProgramRemoval(manifest InstallManifest) error {
	return scheduleInstalledProgramRemoval(
		manifest,
		(windowsPreflightProbe{}).TrustInstallDirectory,
		scheduleRemovalOnRestart,
	)
}

func scheduleInstalledProgramRemoval(manifest InstallManifest, trustDirectory func(string) error, schedule removalScheduler) error {
	if err := validateInstallManifest(manifest); err != nil {
		return err
	}
	installDirectory := filepath.Dir(manifest.Components[0].Path)
	if err := trustDirectory(installDirectory); err != nil {
		return err
	}
	paths := make([]string, 0, len(manifest.Components)+2)
	for _, component := range manifest.Components {
		if !samePath(filepath.Dir(component.Path), installDirectory) {
			return ErrInstallManifestInvalid
		}
		paths = append(paths, component.Path)
	}
	directories := make(map[string]bool)
	for _, file := range manifest.RuntimeFiles {
		path, err := runtimeFilePath(installDirectory, file.Path)
		if err != nil {
			return err
		}
		paths = append(paths, path)
		for parent := filepath.Dir(path); !samePath(parent, installDirectory); parent = filepath.Dir(parent) {
			directories[parent] = true
		}
	}
	paths = append(paths, filepath.Join(installDirectory, "release-manifest.json"))
	orderedDirectories := make([]string, 0, len(directories))
	for directory := range directories {
		orderedDirectories = append(orderedDirectories, directory)
	}
	sort.Slice(orderedDirectories, func(i, j int) bool { return len(orderedDirectories[i]) > len(orderedDirectories[j]) })
	paths = append(paths, orderedDirectories...)
	if manifest.SchemaVersion == 1 {
		paths = append(paths, filepath.Join(installDirectory, "desktop-guard-maintenance.exe"))
	}
	paths = append(paths, installDirectory)
	for _, path := range paths {
		if err := schedule(path); err != nil {
			return fmt.Errorf("schedule removal %q: %w", path, err)
		}
	}
	return nil
}

func scheduleRemovalOnRestart(path string) error {
	pathUTF16, err := winapi.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return winapi.MoveFileEx(pathUTF16, nil, winapi.MOVEFILE_DELAY_UNTIL_REBOOT)
}

func installedProgramPaths(installDirectory string) []string {
	return []string{
		filepath.Join(installDirectory, "desktop-guard-service.exe"),
		filepath.Join(installDirectory, "desktop-guard-ui.exe"),
		filepath.Join(installDirectory, "desktop-guard-agent.exe"),
		filepath.Join(installDirectory, "desktop-guard-maintenance.exe"),
		installDirectory,
	}
}

func cancelScheduledProgramRemoval(installDirectory string) error {
	if err := (windowsPreflightProbe{}).TrustInstallDirectory(installDirectory); err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, sessionManagerRegistryPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	values, _, err := key.GetStringsValue(pendingRenameValueName)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	filtered, changed, err := filterPendingProgramRemovals(values, installedProgramPaths(installDirectory))
	if err != nil || !changed {
		return err
	}
	if len(filtered) == 0 {
		return key.DeleteValue(pendingRenameValueName)
	}
	return key.SetStringsValue(pendingRenameValueName, filtered)
}

func filterPendingProgramRemovals(values, programPaths []string) ([]string, bool, error) {
	if len(values)%2 != 0 {
		return nil, false, errors.New("invalid pending file rename operations")
	}
	filtered := make([]string, 0, len(values))
	changed := false
	for index := 0; index < len(values); index += 2 {
		source, destination := values[index], values[index+1]
		candidate := strings.TrimPrefix(source, `\??\`)
		remove := destination == ""
		if remove {
			remove = false
			for _, path := range programPaths {
				relative, relErr := filepath.Rel(path, candidate)
				if samePath(candidate, path) || (relErr == nil && filepath.IsLocal(relative)) {
					remove = true
					break
				}
			}
		}
		if remove {
			changed = true
			continue
		}
		filtered = append(filtered, source, destination)
	}
	return filtered, changed, nil
}
