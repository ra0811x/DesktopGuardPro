package maintenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// RuntimeFileRecord covers the native assembly, dependencies, configuration,
// licenses and nested resources alongside the four signed executable records.
type RuntimeFileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func runtimeFilePath(directory, relative string) (string, error) {
	if !filepath.IsAbs(directory) || !filepath.IsLocal(relative) ||
		filepath.ToSlash(filepath.Clean(relative)) != relative || relative == "." ||
		strings.Contains(relative, ":") {
		return "", ErrInstallManifestInvalid
	}
	path := filepath.Join(directory, filepath.FromSlash(relative))
	// Reject reparse points in every existing parent, including the root.
	for current := path; ; current = filepath.Dir(current) {
		pointer, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return "", err
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return "", ErrInstallManifestInvalid
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if samePath(current, directory) {
			break
		}
		if filepath.Dir(current) == current {
			return "", ErrInstallManifestInvalid
		}
	}
	return path, nil
}

func isPrimaryComponent(relative string) bool {
	for _, name := range []string{"service", "ui", "agent", "maintenance"} {
		if strings.EqualFold(relative, "desktop-guard-"+name+".exe") {
			return true
		}
	}
	return false
}

// CollectRuntimeFiles is shared by the release builder and maintenance paths.
// The release manifest is excluded because it cannot contain its own digest.
func CollectRuntimeFiles(directory string) ([]RuntimeFileRecord, error) {
	return collectRuntimeFiles(directory, false)
}

func collectRuntimeFiles(directory string, skipBackups bool) ([]RuntimeFileRecord, error) {
	files := make([]RuntimeFileRecord, 0)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == directory {
			_, err := runtimeFilePath(directory, "release-manifest.json")
			return err
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, err := runtimeFilePath(directory, relative); err != nil {
			return err
		}
		name := strings.ToLower(entry.Name())
		if strings.Contains(name, ".old-") || strings.Contains(name, ".new-") {
			if !skipBackups {
				return fmt.Errorf("unfinished runtime replacement: %s", relative)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if isPrimaryComponent(relative) || strings.EqualFold(relative, "release-manifest.json") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return ErrComponentInvalid
		}
		// Native release files may legitimately be empty.
		record, err := inspectRuntimeFile(directory, relative)
		if err != nil {
			return err
		}
		files = append(files, record)
		if len(files) > 4096 {
			return ErrInstallManifestInvalid
		}
		return nil
	})
	return files, err
}

func inspectRuntimeFile(directory, relative string) (RuntimeFileRecord, error) {
	path, err := runtimeFilePath(directory, relative)
	if err != nil {
		return RuntimeFileRecord{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return RuntimeFileRecord{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return RuntimeFileRecord{}, ErrComponentInvalid
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return RuntimeFileRecord{}, err
	}
	return RuntimeFileRecord{Path: relative, Size: info.Size(), SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func validateRuntimeRecords(records []RuntimeFileRecord) error {
	if len(records) > 4096 {
		return ErrInstallManifestInvalid
	}
	seen := make(map[string]bool)
	for _, record := range records {
		if !filepath.IsLocal(record.Path) || filepath.ToSlash(filepath.Clean(record.Path)) != record.Path ||
			record.Path == "." || isPrimaryComponent(record.Path) || strings.EqualFold(record.Path, "release-manifest.json") ||
			strings.Contains(record.Path, ":") || record.Size < 0 || len(record.SHA256) != 64 || seen[strings.ToLower(record.Path)] {
			return ErrInstallManifestInvalid
		}
		if _, err := hex.DecodeString(record.SHA256); err != nil {
			return ErrInstallManifestInvalid
		}
		seen[strings.ToLower(record.Path)] = true
	}
	return nil
}

func verifyRuntimeFiles(directory string, records []RuntimeFileRecord) error {
	if err := validateRuntimeRecords(records); err != nil {
		return err
	}
	for _, record := range records {
		actual, err := inspectRuntimeFile(directory, record.Path)
		if err != nil || actual.Size != record.Size || !strings.EqualFold(actual.SHA256, record.SHA256) {
			return fmt.Errorf("%w: runtime file %q changed", ErrInstallManifestInvalid, record.Path)
		}
	}
	return nil
}

func readReleaseRuntimeFiles(directory string, requireExactFiles bool) ([]RuntimeFileRecord, error) {
	path, err := runtimeFilePath(directory, "release-manifest.json")
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return collectRuntimeFiles(directory, !requireExactFiles)
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumManifestSize {
		return nil, ErrInstallManifestInvalid
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		RuntimeFiles *[]RuntimeFileRecord `json:"runtimeFiles"`
	}
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return nil, err
	}
	if manifest.RuntimeFiles == nil {
		return collectRuntimeFiles(directory, !requireExactFiles)
	}
	files := *manifest.RuntimeFiles
	if err := verifyRuntimeFiles(directory, files); err != nil {
		return nil, err
	}
	if requireExactFiles {
		actual, err := CollectRuntimeFiles(directory)
		if err != nil {
			return nil, err
		}
		if len(actual) != len(files) {
			return nil, errors.New("release runtime inventory is incomplete")
		}
		declared := make(map[string]bool)
		for _, file := range files {
			declared[strings.ToLower(file.Path)] = true
		}
		for _, file := range actual {
			if !declared[strings.ToLower(file.Path)] {
				return nil, ErrInstallManifestInvalid
			}
		}
	}
	return files, nil
}
