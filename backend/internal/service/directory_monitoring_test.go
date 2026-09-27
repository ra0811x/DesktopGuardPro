package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
)

func TestAPIDirectoryMonitoringPersistsScopeAndRecordsChangesDuringProtection(t *testing.T) {
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) {
		if len(roots) == 1 && roots[0] == "invalid" {
			return nil, errors.New("invalid directory")
		}
		return roots, nil
	})
	client := ClientIdentity{UserSID: "owner"}
	request := func(kind contracts.MessageType, roots []string, client ClientIdentity) contracts.Message {
		t.Helper()
		message := newTestMessage(t, kind, time.Now().UTC(), DirectoryMonitoringResult{Directories: roots})
		response, err := api.HandleForClient(message, client)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	want := []string{`C:\Users\Raymond\Documents`}
	response := request(contracts.MessageTypeDirectoryMonitoringUpdate, want, client)
	if response.Type != contracts.MessageTypeDirectoryMonitoringResult || !reflect.DeepEqual(store.directories, want) {
		t.Fatalf("configure response = %+v, stored directories = %v", response, store.directories)
	}
	var loaded DirectoryMonitoringResult
	decodeTestPayload(t, request(contracts.MessageTypeDirectoryMonitoringGet, nil, client), &loaded)
	if !reflect.DeepEqual(loaded.Directories, want) {
		t.Fatalf("loaded directories = %v", loaded.Directories)
	}
	assertAPIError(t, request(contracts.MessageTypeDirectoryMonitoringUpdate, []string{"invalid"}, client), ErrorCodeInvalidPayload)
	assertAPIError(t, request(contracts.MessageTypeDirectoryMonitoringUpdate, nil, ClientIdentity{UserSID: "other"}), ErrorCodeUnauthorized)
	if _, err := api.coordinator.Create("session-1", "保护测试"); err != nil {
		t.Fatal(err)
	}
	updated := []string{`C:\Users\Raymond\Desktop`}
	response = request(contracts.MessageTypeDirectoryMonitoringUpdate, updated, client)
	if response.Type != contracts.MessageTypeDirectoryMonitoringResult || !reflect.DeepEqual(store.directories, updated) {
		t.Fatalf("protected update response = %+v, stored directories = %v", response, store.directories)
	}
	if len(store.events) != 1 || store.events[0].SessionID != "session-1" || store.events[0].Action != "monitoring_rules_changed" {
		t.Fatalf("monitoring rule events = %#v", store.events)
	}
}

func TestAPIDirectoryMonitoringPersistsTargetsWithIndependentRecursion(t *testing.T) {
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	targets := []domain.MonitoringTarget{
		{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: false},
		{Path: `D:\Archive`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
	}
	request := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{Targets: targets})
	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatalf("Handle(update targets) error = %v", err)
	}
	if response.Type != contracts.MessageTypeDirectoryMonitoringResult || !reflect.DeepEqual(store.targets, targets) {
		t.Fatalf("update targets response = %+v, stored = %#v", response, store.targets)
	}
	get := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringGet, time.Now().UTC(), struct{}{})
	response, err = api.HandleForClient(get, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatalf("Handle(get targets) error = %v", err)
	}
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if !reflect.DeepEqual(result.Targets, targets) {
		t.Fatalf("loaded targets = %#v", result.Targets)
	}
}

func TestAPIDirectoryMonitoringAllowsRemovingAllTargets(t *testing.T) {
	store := &directoryMonitoringTestStore{targets: []domain.MonitoringTarget{{
		Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
	}}}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	request := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), struct {
		Targets []domain.MonitoringTarget `json:"targets"`
	}{Targets: []domain.MonitoringTarget{}})
	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatalf("Handle(remove targets) error = %v", err)
	}
	if response.Type != contracts.MessageTypeDirectoryMonitoringResult || len(store.targets) != 0 {
		t.Fatalf("remove targets response = %+v, stored = %#v", response, store.targets)
	}
}

func TestAPIDirectoryMonitoringUsesTargetValidatorForEveryTargetKind(t *testing.T) {
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	validated := false
	api.SetMonitoringTargetValidator(func(targets []domain.MonitoringTarget) ([]domain.MonitoringTarget, error) {
		validated = true
		if len(targets) != 2 || targets[1].Kind != domain.MonitoringTargetKindFile {
			return nil, errors.New("invalid monitoring target")
		}
		return targets, nil
	})
	targets := []domain.MonitoringTarget{
		{Path: `C:\Evidence`, Kind: domain.MonitoringTargetKindDirectory, Recursive: true},
		{Path: `C:\Evidence\summary.txt`, Kind: domain.MonitoringTargetKindFile},
	}
	request := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{Targets: targets})
	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatalf("Handle(update targets) error = %v", err)
	}
	if !validated || response.Type != contracts.MessageTypeDirectoryMonitoringResult || !reflect.DeepEqual(store.targets, targets) {
		t.Fatalf("validated=%t response=%+v stored=%#v", validated, response, store.targets)
	}
}

func TestAPIDirectoryMonitoringPersistsExclusions(t *testing.T) {
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	exclusions := []domain.MonitoringExclusion{
		{Kind: domain.MonitoringExclusionKindExtension, Pattern: ".tmp"},
		{Kind: domain.MonitoringExclusionKindProcess, Pattern: "backup.exe"},
	}
	update := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{Exclusions: exclusions})
	response, err := api.HandleForClient(update, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatalf("Handle(update exclusions) error = %v", err)
	}
	if response.Type != contracts.MessageTypeDirectoryMonitoringResult || !reflect.DeepEqual(store.exclusions, exclusions) {
		t.Fatalf("update exclusions response = %+v, stored = %#v", response, store.exclusions)
	}
	get := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringGet, time.Now().UTC(), struct{}{})
	response, err = api.HandleForClient(get, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatalf("Handle(get exclusions) error = %v", err)
	}
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if !reflect.DeepEqual(result.Exclusions, exclusions) {
		t.Fatalf("loaded exclusions = %#v", result.Exclusions)
	}
}

func TestAPIDirectoryMonitoringPersistsInputActivityPreference(t *testing.T) {
	enabled := false
	store := &directoryMonitoringTestStore{inputActivityEnabled: true}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	update := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{
		InputActivityEnabled: &enabled,
	})
	response, err := api.HandleForClient(update, ClientIdentity{UserSID: "owner"})
	if err != nil || response.Type != contracts.MessageTypeDirectoryMonitoringResult || store.inputActivityEnabled {
		t.Fatalf("update preference response=%+v stored=%t error=%v", response, store.inputActivityEnabled, err)
	}
	if store.policy.UserSession.RecordKeyboardActivity || store.policy.UserSession.RecordMouseClicks || store.policy.UserSession.RecordMouseWheel {
		t.Fatalf("input activity policy was not synchronized: %+v", store.policy.UserSession)
	}
	get := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringGet, time.Now().UTC(), struct{}{})
	response, err = api.HandleForClient(get, ClientIdentity{UserSID: "owner"})
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if err != nil || result.InputActivityEnabled == nil || *result.InputActivityEnabled {
		t.Fatalf("loaded preference=%v error=%v", result.InputActivityEnabled, err)
	}
}

func TestAPIDirectoryMonitoringPersistsWindowTitlePreference(t *testing.T) {
	enabled := true
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	update := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{
		WindowTitleEnabled: &enabled,
	})
	response, err := api.HandleForClient(update, ClientIdentity{UserSID: "owner"})
	if err != nil || response.Type != contracts.MessageTypeDirectoryMonitoringResult || !store.windowTitleEnabled {
		t.Fatalf("update title preference response=%+v stored=%t error=%v", response, store.windowTitleEnabled, err)
	}
	if !store.policy.UserSession.RecordWindowTitle {
		t.Fatalf("window title policy was not synchronized: %+v", store.policy.UserSession)
	}
	get := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringGet, time.Now().UTC(), struct{}{})
	response, err = api.HandleForClient(get, ClientIdentity{UserSID: "owner"})
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if err != nil || result.WindowTitleEnabled == nil || !*result.WindowTitleEnabled {
		t.Fatalf("loaded title preference=%v error=%v", result.WindowTitleEnabled, err)
	}
}

func TestAPIDirectoryMonitoringPersistsHighRiskShortcutsPreference(t *testing.T) {
	enabled := true
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	update := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{
		HighRiskShortcutsEnabled: &enabled,
	})
	response, err := api.HandleForClient(update, ClientIdentity{UserSID: "owner"})
	if err != nil || response.Type != contracts.MessageTypeDirectoryMonitoringResult || !store.highRiskShortcutsEnabled {
		t.Fatalf("update shortcut preference response=%+v stored=%t error=%v", response, store.highRiskShortcutsEnabled, err)
	}
	if !store.policy.UserSession.RecordHighRiskShortcuts {
		t.Fatalf("high risk shortcut policy was not synchronized: %+v", store.policy.UserSession)
	}
	get := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringGet, time.Now().UTC(), struct{}{})
	response, err = api.HandleForClient(get, ClientIdentity{UserSID: "owner"})
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if err != nil || result.HighRiskShortcutsEnabled == nil || !*result.HighRiskShortcutsEnabled {
		t.Fatalf("loaded shortcut preference=%v error=%v", result.HighRiskShortcutsEnabled, err)
	}
}

func TestAPIDirectoryMonitoringPersistsPolicyForFutureSessions(t *testing.T) {
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	policy := domain.DefaultMonitoringPolicy()
	policy.ExternalDevicesEnabled = false
	policy.UserSession.RecordKeyboardActivity = false
	policy.UserSession.RecordMouseClicks = false
	policy.UserSession.RecordMouseWheel = false
	policy.UserSession.RecordWindowTitle = true
	policy.UserSession.RecordHighRiskShortcuts = true
	update := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{
		MonitoringPolicy: &policy,
	})
	response, err := api.HandleForClient(update, ClientIdentity{UserSID: "owner"})
	if err != nil || response.Type != contracts.MessageTypeDirectoryMonitoringResult || store.policy != policy {
		t.Fatalf("update policy response=%+v stored=%+v error=%v", response, store.policy, err)
	}
	if store.inputActivityEnabled || !store.windowTitleEnabled || !store.highRiskShortcutsEnabled {
		t.Fatalf("legacy preferences were not synchronized: input=%t title=%t shortcuts=%t",
			store.inputActivityEnabled, store.windowTitleEnabled, store.highRiskShortcutsEnabled)
	}
	get := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringGet, time.Now().UTC(), struct{}{})
	response, err = api.HandleForClient(get, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if result.MonitoringPolicy == nil || *result.MonitoringPolicy != policy {
		t.Fatalf("loaded policy = %+v", result.MonitoringPolicy)
	}
}

func TestAPIDirectoryMonitoringRemovesInputShieldFromModePolicy(t *testing.T) {
	now := time.Now().UTC()
	store := &directoryMonitoringTestStore{}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	policy := domain.DefaultMonitoringPolicy()
	policy.InputShieldEnabled = true
	policy.InputShield.CredentialMode = domain.InputShieldCredentialLocal
	response, err := api.HandleForClient(newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, now,
		DirectoryMonitoringResult{MonitoringPolicy: &policy}), ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Type != contracts.MessageTypeDirectoryMonitoringResult || store.policy.InputShieldEnabled ||
		store.policy.InputShield.CredentialMode != domain.InputShieldCredentialLocal {
		t.Fatalf("mode input shield normalization response=%+v stored=%+v", response, store.policy)
	}
}

func TestAPICreatesNewSessionUsingSavedMonitoringPolicy(t *testing.T) {
	policy := domain.DefaultMonitoringPolicy()
	policy.ExternalDevicesEnabled = false
	store := &directoryMonitoringTestStore{policy: policy}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	request := newTestMessage(t, contracts.MessageTypeSessionCreate, time.Now().UTC(), CreateSessionRequest{
		ID: "policy-session", Name: "策略生效", MonitoringLevel: domain.MonitoringLevelStrict,
	})
	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	var result SessionResult
	decodeTestPayload(t, response, &result)
	if result.Session == nil || result.Session.MonitoringPolicy != policy ||
		result.Session.MonitoringLevel != domain.MonitoringLevelStandard || len(store.created) != 1 ||
		store.created[0].MonitoringPolicy != policy {
		t.Fatalf("created result=%+v stored=%+v", result, store.created)
	}
}

func TestAPIDirectoryMonitoringPersistsAndResetsSelectedModeProfile(t *testing.T) {
	profiles, err := domain.DefaultMonitoringProfiles()
	if err != nil {
		t.Fatal(err)
	}
	store := &directoryMonitoringTestStore{profiles: profiles}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })

	relaxed := profiles.Relaxed
	relaxed.SystemAndNetworkEnabled = true
	update := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{
		MonitoringPolicy: &relaxed,
	})
	response, err := api.HandleForClient(update, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	var result DirectoryMonitoringResult
	decodeTestPayload(t, response, &result)
	if result.MonitoringProfiles == nil || store.profiles.Relaxed != relaxed {
		t.Fatalf("updated profiles=%+v stored relaxed=%+v", result.MonitoringProfiles, store.profiles.Relaxed)
	}

	mode := domain.MonitoringModeRelaxed
	reset := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), DirectoryMonitoringResult{
		ResetMonitoringMode: &mode,
	})
	response, err = api.HandleForClient(reset, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	decodeTestPayload(t, response, &result)
	want, err := domain.BuiltInMonitoringPolicy(domain.MonitoringModeRelaxed)
	if err != nil {
		t.Fatal(err)
	}
	if result.MonitoringProfiles == nil || store.profiles.Relaxed != want {
		t.Fatalf("reset profiles=%+v stored relaxed=%+v want=%+v", result.MonitoringProfiles, store.profiles.Relaxed, want)
	}
}

func TestAPICreatesSessionUsingAdjustedBuiltInModeProfile(t *testing.T) {
	profiles, err := domain.DefaultMonitoringProfiles()
	if err != nil {
		t.Fatal(err)
	}
	profiles.Relaxed.SystemAndNetworkEnabled = true
	store := &directoryMonitoringTestStore{profiles: profiles}
	api := NewPersistentAuthorizedAPI(NewCoordinator(), store, "owner")
	api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
	request := newTestMessage(t, contracts.MessageTypeSessionCreate, time.Now().UTC(), CreateSessionRequest{
		ID: "adjusted-relaxed", Name: "已调整宽松模式", MonitoringMode: domain.MonitoringModeRelaxed,
	})
	response, err := api.HandleForClient(request, ClientIdentity{UserSID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	var result SessionResult
	decodeTestPayload(t, response, &result)
	if result.Session == nil || !result.Session.MonitoringPolicy.SystemAndNetworkEnabled ||
		result.Session.MonitoringPolicy.Mode != domain.MonitoringModeRelaxed {
		t.Fatalf("created session policy=%+v", result.Session)
	}
}

type directoryMonitoringTestStore struct {
	directories              []string
	targets                  []domain.MonitoringTarget
	exclusions               []domain.MonitoringExclusion
	events                   []domain.AuditEvent
	inputActivityEnabled     bool
	windowTitleEnabled       bool
	highRiskShortcutsEnabled bool
	policy                   domain.MonitoringPolicy
	profiles                 domain.MonitoringProfiles
	created                  []domain.Session
}

func (store *directoryMonitoringTestStore) LoadMonitoringPolicy(context.Context) (domain.MonitoringPolicy, error) {
	if store.policy == (domain.MonitoringPolicy{}) {
		return domain.DefaultMonitoringPolicy(), nil
	}
	return store.policy, nil
}

func (store *directoryMonitoringTestStore) SaveMonitoringPolicy(_ context.Context, policy domain.MonitoringPolicy) error {
	store.policy = policy
	return nil
}

func (store *directoryMonitoringTestStore) LoadMonitoringProfiles(context.Context) (domain.MonitoringProfiles, error) {
	if store.profiles == (domain.MonitoringProfiles{}) {
		return domain.DefaultMonitoringProfiles()
	}
	return store.profiles.Resolved()
}

func (store *directoryMonitoringTestStore) SaveMonitoringProfiles(_ context.Context, profiles domain.MonitoringProfiles) error {
	resolved, err := profiles.Resolved()
	if err != nil {
		return err
	}
	store.profiles = resolved
	store.policy = resolved.Standard
	return nil
}

func (store *directoryMonitoringTestStore) CreateSession(_ context.Context, session domain.Session, _ time.Time) error {
	store.created = append(store.created, session)
	return nil
}
func (*directoryMonitoringTestStore) UpdateSession(context.Context, domain.Session, time.Time) error {
	return nil
}
func (store *directoryMonitoringTestStore) LoadMonitoredDirectories(context.Context) ([]string, error) {
	return append([]string{}, store.directories...), nil
}
func (store *directoryMonitoringTestStore) SaveMonitoredDirectories(_ context.Context, roots []string) error {
	store.directories = append([]string{}, roots...)
	return nil
}

func (store *directoryMonitoringTestStore) LoadMonitoringTargets(context.Context) ([]domain.MonitoringTarget, error) {
	return domain.CloneMonitoringTargets(store.targets), nil
}

func (store *directoryMonitoringTestStore) SaveMonitoringTargets(_ context.Context, targets []domain.MonitoringTarget) error {
	store.targets = domain.CloneMonitoringTargets(targets)
	return nil
}

func (store *directoryMonitoringTestStore) LoadMonitoringExclusions(context.Context) ([]domain.MonitoringExclusion, error) {
	return append([]domain.MonitoringExclusion(nil), store.exclusions...), nil
}

func (store *directoryMonitoringTestStore) SaveMonitoringExclusions(_ context.Context, exclusions []domain.MonitoringExclusion) error {
	store.exclusions = append([]domain.MonitoringExclusion(nil), exclusions...)
	return nil
}

func (store *directoryMonitoringTestStore) LoadInputActivityEnabled(context.Context) (bool, error) {
	return store.inputActivityEnabled, nil
}

func (store *directoryMonitoringTestStore) SaveInputActivityEnabled(_ context.Context, enabled bool) error {
	store.inputActivityEnabled = enabled
	return nil
}

func (store *directoryMonitoringTestStore) LoadWindowTitleEnabled(context.Context) (bool, error) {
	return store.windowTitleEnabled, nil
}

func (store *directoryMonitoringTestStore) SaveWindowTitleEnabled(_ context.Context, enabled bool) error {
	store.windowTitleEnabled = enabled
	return nil
}

func (store *directoryMonitoringTestStore) LoadHighRiskShortcutsEnabled(context.Context) (bool, error) {
	return store.highRiskShortcutsEnabled, nil
}

func (store *directoryMonitoringTestStore) SaveHighRiskShortcutsEnabled(_ context.Context, enabled bool) error {
	store.highRiskShortcutsEnabled = enabled
	return nil
}

func (store *directoryMonitoringTestStore) AppendEventAutoSequence(_ context.Context, event domain.AuditEvent, _ []byte) (domain.AuditEvent, error) {
	event.Sequence = uint64(len(store.events) + 1)
	store.events = append(store.events, event)
	return event, nil
}

func TestScopeChangesRejectedUntilProtectionEnds(t *testing.T) {
	for _, state := range []domain.SessionState{domain.SessionStatePreparing, domain.SessionStateActive, domain.SessionStateDegraded, domain.SessionStatePaused, domain.SessionStateFinalizing} {
		t.Run(string(state), func(t *testing.T) {
			store := &directoryMonitoringTestStore{}
			coordinator := NewCoordinator()
			coordinator.current = &domain.Session{ID: "scope-test", State: state}
			api := NewPersistentAuthorizedAPI(coordinator, store, "owner")
			api.SetDirectoryMonitoringValidator(func(roots []string) ([]string, error) { return roots, nil })
			for _, payload := range []any{
				map[string]any{"directories": []string{`C:\Evidence`}},
				map[string]any{"targets": []domain.MonitoringTarget{}},
				map[string]any{"exclusions": []domain.MonitoringExclusion{}},
				map[string]any{"exclusions": []map[string]string{{"kind": "path", "pattern": `C:\Evidence`}}},
			} {
				request := newTestMessage(t, contracts.MessageTypeDirectoryMonitoringUpdate, time.Now().UTC(), payload)
				response, err := api.HandleForClient(request, ClientIdentity{UserSID: "owner"})
				if err != nil {
					t.Fatal(err)
				}
				assertAPIError(t, response, ErrorCodeInvalidRequest)
				if len(store.directories) != 0 || len(store.targets) != 0 || len(store.exclusions) != 0 || len(store.events) != 0 {
					t.Fatal("rejected scope mutated storage")
				}
			}
		})
	}
}
