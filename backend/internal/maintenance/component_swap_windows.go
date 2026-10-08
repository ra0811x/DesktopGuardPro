package maintenance

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ComponentReplacement struct {
	Name        string
	TargetPath  string
	StagedPath  string
	AllowCreate bool
	Remove      bool
	SHA256      string
	newPath     string
	backupPath  string
	backedUp    bool
	activated   bool
}

type ComponentSwap struct {
	replacements       []ComponentReplacement
	rename             func(string, string) error
	createdDirectories []string
}

func PrepareComponentSwap(pairs []ComponentReplacement) (*ComponentSwap, error) {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, err
	}
	return prepareComponentSwap(pairs, hex.EncodeToString(randomBytes), os.Rename)
}

func prepareComponentSwap(pairs []ComponentReplacement, suffix string, rename func(string, string) error) (*ComponentSwap, error) {
	if len(pairs) == 0 || suffix == "" || rename == nil {
		return nil, ErrInstallLayoutInvalid
	}
	swap := &ComponentSwap{rename: rename}
	for _, pair := range pairs {
		if pair.Name == "" || !filepath.IsAbs(pair.TargetPath) ||
			(!pair.Remove && (!filepath.IsAbs(pair.StagedPath) || samePath(pair.TargetPath, pair.StagedPath))) ||
			(pair.Remove && pair.StagedPath != "") {
			swap.cleanupPrepared()
			return nil, ErrInstallLayoutInvalid
		}
		if pair.AllowCreate {
			if err := swap.createParents(filepath.Dir(pair.TargetPath)); err != nil {
				swap.cleanupPrepared()
				return nil, err
			}
		}
		entries, err := os.ReadDir(filepath.Dir(pair.TargetPath))
		if err != nil {
			swap.cleanupPrepared()
			return nil, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(strings.ToLower(entry.Name()), strings.ToLower(filepath.Base(pair.TargetPath)+".old-")) {
				swap.cleanupPrepared()
				return nil, fmt.Errorf("unfinished component replacement; recover backup %q before retrying", filepath.Join(filepath.Dir(pair.TargetPath), entry.Name()))
			}
		}
		pair.newPath = pair.TargetPath + ".new-" + suffix
		pair.backupPath = pair.TargetPath + ".old-" + suffix
		if !pair.Remove {
			if err := copyReplacementFile(pair.StagedPath, pair.newPath); err != nil {
				swap.cleanupPrepared()
				return nil, fmt.Errorf("prepare component %q: %w", pair.Name, err)
			}
			if pair.SHA256 != "" {
				actual, err := inspectRuntimeFile(filepath.Dir(pair.newPath), filepath.Base(pair.newPath))
				if err != nil || !strings.EqualFold(actual.SHA256, pair.SHA256) {
					_ = os.Remove(pair.newPath)
					swap.cleanupPrepared()
					return nil, fmt.Errorf("%w: staged file %q changed", ErrComponentInvalid, pair.Name)
				}
			}
		}
		swap.replacements = append(swap.replacements, pair)
	}
	return swap, nil
}

func (swap *ComponentSwap) Activate() error {
	for index := range swap.replacements {
		replacement := &swap.replacements[index]
		if err := swap.rename(replacement.TargetPath, replacement.backupPath); err != nil {
			if !replacement.AllowCreate || !os.IsNotExist(err) {
				return errors.Join(fmt.Errorf("backup component %q: %w", replacement.Name, err), swap.Rollback())
			}
		} else {
			replacement.backedUp = true
		}
		if !replacement.Remove {
			if err := swap.rename(replacement.newPath, replacement.TargetPath); err != nil {
				return errors.Join(fmt.Errorf("activate component %q: %w", replacement.Name, err), swap.Rollback())
			}
		}
		replacement.activated = true
	}
	return nil
}

func (swap *ComponentSwap) Rollback() error {
	var rollbackErrors []error
	for index := len(swap.replacements) - 1; index >= 0; index-- {
		replacement := &swap.replacements[index]
		if replacement.activated && !replacement.Remove {
			if err := os.Remove(replacement.TargetPath); err != nil && !os.IsNotExist(err) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove replacement %q; backup %q: %w", replacement.TargetPath, replacement.backupPath, err))
				continue
			}
		}
		if replacement.backedUp {
			if err := swap.rename(replacement.backupPath, replacement.TargetPath); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore component %q from backup %s: %w", replacement.Name, replacement.backupPath, err))
				continue
			}
			replacement.activated = false
			replacement.backedUp = false
		}
		replacement.activated = false
		if !replacement.Remove {
			_ = os.Remove(replacement.newPath)
		}
	}
	swap.cleanupDirectories()
	return errors.Join(rollbackErrors...)
}

func (swap *ComponentSwap) Commit() error {
	var cleanupErrors []error
	for index := range swap.replacements {
		replacement := &swap.replacements[index]
		if !replacement.activated {
			return errors.New("component swap is not active")
		}
		if err := os.Remove(replacement.backupPath); err != nil && !os.IsNotExist(err) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (swap *ComponentSwap) cleanupPrepared() {
	for _, replacement := range swap.replacements {
		if !replacement.Remove {
			_ = os.Remove(replacement.newPath)
		}
	}
	swap.cleanupDirectories()
}

func (swap *ComponentSwap) createParents(directory string) error {
	info, err := os.Lstat(directory)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInstallLayoutInvalid
		}
		return nil
	}
	if !os.IsNotExist(err) || filepath.Dir(directory) == directory {
		return err
	}
	if err := swap.createParents(filepath.Dir(directory)); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return err
	}
	swap.createdDirectories = append(swap.createdDirectories, directory)
	return nil
}

func (swap *ComponentSwap) cleanupDirectories() {
	for index := len(swap.createdDirectories) - 1; index >= 0; index-- {
		_ = os.Remove(swap.createdDirectories[index]) // Remove only empty directories.
	}
}

func copyReplacementFile(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ErrComponentInvalid
	}
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	keep := true
	defer func() {
		_ = destination.Close()
		if keep {
			_ = os.Remove(destinationPath)
		}
	}()
	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	if err := destination.Close(); err != nil {
		return err
	}
	keep = false
	return nil
}
