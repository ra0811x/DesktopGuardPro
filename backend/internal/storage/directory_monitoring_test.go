package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"desktopguardpro/internal/domain"
)

func TestMonitoredDirectoriesPersistEncryptedAndRejectTampering(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	key := bytes.Repeat([]byte{0x52}, 32)
	db, repository := openTestRepository(t, path, key)
	directories, err := repository.LoadMonitoredDirectories(ctx)
	if err != nil || len(directories) != 0 {
		t.Fatalf("initial directories = %v, error = %v", directories, err)
	}
	want := []string{`C:\Users\Raymond\Documents`, `D:\工作资料`}
	if err := repository.SaveMonitoredDirectories(ctx, want); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := db.QueryRowContext(ctx, "SELECT ciphertext FROM directory_monitoring WHERE id = 1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("Raymond")) {
		t.Fatal("directory paths were stored in plaintext")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, repository = openTestRepository(t, path, key)
	defer db.Close()
	directories, err = repository.LoadMonitoredDirectories(ctx)
	if err != nil || !reflect.DeepEqual(directories, want) {
		t.Fatalf("reloaded directories = %v, error = %v", directories, err)
	}
	if err := repository.SaveMonitoredDirectories(ctx, []string{want[1]}); err != nil {
		t.Fatal(err)
	}
	directories, err = repository.LoadMonitoredDirectories(ctx)
	if err != nil || !reflect.DeepEqual(directories, []string{want[1]}) {
		t.Fatalf("updated directories = %v, error = %v", directories, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE directory_monitoring SET ciphertext = zeroblob(length(ciphertext))"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.LoadMonitoredDirectories(ctx); !errors.Is(err, ErrPayloadAuthentication) {
		t.Fatalf("tampered settings error = %v", err)
	}
}

func TestMonitoringTargetsPersistAndLoadLegacyDirectoryArrays(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	key := bytes.Repeat([]byte{0x54}, 32)
	db, repository := openTestRepository(t, path, key)
	defer db.Close()
	targets := []domain.MonitoringTarget{
		{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
		{Path: `C:\Evidence\summary.txt`, Kind: domain.MonitoringTargetKindFile},
		{Path: `E:\`, Kind: domain.MonitoringTargetKindRemovableVolume},
	}
	if err := repository.SaveMonitoringTargets(ctx, targets); err != nil {
		t.Fatalf("SaveMonitoringTargets() error = %v", err)
	}
	loaded, err := repository.LoadMonitoringTargets(ctx)
	if err != nil || !reflect.DeepEqual(loaded, targets) {
		t.Fatalf("LoadMonitoringTargets() = %#v, %v", loaded, err)
	}

	legacyPayload, err := json.Marshal([]string{`D:\Legacy`})
	if err != nil {
		t.Fatalf("marshal legacy directories: %v", err)
	}
	nonce, ciphertext, err := repository.cipher.Encrypt(legacyPayload, []byte(directoryMonitoringAAD))
	if err != nil {
		t.Fatalf("encrypt legacy directories: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE directory_monitoring SET nonce = ?, ciphertext = ? WHERE id = 1", nonce, ciphertext); err != nil {
		t.Fatalf("store legacy directories: %v", err)
	}
	loaded, err = repository.LoadMonitoringTargets(ctx)
	wantLegacy := []domain.MonitoringTarget{{Path: `D:\Legacy`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}}
	if err != nil || !reflect.DeepEqual(loaded, wantLegacy) {
		t.Fatalf("LoadMonitoringTargets() legacy = %#v, %v", loaded, err)
	}
}

func TestMonitoringExclusionsPersistWithTargets(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x55}, 32))
	defer db.Close()
	targets := []domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}}
	exclusions := []domain.MonitoringExclusion{
		{Kind: domain.MonitoringExclusionKindExtension, Pattern: ".tmp"},
		{Kind: domain.MonitoringExclusionKindProcess, Pattern: "backup.exe"},
	}
	if err := repository.SaveMonitoringTargets(ctx, targets); err != nil {
		t.Fatalf("SaveMonitoringTargets() error = %v", err)
	}
	if err := repository.SaveMonitoringExclusions(ctx, exclusions); err != nil {
		t.Fatalf("SaveMonitoringExclusions() error = %v", err)
	}
	loadedTargets, err := repository.LoadMonitoringTargets(ctx)
	if err != nil || !reflect.DeepEqual(loadedTargets, targets) {
		t.Fatalf("LoadMonitoringTargets() = %#v, %v", loadedTargets, err)
	}
	loadedExclusions, err := repository.LoadMonitoringExclusions(ctx)
	if err != nil || !reflect.DeepEqual(loadedExclusions, exclusions) {
		t.Fatalf("LoadMonitoringExclusions() = %#v, %v", loadedExclusions, err)
	}
}

func TestInputActivityPreferenceDefaultsEnabledAndPersistsWithoutReplacingTargets(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x56}, 32))
	defer db.Close()
	targets := []domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}}
	if err := repository.SaveMonitoringTargets(ctx, targets); err != nil {
		t.Fatal(err)
	}
	if enabled, err := repository.LoadInputActivityEnabled(ctx); err != nil || !enabled {
		t.Fatalf("default input activity enabled = %t, error = %v", enabled, err)
	}
	if err := repository.SaveInputActivityEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if enabled, err := repository.LoadInputActivityEnabled(ctx); err != nil || enabled {
		t.Fatalf("stored input activity enabled = %t, error = %v", enabled, err)
	}
	if loaded, err := repository.LoadMonitoringTargets(ctx); err != nil || !reflect.DeepEqual(loaded, targets) {
		t.Fatalf("targets after preference update = %#v, error = %v", loaded, err)
	}
}

func TestWindowTitlePreferenceDefaultsDisabledAndPersists(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x57}, 32))
	defer db.Close()
	if enabled, err := repository.LoadWindowTitleEnabled(ctx); err != nil || enabled {
		t.Fatalf("default window title enabled = %t, error = %v", enabled, err)
	}
	if err := repository.SaveWindowTitleEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := repository.LoadWindowTitleEnabled(ctx); err != nil || !enabled {
		t.Fatalf("stored window title enabled = %t, error = %v", enabled, err)
	}
}

func TestHighRiskShortcutsPreferenceDefaultsDisabledAndPersists(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x58}, 32))
	defer db.Close()
	if enabled, err := repository.LoadHighRiskShortcutsEnabled(ctx); err != nil || enabled {
		t.Fatalf("default high-risk shortcuts enabled = %t, error = %v", enabled, err)
	}
	if err := repository.SaveHighRiskShortcutsEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if enabled, err := repository.LoadHighRiskShortcutsEnabled(ctx); err != nil || !enabled {
		t.Fatalf("stored high-risk shortcuts enabled = %t, error = %v", enabled, err)
	}
}

func TestMonitoringPolicyDefaultsAndPersistsWithExistingConfiguration(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x59}, 32))
	defer db.Close()
	targets := []domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}}
	if err := repository.SaveMonitoringTargets(ctx, targets); err != nil {
		t.Fatal(err)
	}
	policy, err := repository.LoadMonitoringPolicy(ctx)
	if err != nil || policy != domain.DefaultMonitoringPolicy() {
		t.Fatalf("default policy = %+v, error = %v", policy, err)
	}
	policy.ProcessAndSoftwareEnabled = false
	policy.StrictReadAuditEnabled = true
	policy.File.RecordDelete = false
	policy.SystemAndNetwork.MonitorProxy = false
	policy.UserSession.ReportIntervalSeconds = 45
	if err := repository.SaveMonitoringPolicy(ctx, policy); err != nil {
		t.Fatalf("SaveMonitoringPolicy() error = %v", err)
	}
	loaded, err := repository.LoadMonitoringPolicy(ctx)
	if err != nil || loaded != policy {
		t.Fatalf("LoadMonitoringPolicy() = %+v, error = %v", loaded, err)
	}
	if loadedTargets, err := repository.LoadMonitoringTargets(ctx); err != nil || !reflect.DeepEqual(loadedTargets, targets) {
		t.Fatalf("targets after policy update = %#v, error = %v", loadedTargets, err)
	}
}

func TestMonitoringProfilesPersistEachModeWithExistingTargets(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "profiles.db"), bytes.Repeat([]byte{0x6A}, 32))
	defer db.Close()
	targets := []domain.MonitoringTarget{{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true}}
	if err := repository.SaveMonitoringTargets(ctx, targets); err != nil {
		t.Fatal(err)
	}
	profiles, err := repository.LoadMonitoringProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	relaxed, _ := profiles.Policy(domain.MonitoringModeRelaxed)
	relaxed.SystemAndNetworkEnabled = true
	profiles, err = profiles.WithPolicy(domain.MonitoringModeRelaxed, relaxed)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveMonitoringProfiles(ctx, profiles); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.LoadMonitoringProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loadedRelaxed, _ := loaded.Policy(domain.MonitoringModeRelaxed)
	loadedStrict, _ := loaded.Policy(domain.MonitoringModeStrict)
	if !loadedRelaxed.SystemAndNetworkEnabled || !loadedStrict.StrictReadAuditEnabled {
		t.Fatalf("loaded profiles = %+v", loaded)
	}
	if loadedTargets, err := repository.LoadMonitoringTargets(ctx); err != nil || !reflect.DeepEqual(loadedTargets, targets) {
		t.Fatalf("targets after profile update = %#v, error = %v", loadedTargets, err)
	}
}

func TestMonitoringProfilesMigrateLegacySinglePolicyToCustom(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "legacy-profiles.db"), bytes.Repeat([]byte{0x6B}, 32))
	defer db.Close()
	legacy := domain.DefaultMonitoringPolicy()
	legacy.Mode = domain.MonitoringModeCustom
	legacy.ProcessAndSoftwareEnabled = false
	configuration := monitoringConfiguration{Policy: &legacy}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, err := repository.cipher.Encrypt(payload, []byte(directoryMonitoringAAD))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO directory_monitoring (id, nonce, ciphertext) VALUES (1, ?, ?)`, nonce, ciphertext); err != nil {
		t.Fatal(err)
	}
	profiles, err := repository.LoadMonitoringProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	custom, _ := profiles.Policy(domain.MonitoringModeCustom)
	relaxed, _ := profiles.Policy(domain.MonitoringModeRelaxed)
	if custom.ProcessAndSoftwareEnabled || relaxed.ProcessAndSoftwareEnabled {
		t.Fatalf("migrated profiles custom=%+v relaxed=%+v", custom, relaxed)
	}
}

func TestMonitoringPolicyLoadsLegacyBooleansWithDetailedDefaults(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x5B}, 32))
	defer db.Close()
	legacy := domain.MonitoringPolicy{FileActivityEnabled: true, ExternalDevicesEnabled: true}
	configuration := monitoringConfiguration{Policy: &legacy}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, err := repository.cipher.Encrypt(payload, []byte(directoryMonitoringAAD))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO directory_monitoring (id, nonce, ciphertext) VALUES (1, ?, ?)`, nonce, ciphertext); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.LoadMonitoringPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != domain.CurrentMonitoringPolicyVersion || !loaded.File.RecordCreate ||
		loaded.ProcessAndSoftwareEnabled || !loaded.ExternalDevices.RecordConnect {
		t.Fatalf("resolved legacy policy = %+v", loaded)
	}
}

func TestMonitoringPolicyRejectsStrictReadAuditWithoutFileActivity(t *testing.T) {
	ctx := context.Background()
	db, repository := openTestRepository(t, filepath.Join(t.TempDir(), "settings.db"), bytes.Repeat([]byte{0x5A}, 32))
	defer db.Close()
	policy := domain.DefaultMonitoringPolicy()
	policy.FileActivityEnabled = false
	policy.StrictReadAuditEnabled = true
	if err := repository.SaveMonitoringPolicy(ctx, policy); !errors.Is(err, domain.ErrStrictReadAuditRequiresFileActivity) {
		t.Fatalf("SaveMonitoringPolicy() error = %v, want %v", err, domain.ErrStrictReadAuditRequiresFileActivity)
	}
}

func TestDirectoryMonitoringMigrationPreservesVersion2Sessions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "version2.db")
	key := bytes.Repeat([]byte{0x53}, 32)
	db, repository := openTestRepository(t, path, key)
	session := createTestSession(t, repository)
	if _, err := db.ExecContext(ctx, "DROP TABLE directory_monitoring; DROP TABLE sensitive_key_cleanup; ALTER TABLE audit_events DROP COLUMN keys_encrypted; PRAGMA user_version = 2;"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, repository = openTestRepository(t, path, key)
	defer db.Close()
	got, err := repository.GetSession(ctx, session.ID)
	want := *session
	want.MonitoringPolicy = (domain.MonitoringPolicy{
		FileActivityEnabled:        true,
		ProcessAndSoftwareEnabled:  true,
		SystemAndNetworkEnabled:    true,
		ExternalDevicesEnabled:     true,
		UserSessionActivityEnabled: true,
	}).Resolved()
	if err != nil || got != want {
		t.Fatalf("session after migration = %+v, error = %v", got, err)
	}
	if _, err := repository.ListEvents(ctx, session.ID); err != nil {
		t.Fatalf("audit chain after migration: %v", err)
	}
	if err := repository.SaveMonitoredDirectories(ctx, []string{`C:\Documents`}); err != nil {
		t.Fatalf("save configuration after migration: %v", err)
	}
	assertPragmaInt(t, db, "user_version", currentSchemaVersion)
}
