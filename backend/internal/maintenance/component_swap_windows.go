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
	Name       string
	TargetPath string
	StagedPath string
	newPath    string
	backupPath string
	backedUp   bool
	activated  bool
}

type ComponentSwap struct {
	replacements []ComponentReplacement
	rename       func(string, string) error
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
		if pair.Name == "" || !filepath.IsAbs(pair.TargetPath) || !filepath.IsAbs(pair.StagedPath) || samePath(pair.TargetPath, pair.StagedPath) {
			swap.cleanupPrepared()
			return nil, ErrInstallLayoutInvalid
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
		if err := copyReplacementFile(pair.StagedPath, pair.newPath); err != nil {
			swap.cleanupPrepared()
			return nil, fmt.Errorf("prepare component %q: %w", pair.Name, err)
		}
		swap.replacements = append(swap.replacements, pair)
	}
	return swap, nil
}

func (swap *ComponentSwap) Activate() error {
	for index := range swap.replacements {
		replacement := &swap.replacements[index]
		if err := swap.rename(replacement.TargetPath, replacement.backupPath); err != nil {
			return errors.Join(fmt.Errorf("backup component %q: %w", replacement.Name, err), swap.Rollback())
		}
		replacement.backedUp = true
		if err := swap.rename(replacement.newPath, replacement.TargetPath); err != nil {
			return errors.Join(fmt.Errorf("activate component %q: %w", replacement.Name, err), swap.Rollback())
		}
		replacement.activated = true
	}
	return nil
}

func (swap *ComponentSwap) Rollback() error {
	var rollbackErrors []error
	for index := len(swap.replacements) - 1; index >= 0; index-- {
		replacement := &swap.replacements[index]
		if replacement.backedUp {
			if replacement.activated {
				if err := os.Remove(replacement.TargetPath); err != nil && !os.IsNotExist(err) {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("remove replacement %q; backup %q: %w", replacement.TargetPath, replacement.backupPath, err))
					continue
				}
			}
			if err := swap.rename(replacement.backupPath, replacement.TargetPath); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore component %q from backup %s: %w", replacement.Name, replacement.backupPath, err))
				continue
			}
			replacement.activated = false
			replacement.backedUp = false
		}
		_ = os.Remove(replacement.newPath)
	}
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
		_ = os.Remove(replacement.newPath)
	}
}

func copyReplacementFile(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
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
