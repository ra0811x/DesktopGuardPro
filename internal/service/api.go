package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"desktopguardpro/internal/contracts"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/storage"
)

const (
	HealthStatusRunning  = "running"
	HealthStatusDegraded = "degraded"

	ErrorCodeInvalidPayload         = "invalid_payload"
	ErrorCodeInvalidRequest         = "invalid_request"
	ErrorCodeNoCurrentSession       = "no_current_session"
	ErrorCodeRequestExpired         = "request_expired"
	ErrorCodeSessionInProgress      = "session_in_progress"
	ErrorCodeUnsupportedMessage     = "unsupported_message_type"
	ErrorCodeInvalidTransition      = "invalid_transition"
	ErrorCodeStorageFailure         = "storage_failure"
	ErrorCodeAnalysisUnavailable    = "analysis_unavailable"
	ErrorCodeReportTooLarge         = "report_too_large"
	ErrorCodeReportTransferInvalid  = "report_transfer_invalid"
	ErrorCodeUnauthorized           = "unauthorized"
	ErrorCodeVerificationRequired   = "end_verification_required"
	ErrorCodeVerificationFailed     = "end_verification_failed"
	ErrorCodeVerificationLocked     = "end_verification_locked"
	ErrorCodeAgentUnavailable       = "agent_activity_unavailable"
	ErrorCodeRetentionUnavailable   = "session_retention_unavailable"
	ErrorCodeInputShieldUnavailable = "input_shield_unavailable"
)

const (
	endVerificationLifetime        = time.Minute
	maximumEndVerificationFailures = 5
	endVerificationLockout         = 5 * time.Minute
)

var (
	ErrSessionPersistence                      = errors.New("session persistence failed")
	ErrSystemCredentialVerificationUnavailable = errors.New("system credential verification is unavailable")
)

type HealthResult struct {
	MaintenanceGate bool            `json:"maintenanceGate"`
	Status          string          `json:"status"`
	Session         *domain.Session `json:"session,omitempty"`
}

type CreateSessionRequest struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	MonitoringMode  domain.MonitoringMode  `json:"monitoringMode,omitempty"`
	MonitoringLevel domain.MonitoringLevel `json:"monitoringLevel,omitempty"`
}

type TransitionSessionRequest struct {
	State        domain.SessionState        `json:"state"`
	Verification *EndProtectionVerification `json:"verification,omitempty"`
}

type EndProtectionVerification struct {
	Token    string `json:"token"`
	UserName string `json:"userName"`
	Domain   string `json:"domain,omitempty"`
	Password []byte `json:"password"`
}

type EndVerificationChallenge struct {
	Token      string    `json:"token"`
	ExpiresUTC time.Time `json:"expiresUtc"`
}

type SystemCredentials struct {
	UserName string
	Domain   string
	Password []byte
}

type SessionResult struct {
	Session *domain.Session `json:"session,omitempty"`
}

type SessionStartPreviewRequest struct {
	MonitoringMode  domain.MonitoringMode  `json:"monitoringMode,omitempty"`
	MonitoringLevel domain.MonitoringLevel `json:"monitoringLevel"`
}

type SessionStartPreviewResult struct {
	MonitoringMode       domain.MonitoringMode     `json:"monitoringMode"`
	MonitoringLevel      domain.MonitoringLevel    `json:"monitoringLevel"`
	MonitoringPolicy     domain.MonitoringPolicy   `json:"monitoringPolicy"`
	MonitoredTargetCount int                       `json:"monitoredTargetCount"`
	Targets              []domain.MonitoringTarget `json:"targets"`
	Impact               MonitoringImpactResult    `json:"impact"`
}

type SessionBaselineReviewGetRequest struct {
	SessionID string `json:"sessionId"`
}

type SessionBaselineReviewResolveRequest struct {
	SessionID  string                           `json:"sessionId"`
	Resolution storage.BaselineReviewResolution `json:"resolution"`
}

type SessionBaselineReviewResult struct {
	SessionID  string                           `json:"sessionId"`
	Decision   domain.BaselineStartDecision     `json:"decision"`
	Resolution storage.BaselineReviewResolution `json:"resolution,omitempty"`
	Session    *domain.Session                  `json:"session,omitempty"`
}

type MonitoringImpactResult struct {
	RequiresAdministrator bool                         `json:"requiresAdministrator"`
	ExpectedEventVolume   domain.MonitoringImpactLevel `json:"expectedEventVolume"`
	PerformanceImpact     domain.MonitoringImpactLevel `json:"performanceImpact"`
	StorageImpact         domain.MonitoringImpactLevel `json:"storageImpact"`
}

type ErrorResult struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type AgentActivityReportRequest struct {
	SessionID             string            `json:"sessionId"`
	WindowTitle           string            `json:"windowTitle,omitempty"`
	ProcessID             uint32            `json:"processId,omitempty"`
	ProcessImage          string            `json:"processImage,omitempty"`
	InputActivityCount    uint64            `json:"inputActivityCount,omitempty"`
	KeyboardActivityCount uint64            `json:"keyboardActivityCount,omitempty"`
	MouseClickCount       uint64            `json:"mouseClickCount,omitempty"`
	MouseWheelCount       uint64            `json:"mouseWheelCount,omitempty"`
	HighRiskShortcutCount map[string]uint64 `json:"highRiskShortcutCount,omitempty"`
	ActivityStartUTC      time.Time         `json:"activityStartUtc,omitempty"`
	ActivityEndUTC        time.Time         `json:"activityEndUtc,omitempty"`
	ForegroundStartUTC    time.Time         `json:"foregroundStartUtc,omitempty"`
	ForegroundEndUTC      time.Time         `json:"foregroundEndUtc,omitempty"`
	ForegroundDurationMS  int64             `json:"foregroundDurationMillis,omitempty"`
	ObservedUTC           time.Time         `json:"observedUtc"`
}

type AgentActivityResult struct {
	SessionID string `json:"sessionId"`
}

type InputShieldCredentialUpdateRequest struct {
	Password       []byte `json:"password"`
	EnableRecovery bool   `json:"enableRecovery"`
}

type InputShieldCredentialVerifyRequest struct {
	UserName string `json:"userName,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Password []byte `json:"password"`
	Recovery bool   `json:"recovery"`
}

type InputShieldCredentialResult struct {
	Configured          bool   `json:"configured"`
	RecoveryCodeEnabled bool   `json:"recoveryCodeEnabled"`
	RecoveryCode        string `json:"recoveryCode,omitempty"`
	Verified            bool   `json:"verified,omitempty"`
}

type AgentInputShieldDevice struct {
	Kind          string    `json:"kind"`
	InterfacePath string    `json:"interfacePath,omitempty"`
	InstanceID    string    `json:"instanceId,omitempty"`
	VendorID      string    `json:"vendorId,omitempty"`
	ProductID     string    `json:"productId,omitempty"`
	Active        bool      `json:"active"`
	LastActiveUTC time.Time `json:"lastActiveUtc,omitempty"`
}

type AgentInputShieldReportRequest struct {
	SessionID      string                   `json:"sessionId"`
	Action         string                   `json:"action"`
	State          string                   `json:"state"`
	HookRunning    bool                     `json:"hookRunning"`
	InputKind      string                   `json:"inputKind,omitempty"`
	KeyCode        int                      `json:"keyCode,omitempty"`
	PointerX       *int32                   `json:"pointerX,omitempty"`
	PointerY       *int32                   `json:"pointerY,omitempty"`
	Injected       bool                     `json:"injected,omitempty"`
	LowerIntegrity bool                     `json:"lowerIntegrity,omitempty"`
	DroppedEvents  uint64                   `json:"droppedEvents,omitempty"`
	Device         *AgentInputShieldDevice  `json:"device,omitempty"`
	Devices        []AgentInputShieldDevice `json:"devices,omitempty"`
	ObservedUTC    time.Time                `json:"observedUtc"`
}

type AgentInputShieldResult struct {
	SessionID string `json:"sessionId"`
}

type InputShieldStatusResult struct {
	SessionID     string                   `json:"sessionId,omitempty"`
	State         string                   `json:"state"`
	HookRunning   bool                     `json:"hookRunning"`
	DroppedEvents uint64                   `json:"droppedEvents"`
	ObservedUTC   time.Time                `json:"observedUtc,omitempty"`
	Devices       []AgentInputShieldDevice `json:"devices,omitempty"`
}

type InputControlStartRequest struct {
	Policy          domain.InputShieldPolicy `json:"policy"`
	DurationMinutes int                      `json:"durationMinutes"`
	Indefinite      bool                     `json:"indefinite"`
}

type InputControlResult struct {
	ControlID     string                   `json:"controlId,omitempty"`
	Source        string                   `json:"source,omitempty"`
	Enabled       bool                     `json:"enabled"`
	State         string                   `json:"state"`
	StartedUTC    time.Time                `json:"startedUtc,omitempty"`
	ExpiresUTC    time.Time                `json:"expiresUtc,omitempty"`
	Indefinite    bool                     `json:"indefinite"`
	Policy        domain.InputShieldPolicy `json:"policy"`
	HookRunning   bool                     `json:"hookRunning"`
	DroppedEvents uint64                   `json:"droppedEvents"`
	ObservedUTC   time.Time                `json:"observedUtc,omitempty"`
	Devices       []AgentInputShieldDevice `json:"devices,omitempty"`
}

type AgentActivityHandler func(context.Context, ClientIdentity, AgentActivityReportRequest) error
type AgentInputShieldHandler func(context.Context, ClientIdentity, AgentInputShieldReportRequest) error

type SessionRetentionLockRequest struct {
	SessionID string `json:"sessionId"`
	Locked    bool   `json:"locked"`
}

type SessionRetentionLockResult struct {
	SessionID string `json:"sessionId"`
	Locked    bool   `json:"locked"`
}

type SessionRetentionPruneRequest struct {
	BeforeUTC time.Time `json:"beforeUtc"`
	Limit     int       `json:"limit,omitempty"`
}

type SessionRetentionPruneResult struct {
	DeletedSessionIDs []string `json:"deletedSessionIds"`
}

type ClientIdentity struct {
	ProcessID        uint32
	WindowsSessionID uint32
	UserSID          string
	ImagePath        string
}

func (identity ClientIdentity) Principal() string {
	userSID := strings.TrimSpace(identity.UserSID)
	if userSID == "" {
		return ""
	}
	return fmt.Sprintf("%s\x00%d", userSID, identity.WindowsSessionID)
}

type API struct {
	coordinator                  *Coordinator
	store                        SessionStore
	analysis                     *analysisRuntime
	now                          func() time.Time
	authorizedUserSID            string
	healthMutex                  sync.RWMutex
	healthStatus                 string
	healthStatusSource           func() string
	lifecycleMutex               sync.RWMutex
	sessionLifecycle             SessionLifecycle
	verificationMutex            sync.Mutex
	challenges                   map[string]endVerificationChallenge
	failures                     map[string]endVerificationFailure
	verifier                     SystemCredentialVerifier
	newChallenge                 func() (string, error)
	directoryMonitoringValidator func([]string) ([]string, error)
	monitoringTargetValidator    func([]domain.MonitoringTarget) ([]domain.MonitoringTarget, error)
	agentMutex                   sync.RWMutex
	agentExecutable              string
	agentActivityHandler         AgentActivityHandler
	agentInputShieldHandler      AgentInputShieldHandler
	inputShieldStatus            InputShieldStatusResult
	inputControl                 InputControlResult
	inputShieldCredentials       *InputShieldCredentialManager
}

type endVerificationChallenge struct {
	principal  string
	sessionID  string
	expiresUTC time.Time
}

type endVerificationFailure struct {
	count       int
	lockedUntil time.Time
}

type SessionStore interface {
	CreateSession(ctx context.Context, session domain.Session, at time.Time) error
	UpdateSession(ctx context.Context, session domain.Session, at time.Time) error
}

type sessionLifecycleEventStore interface {
	AppendEventAutoSequence(context.Context, domain.AuditEvent, []byte) (domain.AuditEvent, error)
}

type sessionLifecycleTransitionStore interface {
	UpdateSessionAndAppendEvent(context.Context, domain.Session, time.Time, domain.AuditEvent, []byte) (domain.AuditEvent, error)
}

type baselineReviewStore interface {
	LoadBaselineReview(context.Context, string) (storage.BaselineReview, error)
}

type baselineReviewResolutionStore interface {
	baselineReviewStore
	ResolveBaselineReviewAndUpdateSessionAndAppendEvent(
		context.Context,
		domain.Session,
		time.Time,
		storage.BaselineReviewResolution,
		domain.AuditEvent,
		[]byte,
	) (domain.AuditEvent, error)
}

type sessionLifecycleEventPayload struct {
	PreviousState domain.SessionState `json:"previousState,omitempty"`
	CurrentState  domain.SessionState `json:"currentState"`
	ObservedUTC   time.Time           `json:"observedUtc"`
}

type retentionStore interface {
	SetSessionRetentionLock(context.Context, string, bool) error
	PruneTerminalSessions(context.Context, time.Time, int) ([]string, error)
}

type SessionLifecycle interface {
	WaitForActive(ctx context.Context, sessionID string) error
	WaitForStopped(ctx context.Context, sessionID string) error
}

type SystemCredentialVerifier interface {
	VerifySystemCredentials(ctx context.Context, credentials SystemCredentials, expectedUserSID string) error
}

func NewAPI(coordinator *Coordinator) *API {
	return newAPIWithClock(coordinator, time.Now)
}

func NewPersistentAPI(coordinator *Coordinator, store SessionStore) *API {
	return &API{coordinator: coordinator, store: store, analysis: newAnalysisRuntime(store), now: time.Now}
}

func NewPersistentAuthorizedAPI(coordinator *Coordinator, store SessionStore, authorizedUserSID string) *API {
	return &API{
		coordinator: coordinator, store: store, analysis: newAnalysisRuntime(store), now: time.Now,
		authorizedUserSID: strings.TrimSpace(authorizedUserSID),
	}
}

func newAPIWithClock(coordinator *Coordinator, now func() time.Time) *API {
	return &API{coordinator: coordinator, now: now}
}

func (api *API) Handle(request contracts.Message) (contracts.Message, error) {
	return api.HandleForClient(request, ClientIdentity{})
}

func (api *API) SetHealthStatus(status string) {
	api.healthMutex.Lock()
	defer api.healthMutex.Unlock()

	if status != HealthStatusDegraded {
		status = HealthStatusRunning
	}
	api.healthStatus = status
}

func (api *API) SetHealthStatusSource(source func() string) {
	api.healthMutex.Lock()
	defer api.healthMutex.Unlock()
	api.healthStatusSource = source
}

func (api *API) SetSessionLifecycle(lifecycle SessionLifecycle) {
	api.lifecycleMutex.Lock()
	defer api.lifecycleMutex.Unlock()
	api.sessionLifecycle = lifecycle
}

func (api *API) SetSystemCredentialVerifier(verifier SystemCredentialVerifier) {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	api.verifier = verifier
}

func (api *API) SetAgentExecutable(path string) {
	api.agentMutex.Lock()
	defer api.agentMutex.Unlock()
	api.agentExecutable = strings.TrimSpace(path)
}

func (api *API) SetAgentActivityHandler(handler AgentActivityHandler) {
	api.agentMutex.Lock()
	defer api.agentMutex.Unlock()
	api.agentActivityHandler = handler
}

func (api *API) SetAgentInputShieldHandler(handler AgentInputShieldHandler) {
	api.agentMutex.Lock()
	defer api.agentMutex.Unlock()
	api.agentInputShieldHandler = handler
}

func (api *API) SetInputShieldCredentialManager(manager *InputShieldCredentialManager) {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	api.inputShieldCredentials = manager
}

func (api *API) HandleForClient(request contracts.Message, client ClientIdentity) (contracts.Message, error) {
	return api.HandleForClientContext(context.Background(), request, client)
}

func (api *API) HandleForClientContext(parent context.Context, request contracts.Message, client ClientIdentity) (contracts.Message, error) {
	if err := request.Validate(); err != nil {
		return contracts.Message{}, err
	}
	if !request.DeadlineUTC.After(api.now().UTC()) {
		return api.errorResponse(request, ErrorCodeRequestExpired, "request deadline has expired")
	}
	if api.authorizedUserSID != "" && !strings.EqualFold(api.authorizedUserSID, strings.TrimSpace(client.UserSID)) {
		if request.Type == contracts.MessageTypeHealthGet && strings.EqualFold(strings.TrimSpace(client.UserSID), "S-1-5-18") {
			// The pipe server obtains this SID from the client token. MSI needs
			// liveness only; do not include the owner's session in this response.
			return api.response(request, contracts.MessageTypeHealthResult, HealthResult{Status: api.currentHealthStatus(), MaintenanceGate: true})
		}
		return api.errorResponse(request, ErrorCodeUnauthorized, "client is not authorized for this installation")
	}
	ctx, cancel := context.WithTimeout(parent, min(request.DeadlineUTC.Sub(api.now().UTC()), 30*time.Second))
	defer cancel()

	switch request.Type {
	case contracts.MessageTypeHealthGet:
		return api.healthResponse(request)
	case contracts.MessageTypeDirectoryMonitoringGet:
		return api.getDirectoryMonitoring(request)
	case contracts.MessageTypeDirectoryMonitoringUpdate:
		return api.updateDirectoryMonitoring(request)
	case contracts.MessageTypeSessionCreate:
		return api.createSession(request)
	case contracts.MessageTypeSessionTransition:
		return api.transitionSession(request, client)
	case contracts.MessageTypeSessionEndVerificationCreate:
		return api.createEndVerificationChallenge(request, client)
	case contracts.MessageTypeSessionStartPreviewGet:
		return api.sessionStartPreview(request)
	case contracts.MessageTypeSessionBaselineReviewGet:
		return api.getSessionBaselineReview(ctx, request)
	case contracts.MessageTypeSessionBaselineReviewResolve:
		return api.resolveSessionBaselineReview(ctx, request)
	case contracts.MessageTypeSessionCurrentGet:
		return api.currentSession(request)
	case contracts.MessageTypeSessionList:
		return api.listSessions(ctx, request)
	case contracts.MessageTypeSessionRetentionLockUpdate:
		return api.updateSessionRetentionLock(ctx, request)
	case contracts.MessageTypeSessionRetentionPrune:
		return api.pruneSessionRetention(ctx, request)
	case contracts.MessageTypeTimelineQuery:
		return api.queryTimeline(ctx, request)
	case contracts.MessageTypeRiskEvaluate:
		return api.evaluateRisk(ctx, request)
	case contracts.MessageTypeRiskFindingStatusUpdate:
		return api.updateRiskFindingStatus(ctx, request, client)
	case contracts.MessageTypeAssetDifferenceQuery:
		return api.queryAssetDifferences(ctx, request)
	case contracts.MessageTypeAgentActivityReport:
		return api.recordAgentActivity(ctx, request, client)
	case contracts.MessageTypeInputShieldCredentialGet:
		return api.getInputShieldCredentialStatus(ctx, request)
	case contracts.MessageTypeInputShieldCredentialUpdate:
		return api.updateInputShieldCredentials(ctx, request)
	case contracts.MessageTypeInputShieldCredentialDelete:
		return api.deleteInputShieldCredentials(ctx, request)
	case contracts.MessageTypeInputShieldCredentialVerify:
		return api.verifyInputShieldCredentials(ctx, request, client)
	case contracts.MessageTypeAgentInputShieldReport:
		return api.recordAgentInputShield(ctx, request, client)
	case contracts.MessageTypeInputShieldStatusGet:
		return api.getInputShieldStatus(request)
	case contracts.MessageTypeInputControlGet:
		return api.getInputControl(request)
	case contracts.MessageTypeInputControlStart:
		return api.startInputControl(ctx, request)
	case contracts.MessageTypeInputControlStop:
		return api.stopInputControl(request)
	case contracts.MessageTypeReportExport:
		return api.exportReport(ctx, request, client)
	case contracts.MessageTypeReportChunkGet:
		return api.getReportChunk(request, client)
	default:
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "message type is not accepted as a request")
	}
}

func (api *API) updateSessionRetentionLock(
	ctx context.Context,
	request contracts.Message,
) (contracts.Message, error) {
	store, ok := api.store.(retentionStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeRetentionUnavailable, "session retention is unavailable")
	}
	var payload SessionRetentionLockRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session retention lock payload is invalid")
	}
	if err := store.SetSessionRetentionLock(ctx, payload.SessionID, payload.Locked); err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "session retention lock could not be updated")
	}
	return api.response(request, contracts.MessageTypeSessionRetentionLockResult, SessionRetentionLockResult{
		SessionID: payload.SessionID, Locked: payload.Locked,
	})
}

func (api *API) pruneSessionRetention(
	ctx context.Context,
	request contracts.Message,
) (contracts.Message, error) {
	store, ok := api.store.(retentionStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeRetentionUnavailable, "session retention is unavailable")
	}
	var payload SessionRetentionPruneRequest
	if err := request.DecodePayload(&payload); err != nil || payload.BeforeUTC.IsZero() || payload.Limit < 0 {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session retention prune payload is invalid")
	}
	_, offset := payload.BeforeUTC.Zone()
	if offset != 0 {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session retention prune payload is invalid")
	}
	deleted, err := store.PruneTerminalSessions(ctx, payload.BeforeUTC, payload.Limit)
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "session retention cleanup could not be completed")
	}
	return api.response(request, contracts.MessageTypeSessionRetentionPruneResult, SessionRetentionPruneResult{
		DeletedSessionIDs: deleted,
	})
}

func (api *API) recordAgentActivity(
	ctx context.Context,
	request contracts.Message,
	client ClientIdentity,
) (contracts.Message, error) {
	var payload AgentActivityReportRequest
	if err := request.DecodePayload(&payload); err != nil || !payload.valid() {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "agent activity payload is invalid")
	}
	api.agentMutex.RLock()
	agentExecutable := api.agentExecutable
	handler := api.agentActivityHandler
	api.agentMutex.RUnlock()
	if !sameWindowsPath(agentExecutable, client.ImagePath) {
		return api.errorResponse(request, ErrorCodeUnauthorized, "agent activity client is not authorized")
	}
	if handler == nil {
		return api.errorResponse(request, ErrorCodeAgentUnavailable, "agent activity collection is unavailable")
	}
	session, active := api.coordinator.Current()
	if !active || session.ID != payload.SessionID ||
		(session.State != domain.SessionStateActive && session.State != domain.SessionStateDegraded) {
		return api.errorResponse(request, ErrorCodeNoCurrentSession, "agent activity has no active protection session")
	}
	if !session.MonitoringPolicy.UserSessionActivityEnabled {
		return api.response(request, contracts.MessageTypeAgentActivityResult, AgentActivityResult{SessionID: session.ID})
	}
	payload = applyUserSessionPolicy(payload, session.MonitoringPolicy.Resolved().UserSession)
	if err := handler(ctx, client, payload); err != nil {
		return api.errorResponse(request, ErrorCodeAgentUnavailable, "agent activity could not be recorded")
	}
	return api.response(request, contracts.MessageTypeAgentActivityResult, AgentActivityResult{SessionID: session.ID})
}

func (api *API) recordAgentInputShield(
	ctx context.Context,
	request contracts.Message,
	client ClientIdentity,
) (contracts.Message, error) {
	var payload AgentInputShieldReportRequest
	if err := request.DecodePayload(&payload); err != nil || !payload.valid() {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "agent input shield payload is invalid")
	}
	api.agentMutex.RLock()
	agentExecutable := api.agentExecutable
	handler := api.agentInputShieldHandler
	api.agentMutex.RUnlock()
	if !sameWindowsPath(agentExecutable, client.ImagePath) {
		return api.errorResponse(request, ErrorCodeUnauthorized, "agent input shield client is not authorized")
	}
	control := api.currentInputControl()
	if !control.Enabled || control.ControlID != payload.SessionID {
		return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input control is not active")
	}
	payload = applyInputShieldPolicy(payload, control.Policy)
	if handler != nil && control.Source == "session" {
		if err := handler(ctx, client, payload); err != nil {
			return api.errorResponse(request, ErrorCodeAgentUnavailable, "input shield event could not be recorded")
		}
	}
	api.agentMutex.Lock()
	api.inputShieldStatus = InputShieldStatusResult{
		SessionID: payload.SessionID, State: payload.State, HookRunning: payload.HookRunning,
		DroppedEvents: payload.DroppedEvents, ObservedUTC: payload.ObservedUTC,
		Devices: append([]AgentInputShieldDevice(nil), payload.Devices...),
	}
	api.agentMutex.Unlock()
	return api.response(request, contracts.MessageTypeAgentInputShieldResult, AgentInputShieldResult{SessionID: control.ControlID})
}

func (api *API) getInputShieldStatus(request contracts.Message) (contracts.Message, error) {
	control := api.currentInputControl()
	status := InputShieldStatusResult{SessionID: control.ControlID, State: control.State,
		HookRunning: control.HookRunning, DroppedEvents: control.DroppedEvents,
		ObservedUTC: control.ObservedUTC, Devices: append([]AgentInputShieldDevice(nil), control.Devices...)}
	return api.response(request, contracts.MessageTypeInputShieldStatusResult, status)
}

func (api *API) getInputControl(request contracts.Message) (contracts.Message, error) {
	return api.response(request, contracts.MessageTypeInputControlResult, api.currentInputControl())
}

func (api *API) startInputControl(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	var payload InputControlStartRequest
	if err := request.DecodePayload(&payload); err != nil ||
		(!payload.Indefinite && (payload.DurationMinutes < 1 || payload.DurationMinutes > 480)) ||
		(payload.Indefinite && payload.DurationMinutes != 0) {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "temporary input control payload is invalid")
	}
	payload.Policy.RestoreAfterRestart = false
	validationPolicy := domain.DefaultMonitoringPolicy()
	validationPolicy.FileActivityEnabled = false
	validationPolicy.ProcessAndSoftwareEnabled = false
	validationPolicy.SystemAndNetworkEnabled = false
	validationPolicy.ExternalDevicesEnabled = false
	validationPolicy.UserSessionActivityEnabled = false
	validationPolicy.InputShieldEnabled = true
	validationPolicy.StrictReadAuditEnabled = false
	validationPolicy.InputShield = payload.Policy
	if err := validationPolicy.Validate(); err != nil {
		return api.domainErrorResponse(request, err)
	}
	if payload.Policy.CredentialMode == domain.InputShieldCredentialLocal {
		manager := api.inputShieldCredentialManager()
		if manager == nil {
			return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input shield local credentials are unavailable")
		}
		status, err := manager.Status(ctx)
		if err != nil || !status.Configured {
			return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "configure an input shield local credential before starting input control")
		}
	}
	tokenFactory := api.newChallenge
	var controlID string
	var err error
	if tokenFactory != nil {
		controlID, err = tokenFactory()
	} else {
		controlID, err = newSessionLifecycleEventID()
	}
	if err != nil || strings.TrimSpace(controlID) == "" {
		return api.errorResponse(request, ErrorCodeInvalidRequest, "temporary input control could not be created")
	}
	now := api.now().UTC()
	control := InputControlResult{
		ControlID: controlID, Source: "temporary", Enabled: true, State: "starting",
		StartedUTC: now, Indefinite: payload.Indefinite, Policy: payload.Policy,
	}
	if !payload.Indefinite {
		control.ExpiresUTC = now.Add(time.Duration(payload.DurationMinutes) * time.Minute)
	}
	api.agentMutex.Lock()
	api.inputControl = control
	api.inputShieldStatus = InputShieldStatusResult{}
	api.agentMutex.Unlock()
	return api.response(request, contracts.MessageTypeInputControlResult, control)
}

func (api *API) stopInputControl(request contracts.Message) (contracts.Message, error) {
	api.agentMutex.Lock()
	api.inputControl = InputControlResult{}
	api.inputShieldStatus = InputShieldStatusResult{}
	api.agentMutex.Unlock()
	return api.response(request, contracts.MessageTypeInputControlResult, api.currentInputControl())
}

func (api *API) currentInputControl() InputControlResult {
	now := api.now().UTC()
	api.agentMutex.Lock()
	control := api.inputControl
	if control.Enabled && !control.Indefinite && !control.ExpiresUTC.After(now) {
		api.inputControl = InputControlResult{}
		api.inputShieldStatus = InputShieldStatusResult{}
		control = InputControlResult{}
	}
	status := api.inputShieldStatus
	api.agentMutex.Unlock()

	if !control.Enabled {
		session, ok := api.coordinator.Current()
		if ok && (session.State == domain.SessionStateActive || session.State == domain.SessionStateDegraded) {
			policy := session.MonitoringPolicy.Resolved()
			if policy.InputShieldEnabled && (session.State == domain.SessionStateActive || policy.InputShield.RestoreAfterRestart) {
				control = InputControlResult{ControlID: session.ID, Source: "session", Enabled: true,
					State: "starting", Policy: policy.InputShield}
			}
		}
	}
	if !control.Enabled {
		return InputControlResult{State: "disabled"}
	}
	if status.SessionID == control.ControlID {
		control.State = status.State
		control.HookRunning = status.HookRunning
		control.DroppedEvents = status.DroppedEvents
		control.ObservedUTC = status.ObservedUTC
		control.Devices = append([]AgentInputShieldDevice(nil), status.Devices...)
		heartbeat := time.Duration(control.Policy.HookHeartbeatSeconds) * time.Second
		if !control.ObservedUTC.IsZero() && now.Sub(control.ObservedUTC) > 3*heartbeat {
			control.State = "degraded"
			control.HookRunning = false
		}
	}
	return control
}

func (request AgentInputShieldReportRequest) valid() bool {
	if strings.TrimSpace(request.SessionID) == "" || request.ObservedUTC.IsZero() || request.KeyCode < 0 || request.KeyCode > 255 {
		return false
	}
	if _, offset := request.ObservedUTC.Zone(); offset != 0 {
		return false
	}
	switch request.Action {
	case "started", "stopped", "heartbeat", "input_blocked", "device_connected", "device_removed",
		"unlock_requested", "unlock_succeeded", "unlock_failed", "hook_degraded":
	default:
		return false
	}
	switch request.State {
	case "starting", "protecting", "verifying", "suspended", "degraded", "disabled":
	default:
		return false
	}
	if (request.PointerX == nil) != (request.PointerY == nil) {
		return false
	}
	if request.InputKind != "" && request.InputKind != "keyboard" && request.InputKind != "mouse_button" &&
		request.InputKind != "mouse_wheel" {
		return false
	}
	if request.Device != nil && !request.Device.valid() {
		return false
	}
	if len(request.Devices) > 64 {
		return false
	}
	for index := range request.Devices {
		if !request.Devices[index].valid() {
			return false
		}
	}
	return true
}

func (device AgentInputShieldDevice) valid() bool {
	if device.Kind != "keyboard" && device.Kind != "mouse" {
		return false
	}
	if len(device.InterfacePath) > 4096 || len(device.InstanceID) > 4096 ||
		len(device.VendorID) > 16 || len(device.ProductID) > 16 {
		return false
	}
	if !device.LastActiveUTC.IsZero() {
		_, offset := device.LastActiveUTC.Zone()
		return offset == 0
	}
	return true
}

func applyInputShieldPolicy(request AgentInputShieldReportRequest, policy domain.InputShieldPolicy) AgentInputShieldReportRequest {
	if !policy.RecordBlockedInputCategory {
		request.InputKind = ""
	}
	if !policy.RecordKeyNames {
		request.KeyCode = 0
	}
	if !policy.RecordPointerCoordinates {
		request.PointerX = nil
		request.PointerY = nil
	}
	if !policy.TrackActiveDevices && request.Action != "device_connected" && request.Action != "device_removed" {
		request.Device = nil
	}
	if !policy.TrackActiveDevices {
		request.Devices = nil
	}
	if request.Action == "device_connected" && !policy.WarnOnDeviceArrival {
		request.Device = nil
	}
	if request.Action == "device_removed" && !policy.RecordDeviceRemoval {
		request.Device = nil
	}
	return request
}

func applyUserSessionPolicy(request AgentActivityReportRequest, policy domain.UserSessionPolicy) AgentActivityReportRequest {
	if !policy.RecordForegroundApplication {
		request.ProcessID = 0
		request.ProcessImage = ""
		request.ForegroundStartUTC = time.Time{}
		request.ForegroundEndUTC = time.Time{}
		request.ForegroundDurationMS = 0
	}
	if !policy.RecordWindowTitle {
		request.WindowTitle = ""
	}
	if !policy.RecordKeyboardActivity {
		request.KeyboardActivityCount = 0
	}
	if !policy.RecordMouseClicks {
		request.MouseClickCount = 0
	}
	if !policy.RecordMouseWheel {
		request.MouseWheelCount = 0
	}
	if !policy.RecordHighRiskShortcuts {
		request.HighRiskShortcutCount = nil
	}
	if !policy.RecordKeyboardActivity || !policy.RecordMouseClicks || !policy.RecordMouseWheel {
		request.InputActivityCount = request.KeyboardActivityCount + request.MouseClickCount + request.MouseWheelCount
	}
	if request.InputActivityCount == 0 {
		request.ActivityStartUTC = time.Time{}
		request.ActivityEndUTC = time.Time{}
	}
	return request
}

func (request AgentActivityReportRequest) valid() bool {
	if strings.TrimSpace(request.SessionID) == "" || request.ObservedUTC.IsZero() ||
		len(request.WindowTitle) > 2_048 || len(request.ProcessImage) > 4_096 {
		return false
	}
	_, offset := request.ObservedUTC.Zone()
	if offset != 0 {
		return false
	}
	if request.ActivityStartUTC.IsZero() != request.ActivityEndUTC.IsZero() {
		return false
	}
	if !request.ActivityStartUTC.IsZero() {
		_, startOffset := request.ActivityStartUTC.Zone()
		_, endOffset := request.ActivityEndUTC.Zone()
		if startOffset != 0 || endOffset != 0 || request.ActivityEndUTC.Before(request.ActivityStartUTC) || request.ObservedUTC.Before(request.ActivityEndUTC) {
			return false
		}
	}
	if request.ForegroundStartUTC.IsZero() != request.ForegroundEndUTC.IsZero() || request.ForegroundDurationMS < 0 {
		return false
	}
	if !request.ForegroundStartUTC.IsZero() {
		_, startOffset := request.ForegroundStartUTC.Zone()
		_, endOffset := request.ForegroundEndUTC.Zone()
		duration := request.ForegroundEndUTC.Sub(request.ForegroundStartUTC).Milliseconds()
		if startOffset != 0 || endOffset != 0 || request.ForegroundEndUTC.Before(request.ForegroundStartUTC) ||
			request.ObservedUTC.Before(request.ForegroundEndUTC) || request.ForegroundDurationMS != duration {
			return false
		}
	}
	if request.KeyboardActivityCount+request.MouseClickCount+request.MouseWheelCount > request.InputActivityCount {
		return false
	}
	var shortcutTotal uint64
	for category, count := range request.HighRiskShortcutCount {
		switch category {
		case "alt_tab", "win_run", "win_lock", "task_manager":
		default:
			return false
		}
		if count == 0 {
			return false
		}
		shortcutTotal += count
	}
	if shortcutTotal > request.KeyboardActivityCount {
		return false
	}
	return request.ProcessID != 0 || strings.TrimSpace(request.WindowTitle) != "" ||
		strings.TrimSpace(request.ProcessImage) != "" || request.InputActivityCount != 0
}

func sameWindowsPath(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(expected), filepath.Clean(actual))
}

func (api *API) currentHealthStatus() string {
	api.healthMutex.RLock()
	status := api.healthStatus
	source := api.healthStatusSource
	api.healthMutex.RUnlock()
	if status == "" {
		status = HealthStatusRunning
	}
	if source != nil && source() == HealthStatusDegraded {
		status = HealthStatusDegraded
	}
	return status
}

func (api *API) healthResponse(request contracts.Message) (contracts.Message, error) {
	result := HealthResult{Status: api.currentHealthStatus(), MaintenanceGate: true}
	if session, ok := api.coordinator.Current(); ok {
		result.Session = &session
	}
	return api.response(request, contracts.MessageTypeHealthResult, result)
}

func (api *API) createSession(request contracts.Message) (contracts.Message, error) {
	var payload CreateSessionRequest
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session create payload is invalid")
	}

	ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
	defer cancel()
	session, err := api.coordinator.CreatePersistedWithMonitoringPolicyProvider(payload.ID, payload.Name, func() (domain.MonitoringPolicy, error) {
		return api.monitoringPolicyForNewSession(ctx, payload.MonitoringLevel, payload.MonitoringMode)
	}, func(session domain.Session) error {
		if err := api.validateDirectoryMonitoring(ctx); err != nil {
			return err
		}
		if api.store == nil {
			return nil
		}
		if err := api.store.CreateSession(ctx, session, api.now().UTC()); err != nil {
			return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
		}
		return nil
	})
	if err != nil {
		return api.domainErrorResponse(request, err)
	}
	if err := api.recordSessionLifecycleEvent(ctx, nil, session); err != nil {
		return api.domainErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeSessionResult, SessionResult{Session: &session})
}

func (api *API) sessionStartPreview(request contracts.Message) (contracts.Message, error) {
	var payload SessionStartPreviewRequest
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "启动预览参数格式无效")
	}

	store, ok := api.store.(DirectoryMonitoringStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "重点目录配置暂不可用")
	}
	ctx, cancel := context.WithDeadline(context.Background(), request.DeadlineUTC)
	defer cancel()
	monitoredTargetCount := 0
	var monitoredTargets []domain.MonitoringTarget
	if targetStore, ok := api.store.(MonitoringTargetStore); ok {
		targets, err := targetStore.LoadMonitoringTargets(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法读取")
		}
		monitoredTargetCount = len(targets)
		monitoredTargets = domain.CloneMonitoringTargets(targets)
	}
	if monitoredTargetCount == 0 {
		directories, err := store.LoadMonitoredDirectories(ctx)
		if err != nil {
			return api.errorResponse(request, ErrorCodeStorageFailure, "重点目录配置无法读取")
		}
		monitoredTargetCount = len(directories)
		for _, directory := range directories {
			monitoredTargets = append(monitoredTargets, domain.MonitoringTarget{
				Path: directory, Kind: domain.MonitoringTargetKindDirectory, Recursive: true,
			})
		}
	}

	policy, err := api.monitoringPolicyForNewSession(ctx, payload.MonitoringLevel, payload.MonitoringMode)
	if err != nil {
		return api.domainErrorResponse(request, err)
	}
	preview, err := (domain.MonitoringProfile{Level: policy.MonitoringLevel()}).StartPreview(monitoredTargetCount)
	if err != nil {
		return api.domainErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeSessionStartPreviewResult, SessionStartPreviewResult{
		MonitoringMode:       policy.Mode,
		MonitoringLevel:      preview.MonitoringLevel,
		MonitoringPolicy:     policy,
		MonitoredTargetCount: preview.MonitoredTargetCount,
		Targets:              monitoredTargets,
		Impact: MonitoringImpactResult{
			RequiresAdministrator: preview.Impact.RequiresAdministrator,
			ExpectedEventVolume:   preview.Impact.ExpectedEventVolume,
			PerformanceImpact:     preview.Impact.PerformanceImpact,
			StorageImpact:         preview.Impact.StorageImpact,
		},
	})
}

func (api *API) monitoringPolicyForNewSession(
	ctx context.Context,
	legacyLevel domain.MonitoringLevel,
	mode domain.MonitoringMode,
) (domain.MonitoringPolicy, error) {
	if mode != "" {
		if profileStore, ok := api.store.(MonitoringProfileStore); ok {
			profiles, err := profileStore.LoadMonitoringProfiles(ctx)
			if err != nil {
				return domain.MonitoringPolicy{}, fmt.Errorf("%w: load monitoring profiles: %v", ErrSessionPersistence, err)
			}
			policy, err := profiles.Policy(mode)
			if err != nil {
				return domain.MonitoringPolicy{}, err
			}
			return policy, nil
		}
		if mode != domain.MonitoringModeCustom {
			return domain.BuiltInMonitoringPolicy(mode)
		}
	}
	if policyStore, ok := api.store.(MonitoringPolicyStore); ok {
		policy, err := policyStore.LoadMonitoringPolicy(ctx)
		if err != nil {
			return domain.MonitoringPolicy{}, fmt.Errorf("%w: load monitoring policy: %v", ErrSessionPersistence, err)
		}
		policy = policy.Resolved()
		if mode == domain.MonitoringModeCustom {
			policy.Mode = domain.MonitoringModeCustom
		}
		if err := policy.Validate(); err != nil {
			return domain.MonitoringPolicy{}, err
		}
		return policy, nil
	}
	if legacyLevel == "" {
		legacyLevel = domain.MonitoringLevelStandard
	}
	if err := (domain.MonitoringProfile{Level: legacyLevel}).Validate(); err != nil {
		return domain.MonitoringPolicy{}, err
	}
	policy := domain.DefaultMonitoringPolicy()
	policy.StrictReadAuditEnabled = legacyLevel == domain.MonitoringLevelStrict
	if mode == domain.MonitoringModeCustom {
		policy.Mode = domain.MonitoringModeCustom
	}
	return policy, nil
}

func (api *API) getSessionBaselineReview(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	var payload SessionBaselineReviewGetRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "baseline review payload is invalid")
	}
	session, active := api.coordinator.Current()
	if !active || session.ID != payload.SessionID {
		return api.errorResponse(request, ErrorCodeNoCurrentSession, "baseline review session is not current")
	}
	if session.State != domain.SessionStateBaselineReview {
		return api.errorResponse(request, ErrorCodeInvalidTransition, "baseline review is not pending")
	}
	store, ok := api.store.(baselineReviewStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "baseline review is unavailable")
	}
	review, err := store.LoadBaselineReview(ctx, session.ID)
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "baseline review could not be loaded")
	}
	return api.response(request, contracts.MessageTypeSessionBaselineReviewResult, SessionBaselineReviewResult{
		SessionID: review.SessionID, Decision: review.Decision, Resolution: review.Resolution,
	})
}

func (api *API) resolveSessionBaselineReview(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	var payload SessionBaselineReviewResolveRequest
	if err := request.DecodePayload(&payload); err != nil || strings.TrimSpace(payload.SessionID) == "" ||
		(payload.Resolution != storage.BaselineReviewResolutionContinue && payload.Resolution != storage.BaselineReviewResolutionCancel) {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "baseline review resolution payload is invalid")
	}
	session, active := api.coordinator.Current()
	if !active || session.ID != payload.SessionID {
		return api.errorResponse(request, ErrorCodeNoCurrentSession, "baseline review session is not current")
	}
	if session.State != domain.SessionStateBaselineReview {
		return api.errorResponse(request, ErrorCodeInvalidTransition, "baseline review is not pending")
	}
	store, ok := api.store.(baselineReviewResolutionStore)
	if !ok {
		return api.errorResponse(request, ErrorCodeUnsupportedMessage, "baseline review resolution is unavailable")
	}
	review, err := store.LoadBaselineReview(ctx, session.ID)
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "baseline review could not be loaded")
	}
	var target domain.SessionState
	switch payload.Resolution {
	case storage.BaselineReviewResolutionContinue:
		if !review.Decision.CanContinue {
			return api.errorResponse(request, ErrorCodeInvalidTransition, "baseline review cannot continue")
		}
		target = domain.SessionStateActive
	case storage.BaselineReviewResolutionCancel:
		if !review.Decision.CanCancel {
			return api.errorResponse(request, ErrorCodeInvalidTransition, "baseline review cannot be cancelled")
		}
		target = domain.SessionStateFailed
	}
	resolvedSession, err := api.coordinator.TransitionPersisted(target, func(next domain.Session) error {
		event, eventPayload, err := api.sessionLifecycleEvent(&session, next)
		if err != nil {
			return err
		}
		if _, err := store.ResolveBaselineReviewAndUpdateSessionAndAppendEvent(
			ctx, next, api.now().UTC(), payload.Resolution, event, eventPayload,
		); err != nil {
			return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
		}
		return nil
	})
	if err != nil {
		return api.domainErrorResponse(request, err)
	}
	return api.response(request, contracts.MessageTypeSessionBaselineReviewResult, SessionBaselineReviewResult{
		SessionID: review.SessionID, Decision: review.Decision, Resolution: payload.Resolution, Session: &resolvedSession,
	})
}

func (api *API) transitionSession(request contracts.Message, client ClientIdentity) (contracts.Message, error) {
	var payload TransitionSessionRequest
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "session transition payload is invalid")
	}
	if payload.State == domain.SessionStateFinalizing {
		if err := api.verifyEndProtection(request, client, payload.Verification); err != nil {
			return api.errorResponse(request, verificationErrorCode(err), "system credential verification did not complete")
		}
	}
	return api.transitionSessionState(request, payload.State)
}

func (api *API) transitionSessionState(
	request contracts.Message,
	nextState domain.SessionState,
) (contracts.Message, error) {
	previous, hadPrevious := api.coordinator.Current()
	atomicStore, useAtomicStore := api.store.(sessionLifecycleTransitionStore)
	remaining := request.DeadlineUTC.Sub(api.now().UTC())
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	api.lifecycleMutex.RLock()
	lifecycle := api.sessionLifecycle
	api.lifecycleMutex.RUnlock()
	if lifecycle != nil {
		lifecycleCtx, lifecycleCancel := context.WithTimeout(ctx, remaining)
		defer lifecycleCancel()
		var err error
		switch nextState {
		case domain.SessionStateActive:
			session, ok := api.coordinator.Current()
			if !ok {
				return api.errorResponse(request, ErrorCodeNoCurrentSession, ErrNoCurrentSession.Error())
			}
			err = lifecycle.WaitForActive(lifecycleCtx, session.ID)
		case domain.SessionStateCompleted:
			session, ok := api.coordinator.Current()
			if !ok {
				return api.errorResponse(request, ErrorCodeNoCurrentSession, ErrNoCurrentSession.Error())
			}
			err = lifecycle.WaitForStopped(lifecycleCtx, session.ID)
		}
		if err != nil {
			if nextState == domain.SessionStateActive {
				if pending, ok := api.coordinator.Current(); ok && pending.State == domain.SessionStateBaselineReview {
					return api.response(request, contracts.MessageTypeSessionResult, SessionResult{Session: &pending})
				}
			}
			return api.errorResponse(request, ErrorCodeInvalidRequest, "collector lifecycle transition did not complete")
		}
	}

	session, err := api.coordinator.TransitionPersisted(nextState, func(session domain.Session) error {
		if api.store == nil {
			return nil
		}
		if hadPrevious && useAtomicStore {
			event, payload, err := api.sessionLifecycleEvent(&previous, session)
			if err != nil {
				return err
			}
			if _, err := atomicStore.UpdateSessionAndAppendEvent(ctx, session, api.now().UTC(), event, payload); err != nil {
				return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
			}
			return nil
		}
		if err := api.store.UpdateSession(ctx, session, api.now().UTC()); err != nil {
			return fmt.Errorf("%w: %v", ErrSessionPersistence, err)
		}
		return nil
	})
	if err != nil {
		return api.domainErrorResponse(request, err)
	}
	if hadPrevious && !useAtomicStore {
		if err := api.recordSessionLifecycleEvent(ctx, &previous, session); err != nil {
			return api.domainErrorResponse(request, err)
		}
	}
	return api.response(request, contracts.MessageTypeSessionResult, SessionResult{Session: &session})
}

func (api *API) recordSessionLifecycleEvent(
	ctx context.Context,
	previous *domain.Session,
	session domain.Session,
) error {
	store, ok := api.store.(sessionLifecycleEventStore)
	if !ok {
		return nil
	}
	event, payload, err := api.sessionLifecycleEvent(previous, session)
	if err != nil {
		return err
	}
	if _, err := store.AppendEventAutoSequence(ctx, event, payload); err != nil {
		return fmt.Errorf("%w: record lifecycle event: %v", ErrSessionPersistence, err)
	}
	return nil
}

func (api *API) sessionLifecycleEvent(
	previous *domain.Session,
	session domain.Session,
) (domain.AuditEvent, []byte, error) {
	action, severity := lifecycleEventDetails(previous, session.State)
	observedUTC := api.now().UTC()
	eventPayload := sessionLifecycleEventPayload{
		CurrentState: session.State,
		ObservedUTC:  observedUTC,
	}
	if previous != nil {
		eventPayload.PreviousState = previous.State
	}
	payload, err := json.Marshal(eventPayload)
	if err != nil {
		return domain.AuditEvent{}, nil, fmt.Errorf("%w: encode lifecycle event: %v", ErrSessionPersistence, err)
	}
	eventID, err := newSessionLifecycleEventID()
	if err != nil {
		return domain.AuditEvent{}, nil, fmt.Errorf("%w: %v", ErrSessionPersistence, err)
	}
	return domain.AuditEvent{
		EventID: eventID, SessionID: session.ID, Category: domain.EventCategoryHealth,
		Action: action, Severity: severity, ObservedUTC: observedUTC,
		MonotonicTicks: observedUTC.UnixNano(), Source: "desktop_guard_service",
		Confidence: domain.EventConfidenceDirect,
	}, payload, nil
}

func lifecycleEventDetails(previous *domain.Session, current domain.SessionState) (string, domain.EventSeverity) {
	switch current {
	case domain.SessionStateDraft:
		return "session_created", domain.EventSeverityLow
	case domain.SessionStatePreparing:
		return "protection_preparing", domain.EventSeverityLow
	case domain.SessionStateBaselineReview:
		return "baseline_review_required", domain.EventSeverityMedium
	case domain.SessionStateActive:
		if previous != nil && (previous.State == domain.SessionStateDegraded || previous.State == domain.SessionStatePaused) {
			return "protection_resumed", domain.EventSeverityMedium
		}
		return "protection_started", domain.EventSeverityLow
	case domain.SessionStatePaused:
		return "protection_paused", domain.EventSeverityMedium
	case domain.SessionStateDegraded, domain.SessionStateFailed:
		return "protection_interrupted", domain.EventSeverityHigh
	case domain.SessionStateFinalizing:
		return "protection_ending", domain.EventSeverityLow
	case domain.SessionStateCompleted:
		return "protection_ended", domain.EventSeverityLow
	default:
		return "protection_state_changed", domain.EventSeverityLow
	}
}

func newSessionLifecycleEventID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create lifecycle event id: %w", err)
	}
	return "lifecycle-" + hex.EncodeToString(data), nil
}

func (api *API) createEndVerificationChallenge(
	request contracts.Message,
	client ClientIdentity,
) (contracts.Message, error) {
	session, ok := api.coordinator.Current()
	if !ok {
		return api.errorResponse(request, ErrorCodeNoCurrentSession, ErrNoCurrentSession.Error())
	}
	if session.State != domain.SessionStateActive && session.State != domain.SessionStateDegraded && session.State != domain.SessionStatePaused {
		return api.errorResponse(request, ErrorCodeInvalidTransition, "protection session cannot be ended in its current state")
	}
	principal := client.Principal()
	if principal == "" {
		return api.errorResponse(request, ErrorCodeUnauthorized, "client identity is required")
	}
	tokenFactory := api.newChallenge
	if tokenFactory == nil {
		tokenFactory = newEndVerificationToken
	}
	token, err := tokenFactory()
	if err != nil || strings.TrimSpace(token) == "" {
		return api.errorResponse(request, ErrorCodeInvalidRequest, "could not create end verification challenge")
	}
	expiresUTC := api.now().UTC().Add(endVerificationLifetime)
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	if api.challenges == nil {
		api.challenges = make(map[string]endVerificationChallenge)
	}
	now := api.now().UTC()
	api.deleteExpiredChallengesLocked(now)
	if failure, exists := api.failures[principal]; exists && failure.lockedUntil.After(now) {
		return api.errorResponse(request, ErrorCodeVerificationLocked, "system credential verification is temporarily locked")
	}
	api.challenges[token] = endVerificationChallenge{principal: principal, sessionID: session.ID, expiresUTC: expiresUTC}
	return api.response(request, contracts.MessageTypeSessionEndVerificationResult, EndVerificationChallenge{Token: token, ExpiresUTC: expiresUTC})
}

func (api *API) verifyEndProtection(
	request contracts.Message,
	client ClientIdentity,
	verification *EndProtectionVerification,
) error {
	if verification == nil || strings.TrimSpace(verification.Token) == "" ||
		strings.TrimSpace(verification.UserName) == "" || len(verification.Password) == 0 {
		return errEndVerificationRequired
	}
	defer clear(verification.Password)
	session, ok := api.coordinator.Current()
	if !ok {
		return ErrNoCurrentSession
	}
	api.verificationMutex.Lock()
	challenge, exists := api.challenges[verification.Token]
	if exists {
		delete(api.challenges, verification.Token)
	}
	api.verificationMutex.Unlock()
	if !exists || !challenge.expiresUTC.After(api.now().UTC()) || challenge.principal != client.Principal() || challenge.sessionID != session.ID {
		return errEndVerificationRequired
	}
	api.verificationMutex.Lock()
	verifier := api.verifier
	api.verificationMutex.Unlock()
	if verifier == nil {
		return ErrSystemCredentialVerificationUnavailable
	}
	remaining := request.DeadlineUTC.Sub(api.now().UTC())
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()
	if err := verifier.VerifySystemCredentials(ctx, SystemCredentials{
		UserName: verification.UserName, Domain: verification.Domain, Password: verification.Password,
	}, strings.TrimSpace(client.UserSID)); err != nil {
		api.recordVerificationFailure(client.Principal())
		return fmt.Errorf("verify system credentials: %w", err)
	}
	api.clearVerificationFailures(client.Principal())
	return nil
}

var errEndVerificationRequired = errors.New("end verification is required")

func verificationErrorCode(err error) string {
	if errors.Is(err, errEndVerificationRequired) {
		return ErrorCodeVerificationRequired
	}
	return ErrorCodeVerificationFailed
}

func (api *API) deleteExpiredChallengesLocked(now time.Time) {
	for token, challenge := range api.challenges {
		if !challenge.expiresUTC.After(now) {
			delete(api.challenges, token)
		}
	}
}

func (api *API) recordVerificationFailure(principal string) {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	if api.failures == nil {
		api.failures = make(map[string]endVerificationFailure)
	}
	failure := api.failures[principal]
	failure.count++
	if failure.count >= maximumEndVerificationFailures {
		failure.count = 0
		failure.lockedUntil = api.now().UTC().Add(endVerificationLockout)
	}
	api.failures[principal] = failure
}

func (api *API) clearVerificationFailures(principal string) {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	delete(api.failures, principal)
}

func (api *API) getInputShieldCredentialStatus(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	manager := api.inputShieldCredentialManager()
	if manager == nil {
		return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input shield credentials are unavailable")
	}
	status, err := manager.Status(ctx)
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "input shield credential status could not be read")
	}
	return api.response(request, contracts.MessageTypeInputShieldCredentialResult, InputShieldCredentialResult{
		Configured: status.Configured, RecoveryCodeEnabled: status.RecoveryCodeEnabled,
	})
}

func (api *API) updateInputShieldCredentials(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	var payload InputShieldCredentialUpdateRequest
	if err := request.DecodePayload(&payload); err != nil {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "input shield credential payload is invalid")
	}
	defer clear(payload.Password)
	manager := api.inputShieldCredentialManager()
	if manager == nil {
		return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input shield credentials are unavailable")
	}
	recoveryCode, err := manager.SetPassword(ctx, payload.Password, payload.EnableRecovery)
	if errors.Is(err, ErrInputShieldPasswordInvalid) {
		return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
	}
	if err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "input shield credentials could not be saved")
	}
	return api.response(request, contracts.MessageTypeInputShieldCredentialResult, InputShieldCredentialResult{
		Configured: true, RecoveryCodeEnabled: payload.EnableRecovery, RecoveryCode: recoveryCode,
	})
}

func (api *API) deleteInputShieldCredentials(ctx context.Context, request contracts.Message) (contracts.Message, error) {
	manager := api.inputShieldCredentialManager()
	if manager == nil {
		return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input shield credentials are unavailable")
	}
	if err := manager.Delete(ctx); err != nil {
		return api.errorResponse(request, ErrorCodeStorageFailure, "input shield credentials could not be deleted")
	}
	return api.response(request, contracts.MessageTypeInputShieldCredentialResult, InputShieldCredentialResult{})
}

func (api *API) verifyInputShieldCredentials(
	ctx context.Context,
	request contracts.Message,
	client ClientIdentity,
) (contracts.Message, error) {
	var payload InputShieldCredentialVerifyRequest
	if err := request.DecodePayload(&payload); err != nil || len(payload.Password) == 0 {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "input shield verification payload is invalid")
	}
	defer clear(payload.Password)
	control := api.currentInputControl()
	if !control.Enabled {
		return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input control is not active")
	}
	principal := client.Principal()
	if principal == "" {
		return api.errorResponse(request, ErrorCodeUnauthorized, "client identity is required")
	}
	maximumFailures := control.Policy.MaxFailedUnlockAttempts
	lockout := time.Duration(control.Policy.UnlockLockoutSeconds) * time.Second
	if control.Policy.CredentialMode == domain.InputShieldCredentialLocal {
		manager := api.inputShieldCredentialManager()
		if manager == nil {
			return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "input shield credentials are unavailable")
		}
		err := manager.Verify(ctx, principal, payload.Password, payload.Recovery, maximumFailures, lockout)
		if errors.Is(err, ErrInputShieldCredentialLocked) {
			return api.errorResponse(request, ErrorCodeVerificationLocked, err.Error())
		}
		if err != nil {
			return api.errorResponse(request, ErrorCodeVerificationFailed, "input shield credential verification failed")
		}
		return api.inputShieldVerificationSucceeded(request, control.Policy, control.Source)
	}
	if payload.Recovery || strings.TrimSpace(payload.UserName) == "" {
		return api.errorResponse(request, ErrorCodeInvalidPayload, "Windows credential verification payload is invalid")
	}
	failureKey := "input-shield\x00" + principal
	if api.verificationIsLocked(failureKey) {
		return api.errorResponse(request, ErrorCodeVerificationLocked, "input shield credential verification is temporarily locked")
	}
	api.verificationMutex.Lock()
	verifier := api.verifier
	api.verificationMutex.Unlock()
	if verifier == nil {
		return api.errorResponse(request, ErrorCodeInputShieldUnavailable, "Windows credential verification is unavailable")
	}
	if err := verifier.VerifySystemCredentials(ctx, SystemCredentials{
		UserName: payload.UserName, Domain: payload.Domain, Password: payload.Password,
	}, strings.TrimSpace(client.UserSID)); err != nil {
		if api.recordVerificationFailureWithLimit(failureKey, maximumFailures, lockout) {
			return api.errorResponse(request, ErrorCodeVerificationLocked, "input shield credential verification is temporarily locked")
		}
		return api.errorResponse(request, ErrorCodeVerificationFailed, "input shield credential verification failed")
	}
	api.clearVerificationFailures(failureKey)
	return api.inputShieldVerificationSucceeded(request, control.Policy, control.Source)
}

func (api *API) inputShieldVerificationSucceeded(
	request contracts.Message,
	policy domain.InputShieldPolicy,
	source string,
) (contracts.Message, error) {
	if source == "temporary" {
		api.agentMutex.Lock()
		api.inputControl = InputControlResult{}
		api.inputShieldStatus = InputShieldStatusResult{}
		api.agentMutex.Unlock()
		return api.response(request, contracts.MessageTypeInputShieldCredentialResult, InputShieldCredentialResult{Verified: true})
	}
	if policy.UnlockAction == domain.InputShieldUnlockActionEndSession {
		for _, state := range []domain.SessionState{domain.SessionStateFinalizing, domain.SessionStateCompleted} {
			response, err := api.transitionSessionState(request, state)
			if err != nil || response.Type == contracts.MessageTypeError {
				return response, err
			}
		}
	}
	return api.response(request, contracts.MessageTypeInputShieldCredentialResult, InputShieldCredentialResult{Verified: true})
}

func (api *API) inputShieldCredentialManager() *InputShieldCredentialManager {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	return api.inputShieldCredentials
}

func (api *API) verificationIsLocked(principal string) bool {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	failure, exists := api.failures[principal]
	return exists && failure.lockedUntil.After(api.now().UTC())
}

func (api *API) recordVerificationFailureWithLimit(principal string, maximumFailures int, lockout time.Duration) bool {
	api.verificationMutex.Lock()
	defer api.verificationMutex.Unlock()
	if api.failures == nil {
		api.failures = make(map[string]endVerificationFailure)
	}
	failure := api.failures[principal]
	failure.count++
	if failure.count >= maximumFailures {
		failure.count = 0
		failure.lockedUntil = api.now().UTC().Add(lockout)
	}
	api.failures[principal] = failure
	return failure.lockedUntil.After(api.now().UTC())
}

func newEndVerificationToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create end verification challenge: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func (api *API) currentSession(request contracts.Message) (contracts.Message, error) {
	session, ok := api.coordinator.Current()
	if !ok {
		return api.errorResponse(request, ErrorCodeNoCurrentSession, ErrNoCurrentSession.Error())
	}
	return api.response(request, contracts.MessageTypeSessionResult, SessionResult{Session: &session})
}

func (api *API) domainErrorResponse(
	request contracts.Message,
	err error,
) (contracts.Message, error) {
	switch {
	case errors.Is(err, ErrSessionInProgress):
		return api.errorResponse(request, ErrorCodeSessionInProgress, err.Error())
	case errors.Is(err, ErrNoCurrentSession):
		return api.errorResponse(request, ErrorCodeNoCurrentSession, err.Error())
	case errors.Is(err, domain.ErrInvalidSessionTransition):
		return api.errorResponse(request, ErrorCodeInvalidTransition, err.Error())
	case errors.Is(err, ErrSessionPersistence):
		return api.errorResponse(request, ErrorCodeStorageFailure, "session state could not be persisted")
	case errors.Is(err, ErrDirectoryMonitoringInvalid):
		return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
	case errors.Is(err, domain.ErrInvalidMonitoringLevel), errors.Is(err, domain.ErrInvalidMonitoredTargetCount),
		errors.Is(err, domain.ErrMonitoringPolicyRequiresCapability), errors.Is(err, domain.ErrStrictReadAuditRequiresFileActivity),
		errors.Is(err, domain.ErrMonitoringPolicyVersionUnsupported), errors.Is(err, domain.ErrMonitoringModeInvalid),
		errors.Is(err, domain.ErrMonitoringPolicyDetailInvalid):
		return api.errorResponse(request, ErrorCodeInvalidPayload, err.Error())
	default:
		return api.errorResponse(request, ErrorCodeInvalidRequest, err.Error())
	}
}

func (api *API) response(
	request contracts.Message,
	messageType contracts.MessageType,
	payload any,
) (contracts.Message, error) {
	return contracts.NewMessage(request.RequestID, messageType, request.DeadlineUTC, payload)
}

func (api *API) errorResponse(
	request contracts.Message,
	code string,
	message string,
) (contracts.Message, error) {
	if code == "" {
		return contracts.Message{}, fmt.Errorf("error response code is required")
	}
	return api.response(request, contracts.MessageTypeError, ErrorResult{
		Code:    code,
		Message: message,
	})
}
