package reporting

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"desktopguardpro/internal/buildinfo"
	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/risk"
	"desktopguardpro/internal/storage"
)

const (
	reportSchemaVersion     = 1
	defaultReportEventLimit = 10_000
	maximumReportEventLimit = 100_000
	defaultReportTextLimit  = 512
	maximumReportTextLimit  = 4_096
)

var ErrReportInputInvalid = errors.New("report input is invalid")

type ObjectDetailMode string

const (
	ObjectDetailOmit     ObjectDetailMode = "omit"
	ObjectDetailBasename ObjectDetailMode = "basename"
	ObjectDetailFull     ObjectDetailMode = "full"
)

type RedactionPolicy struct {
	ObjectDetails       ObjectDetailMode `json:"objectDetails"`
	IncludeProcessKey   bool             `json:"includeProcessKey"`
	IncludePayload      bool             `json:"includePayload"`
	IncludeUsernames    bool             `json:"includeUsernames"`
	IncludeWindowTitles bool             `json:"includeWindowTitles"`
	MaximumTextLength   int              `json:"maximumTextLength"`
}

type ReportOptions struct {
	Redaction         RedactionPolicy
	MaximumEventCount int
	AssetDifferences  []domain.AssetDifference
	SoftwareVersion   string
	Now               func() time.Time
}

type Report struct {
	Range           *ReportRange        `json:"range,omitempty"`
	SchemaVersion   int                 `json:"schemaVersion"`
	SoftwareVersion string              `json:"softwareVersion"`
	GeneratedUTC    time.Time           `json:"generatedUtc"`
	Session         ReportSession       `json:"session"`
	Redaction       RedactionPolicy     `json:"redaction"`
	Summary         SessionSummary      `json:"summary"`
	Findings        []ReportFinding     `json:"findings"`
	RuleFailures    []risk.RuleFailure  `json:"ruleFailures,omitempty"`
	RuleVersions    []risk.RuleVersion  `json:"ruleVersions,omitempty"`
	AssetChanges    []ReportAssetChange `json:"assetChanges,omitempty"`
	Timeline        []ReportEvent       `json:"timeline"`
}

type ReportRange struct {
	FirstSequence     uint64 `json:"firstSequence"`
	LastSequence      uint64 `json:"lastSequence"`
	SessionEventCount uint64 `json:"sessionEventCount"`
	Partial           bool   `json:"partial"`
}

type ReportSession struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	State    domain.SessionState `json:"state"`
	Revision uint64              `json:"revision"`
}

type ReportFinding struct {
	ID               string             `json:"id"`
	RuleID           string             `json:"ruleId"`
	Title            string             `json:"title"`
	Summary          string             `json:"summary"`
	Level            risk.Level         `json:"level"`
	Score            uint8              `json:"score"`
	Confidence       float64            `json:"confidence"`
	Status           risk.FindingStatus `json:"status"`
	FirstObservedUTC time.Time          `json:"firstObservedUtc"`
	LastObservedUTC  time.Time          `json:"lastObservedUtc"`
	Evidence         []ReportEvidence   `json:"evidence"`
	Tags             []string           `json:"tags,omitempty"`
}

type ReportEvidence struct {
	EventID     string               `json:"eventId"`
	Sequence    uint64               `json:"sequence"`
	ObservedUTC time.Time            `json:"observedUtc"`
	Category    domain.EventCategory `json:"category"`
	Action      string               `json:"action"`
	ObjectKey   string               `json:"objectKey,omitempty"`
}

type ReportAssetChange struct {
	Kind              domain.AssetDifferenceKind `json:"kind"`
	PresenceStatus    string                     `json:"presenceStatus,omitempty"`
	Category          domain.AssetCategory       `json:"category"`
	Identifier        string                     `json:"identifier,omitempty"`
	DisplayName       string                     `json:"displayName,omitempty"`
	ChangedAttributes []string                   `json:"changedAttributes,omitempty"`
}

type ReportEvent struct {
	EventID     string                 `json:"eventId"`
	Sequence    uint64                 `json:"sequence"`
	Category    domain.EventCategory   `json:"category"`
	Action      string                 `json:"action"`
	Severity    domain.EventSeverity   `json:"severity"`
	ObservedUTC time.Time              `json:"observedUtc"`
	ProcessKey  string                 `json:"processKey,omitempty"`
	ObjectKey   string                 `json:"objectKey,omitempty"`
	Source      string                 `json:"source"`
	Confidence  domain.EventConfidence `json:"confidence"`
	Payload     json.RawMessage        `json:"payload,omitempty"`
}

func BuildReport(
	session domain.Session,
	records []storage.EventRecord,
	evaluation risk.Evaluation,
	summary SessionSummary,
	options ReportOptions,
) (Report, error) {
	options, err := normalizeReportOptions(options)
	ruleVersions, versionsErr := normalizeRuleVersions(evaluation.RuleVersions)
	if err != nil || validateSummarySession(session) != nil ||
		summary.SessionID != session.ID || summary.SessionName != session.Name || summary.SessionState != session.State ||
		evaluation.SessionID != session.ID || summary.EventCount != uint64(len(records)) ||
		summary.FindingCount != uint64(len(evaluation.Findings)) ||
		summary.RuleFailureCount != uint64(len(evaluation.Failures)) || len(records) > options.MaximumEventCount ||
		versionsErr != nil {
		return Report{}, ErrReportInputInvalid
	}
	generatedUTC := options.Now().UTC()
	if generatedUTC.IsZero() {
		return Report{}, ErrReportInputInvalid
	}
	report := Report{
		SchemaVersion: reportSchemaVersion, SoftwareVersion: options.SoftwareVersion, GeneratedUTC: generatedUTC,
		Session:   ReportSession{ID: session.ID, Name: session.Name, State: session.State, Revision: session.Revision},
		Redaction: options.Redaction, Summary: summary,
		Findings:     make([]ReportFinding, 0, len(evaluation.Findings)),
		RuleFailures: append([]risk.RuleFailure(nil), evaluation.Failures...),
		RuleVersions: ruleVersions,
		AssetChanges: make([]ReportAssetChange, 0, len(options.AssetDifferences)),
		Timeline:     make([]ReportEvent, 0, len(records)),
	}
	for _, difference := range options.AssetDifferences {
		change, err := reportAssetChange(difference, options.Redaction)
		if err != nil {
			return Report{}, ErrReportInputInvalid
		}
		report.AssetChanges = append(report.AssetChanges, change)
	}
	for _, finding := range evaluation.Findings {
		if err := finding.Validate(); err != nil || finding.SessionID != session.ID {
			return Report{}, ErrReportInputInvalid
		}
		report.Findings = append(report.Findings, reportFinding(finding, options.Redaction))
	}
	for _, record := range records {
		if err := record.Event.Validate(); err != nil || record.Event.SessionID != session.ID {
			return Report{}, ErrReportInputInvalid
		}
		reportEvent, err := reportEvent(record, options.Redaction)
		if err != nil {
			return Report{}, err
		}
		report.Timeline = append(report.Timeline, reportEvent)
	}
	return report, nil
}

func normalizeRuleVersions(versions []risk.RuleVersion) ([]risk.RuleVersion, error) {
	if len(versions) == 0 {
		return nil, nil
	}
	normalized := make([]risk.RuleVersion, 0, len(versions))
	seen := make(map[string]struct{}, len(versions))
	for _, version := range versions {
		version.RuleID = strings.TrimSpace(version.RuleID)
		version.Version = strings.TrimSpace(version.Version)
		if version.RuleID == "" || version.Version == "" {
			return nil, ErrReportInputInvalid
		}
		if _, exists := seen[version.RuleID]; exists {
			return nil, ErrReportInputInvalid
		}
		seen[version.RuleID] = struct{}{}
		normalized = append(normalized, version)
	}
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].RuleID < normalized[right].RuleID
	})
	return normalized, nil
}

func reportAssetChange(
	difference domain.AssetDifference,
	policy RedactionPolicy,
) (ReportAssetChange, error) {
	var asset *domain.Asset
	switch difference.Kind {
	case domain.AssetDifferenceAdded:
		if difference.Before != nil || difference.After == nil ||
			len(difference.ChangedAttributes) != 0 {
			return ReportAssetChange{}, ErrReportInputInvalid
		}
		asset = difference.After
	case domain.AssetDifferenceRemoved:
		if difference.Before == nil || difference.After != nil ||
			len(difference.ChangedAttributes) != 0 {
			return ReportAssetChange{}, ErrReportInputInvalid
		}
		asset = difference.Before
	case domain.AssetDifferenceChanged:
		if difference.Before == nil || difference.After == nil ||
			len(difference.ChangedAttributes) == 0 ||
			difference.Before.Category != difference.After.Category ||
			strings.TrimSpace(difference.Before.Identifier) !=
				strings.TrimSpace(difference.After.Identifier) {
			return ReportAssetChange{}, ErrReportInputInvalid
		}
		if err := difference.Before.Validate(); err != nil {
			return ReportAssetChange{}, ErrReportInputInvalid
		}
		asset = difference.After
	default:
		return ReportAssetChange{}, ErrReportInputInvalid
	}
	if err := asset.Validate(); err != nil {
		return ReportAssetChange{}, ErrReportInputInvalid
	}

	change := ReportAssetChange{Kind: difference.Kind, PresenceStatus: difference.PresenceStatus, Category: asset.Category}
	if policy.ObjectDetails != ObjectDetailOmit {
		change.DisplayName = truncateText(asset.DisplayName, policy.MaximumTextLength)
	}
	if policy.ObjectDetails == ObjectDetailFull {
		change.Identifier = truncateText(asset.Identifier, policy.MaximumTextLength)
	}
	if len(difference.ChangedAttributes) > 0 {
		seen := make(map[string]struct{}, len(difference.ChangedAttributes))
		for _, attribute := range difference.ChangedAttributes {
			attribute = strings.TrimSpace(attribute)
			if attribute == "" {
				return ReportAssetChange{}, ErrReportInputInvalid
			}
			if _, exists := seen[attribute]; exists {
				return ReportAssetChange{}, ErrReportInputInvalid
			}
			seen[attribute] = struct{}{}
			change.ChangedAttributes = append(
				change.ChangedAttributes,
				truncateText(attribute, policy.MaximumTextLength),
			)
		}
		sort.Strings(change.ChangedAttributes)
	}
	return change, nil
}

func normalizeReportOptions(options ReportOptions) (ReportOptions, error) {
	if options.MaximumEventCount == 0 {
		options.MaximumEventCount = defaultReportEventLimit
	}
	if options.MaximumEventCount < 0 || options.MaximumEventCount > maximumReportEventLimit {
		return ReportOptions{}, ErrReportInputInvalid
	}
	if options.Redaction.ObjectDetails == "" {
		options.Redaction.ObjectDetails = ObjectDetailBasename
	}
	switch options.Redaction.ObjectDetails {
	case ObjectDetailOmit, ObjectDetailBasename, ObjectDetailFull:
	default:
		return ReportOptions{}, ErrReportInputInvalid
	}
	if options.Redaction.MaximumTextLength == 0 {
		options.Redaction.MaximumTextLength = defaultReportTextLimit
	}
	if options.Redaction.MaximumTextLength < 1 || options.Redaction.MaximumTextLength > maximumReportTextLimit {
		return ReportOptions{}, ErrReportInputInvalid
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	options.SoftwareVersion = strings.TrimSpace(options.SoftwareVersion)
	if options.SoftwareVersion == "" {
		options.SoftwareVersion = buildinfo.ProductVersion()
	}
	return options, nil
}

func reportFinding(finding risk.Finding, policy RedactionPolicy) ReportFinding {
	status := finding.Status
	if status == "" {
		status = risk.FindingStatusPendingReview
	}
	reported := ReportFinding{
		ID: finding.ID, RuleID: finding.RuleID,
		Title:   truncateText(finding.Title, policy.MaximumTextLength),
		Summary: truncateText(finding.Summary, policy.MaximumTextLength),
		Level:   finding.Level, Score: finding.Score, Confidence: finding.Confidence, Status: status,
		FirstObservedUTC: finding.FirstObservedUTC, LastObservedUTC: finding.LastObservedUTC,
		Evidence: make([]ReportEvidence, 0, len(finding.Evidence)),
		Tags:     append([]string(nil), finding.Tags...),
	}
	for _, evidence := range finding.Evidence {
		reported.Evidence = append(reported.Evidence, ReportEvidence{
			EventID: evidence.EventID, Sequence: evidence.Sequence, ObservedUTC: evidence.ObservedUTC,
			Category: evidence.Category, Action: evidence.Action,
			ObjectKey: redactObjectKey(evidence.Category, evidence.ObjectKey, policy),
		})
	}
	return reported
}

func reportEvent(record storage.EventRecord, policy RedactionPolicy) (ReportEvent, error) {
	event := record.Event
	reported := ReportEvent{
		EventID: event.EventID, Sequence: event.Sequence, Category: event.Category,
		Action: truncateText(event.Action, policy.MaximumTextLength), Severity: event.Severity,
		ObservedUTC: event.ObservedUTC,
		ObjectKey:   redactObjectKey(event.Category, event.ObjectKey, policy),
		Source:      truncateText(event.Source, policy.MaximumTextLength), Confidence: event.Confidence,
	}
	if policy.IncludeProcessKey {
		reported.ProcessKey = truncateText(event.ProcessKey, policy.MaximumTextLength)
	}
	if policy.IncludePayload && len(record.Payload) > 0 {
		if !json.Valid(record.Payload) {
			return ReportEvent{}, ErrReportInputInvalid
		}
		redacted, err := redactPayload(record.Payload, policy)
		if err != nil {
			return ReportEvent{}, ErrReportInputInvalid
		}
		reported.Payload = redacted
	}
	return reported, nil
}

func redactPayload(payload []byte, policy RedactionPolicy) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	redactPayloadValue(value, policy)
	return json.Marshal(value)
}

func redactPayloadValue(value any, policy RedactionPolicy) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.TrimSpace(key))
			switch {
			case !policy.IncludeUsernames && (normalized == "username" || normalized == "user" || normalized == "domainname"):
				current[key] = "[redacted]"
			case !policy.IncludeWindowTitles && normalized == "windowtitle":
				current[key] = "[redacted]"
			case strings.HasSuffix(normalized, "path") || normalized == "objectname":
				if text, ok := child.(string); ok && policy.ObjectDetails != ObjectDetailFull {
					if policy.ObjectDetails == ObjectDetailBasename {
						current[key] = filepath.Base(strings.ReplaceAll(text, "\\", "/"))
					} else {
						current[key] = "[redacted]"
					}
				}
			default:
				redactPayloadValue(child, policy)
			}
		}
	case []any:
		for _, child := range current {
			redactPayloadValue(child, policy)
		}
	}
}

func redactObjectKey(category domain.EventCategory, value string, policy RedactionPolicy) string {
	value = strings.TrimSpace(value)
	if value == "" || policy.ObjectDetails == ObjectDetailOmit {
		return ""
	}
	if policy.ObjectDetails == ObjectDetailFull {
		return truncateText(value, policy.MaximumTextLength)
	}
	switch category {
	case domain.EventCategoryFile, domain.EventCategoryProcess:
		normalized := strings.ReplaceAll(value, "/", "\\")
		if separator := strings.LastIndex(normalized, "\\"); separator >= 0 {
			value = normalized[separator+1:]
		}
		return truncateText(value, policy.MaximumTextLength)
	case domain.EventCategorySoftware, domain.EventCategorySystem:
		return "[registry detail redacted]"
	case domain.EventCategoryDevice:
		return "[device identifier redacted]"
	default:
		return ""
	}
}

func truncateText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
