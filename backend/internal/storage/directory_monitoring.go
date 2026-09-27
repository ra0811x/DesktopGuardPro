package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"desktopguardpro/internal/domain"
)

const directoryMonitoringAAD = "desktop-guard-pro:directory-monitoring:v1"

type monitoringConfiguration struct {
	Targets                  []domain.MonitoringTarget    `json:"targets"`
	Exclusions               []domain.MonitoringExclusion `json:"exclusions"`
	Policy                   *domain.MonitoringPolicy     `json:"policy,omitempty"`
	Profiles                 *domain.MonitoringProfiles   `json:"monitoringProfiles,omitempty"`
	InputActivityEnabled     *bool                        `json:"inputActivityEnabled,omitempty"`
	WindowTitleEnabled       *bool                        `json:"windowTitleEnabled,omitempty"`
	HighRiskShortcutsEnabled *bool                        `json:"highRiskShortcutsEnabled,omitempty"`
}

func (repository *Repository) LoadMonitoringPolicy(ctx context.Context) (domain.MonitoringPolicy, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return domain.MonitoringPolicy{}, err
	}
	if configuration.Policy == nil {
		if configuration.Profiles != nil {
			return configuration.Profiles.Policy(domain.MonitoringModeCustom)
		}
		return domain.DefaultMonitoringPolicy(), nil
	}
	policy := configuration.Policy.Resolved()
	if err := policy.Validate(); err != nil {
		return domain.MonitoringPolicy{}, fmt.Errorf("validate monitoring policy: %w", err)
	}
	return policy, nil
}

func (repository *Repository) SaveMonitoringPolicy(ctx context.Context, policy domain.MonitoringPolicy) error {
	policy = policy.Resolved()
	if err := policy.Validate(); err != nil {
		return err
	}
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.Policy = &policy
	profiles, err := monitoringProfilesFromConfiguration(configuration)
	if err != nil {
		return err
	}
	profiles, err = profiles.WithPolicy(domain.MonitoringModeCustom, policy)
	if err != nil {
		return err
	}
	configuration.Profiles = &profiles
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func (repository *Repository) LoadMonitoringProfiles(ctx context.Context) (domain.MonitoringProfiles, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return domain.MonitoringProfiles{}, err
	}
	return monitoringProfilesFromConfiguration(configuration)
}

func (repository *Repository) SaveMonitoringProfiles(ctx context.Context, profiles domain.MonitoringProfiles) error {
	profiles, err := profiles.Resolved()
	if err != nil {
		return err
	}
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.Profiles = &profiles
	custom, err := profiles.Policy(domain.MonitoringModeCustom)
	if err != nil {
		return err
	}
	configuration.Policy = &custom
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func monitoringProfilesFromConfiguration(configuration monitoringConfiguration) (domain.MonitoringProfiles, error) {
	if configuration.Profiles != nil {
		return configuration.Profiles.Resolved()
	}
	profiles, err := domain.DefaultMonitoringProfiles()
	if err != nil {
		return domain.MonitoringProfiles{}, err
	}
	if configuration.Policy == nil {
		return profiles, nil
	}
	return profiles.WithPolicy(domain.MonitoringModeCustom, configuration.Policy.Resolved())
}

func (repository *Repository) LoadHighRiskShortcutsEnabled(ctx context.Context) (bool, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return false, err
	}
	return configuration.HighRiskShortcutsEnabled != nil && *configuration.HighRiskShortcutsEnabled, nil
}

func (repository *Repository) SaveHighRiskShortcutsEnabled(ctx context.Context, enabled bool) error {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.HighRiskShortcutsEnabled = &enabled
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func (repository *Repository) LoadInputActivityEnabled(ctx context.Context) (bool, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return false, err
	}
	if configuration.InputActivityEnabled == nil {
		return true, nil
	}
	return *configuration.InputActivityEnabled, nil
}

func (repository *Repository) SaveInputActivityEnabled(ctx context.Context, enabled bool) error {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.InputActivityEnabled = &enabled
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func (repository *Repository) LoadWindowTitleEnabled(ctx context.Context) (bool, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return false, err
	}
	return configuration.WindowTitleEnabled != nil && *configuration.WindowTitleEnabled, nil
}

func (repository *Repository) SaveWindowTitleEnabled(ctx context.Context, enabled bool) error {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.WindowTitleEnabled = &enabled
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func (repository *Repository) LoadMonitoredDirectories(ctx context.Context) ([]string, error) {
	targets, err := repository.LoadMonitoringTargets(ctx)
	if err != nil {
		return nil, err
	}
	directories := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.Kind == domain.MonitoringTargetKindDirectory {
			directories = append(directories, target.Path)
		}
	}
	return directories, nil
}

func (repository *Repository) LoadMonitoringTargets(ctx context.Context) ([]domain.MonitoringTarget, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	return domain.CloneMonitoringTargets(configuration.Targets), nil
}

func (repository *Repository) LoadMonitoringExclusions(ctx context.Context) ([]domain.MonitoringExclusion, error) {
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	return append([]domain.MonitoringExclusion(nil), configuration.Exclusions...), nil
}

func (repository *Repository) loadMonitoringConfiguration(ctx context.Context) (monitoringConfiguration, error) {
	var nonce, ciphertext []byte
	err := repository.db.QueryRowContext(ctx, "SELECT nonce, ciphertext FROM directory_monitoring WHERE id = 1").Scan(&nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return monitoringConfiguration{}, nil
	}
	if err != nil {
		return monitoringConfiguration{}, fmt.Errorf("load monitoring targets: %w", err)
	}
	plaintext, err := repository.cipher.Decrypt(nonce, ciphertext, []byte(directoryMonitoringAAD))
	if err != nil {
		return monitoringConfiguration{}, fmt.Errorf("decrypt monitoring targets: %w", err)
	}
	defer clear(plaintext)
	var configuration monitoringConfiguration
	if err := json.Unmarshal(plaintext, &configuration); err == nil {
		if err := domain.ValidateMonitoringTargets(configuration.Targets); err != nil {
			return monitoringConfiguration{}, fmt.Errorf("validate monitoring targets: %w", err)
		}
		if err := domain.ValidateMonitoringExclusions(configuration.Exclusions); err != nil {
			return monitoringConfiguration{}, fmt.Errorf("validate monitoring exclusions: %w", err)
		}
		return configuration, nil
	}
	targets := []domain.MonitoringTarget{}
	if err := json.Unmarshal(plaintext, &targets); err == nil {
		if err := domain.ValidateMonitoringTargets(targets); err != nil {
			return monitoringConfiguration{}, fmt.Errorf("validate monitoring targets: %w", err)
		}
		return monitoringConfiguration{Targets: targets}, nil
	}
	var directories []string
	if err := json.Unmarshal(plaintext, &directories); err != nil {
		return monitoringConfiguration{}, fmt.Errorf("decode monitoring targets: %w", err)
	}
	targets = make([]domain.MonitoringTarget, 0, len(directories))
	for _, directory := range directories {
		targets = append(targets, domain.MonitoringTarget{
			Path: directory, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
		})
	}
	if err := domain.ValidateMonitoringTargets(targets); err != nil {
		return monitoringConfiguration{}, fmt.Errorf("validate legacy monitored directories: %w", err)
	}
	return monitoringConfiguration{Targets: targets}, nil
}

func (repository *Repository) SaveMonitoredDirectories(ctx context.Context, directories []string) error {
	targets := make([]domain.MonitoringTarget, 0, len(directories))
	for _, directory := range directories {
		targets = append(targets, domain.MonitoringTarget{
			Path: directory, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
		})
	}
	return repository.SaveMonitoringTargets(ctx, targets)
}

func (repository *Repository) SaveMonitoringTargets(ctx context.Context, targets []domain.MonitoringTarget) error {
	if targets == nil {
		targets = []domain.MonitoringTarget{}
	}
	if err := domain.ValidateMonitoringTargets(targets); err != nil {
		return err
	}
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.Targets = domain.CloneMonitoringTargets(targets)
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func (repository *Repository) SaveMonitoringExclusions(ctx context.Context, exclusions []domain.MonitoringExclusion) error {
	if exclusions == nil {
		exclusions = []domain.MonitoringExclusion{}
	}
	if err := domain.ValidateMonitoringExclusions(exclusions); err != nil {
		return err
	}
	configuration, err := repository.loadMonitoringConfiguration(ctx)
	if err != nil {
		return err
	}
	configuration.Exclusions = append([]domain.MonitoringExclusion(nil), exclusions...)
	return repository.saveMonitoringConfiguration(ctx, configuration)
}

func (repository *Repository) saveMonitoringConfiguration(ctx context.Context, configuration monitoringConfiguration) error {
	plaintext, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	defer clear(plaintext)
	nonce, ciphertext, err := repository.cipher.Encrypt(plaintext, []byte(directoryMonitoringAAD))
	if err != nil {
		return err
	}
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO directory_monitoring (id, nonce, ciphertext) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET nonce = excluded.nonce, ciphertext = excluded.ciphertext
	`, nonce, ciphertext)
	if err != nil {
		return fmt.Errorf("save monitoring targets: %w", err)
	}
	return nil
}
