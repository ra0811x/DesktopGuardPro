package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"desktopguardpro/internal/domain"
)

type Evaluator struct {
	rules []Rule
}

func NewEvaluator(rules ...Rule) (*Evaluator, error) {
	seen := make(map[string]struct{}, len(rules))
	copyOfRules := append([]Rule(nil), rules...)
	for _, rule := range copyOfRules {
		if rule == nil {
			return nil, ErrRiskRuleRequired
		}
		id := strings.TrimSpace(rule.ID())
		if id == "" {
			return nil, ErrRuleIDRequired
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%w: %s", ErrRuleDuplicate, id)
		}
		seen[id] = struct{}{}
	}
	return &Evaluator{rules: copyOfRules}, nil
}

func (evaluator *Evaluator) Evaluate(ctx context.Context, sessionID string, events []Event) (Evaluation, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Evaluation{}, ErrEvaluationInvalid
	}
	ordered, evidenceByID, err := validateAndOrderEvents(sessionID, events)
	if err != nil {
		return Evaluation{}, err
	}

	result := Evaluation{
		SessionID: sessionID, Findings: make([]Finding, 0), EventCount: uint64(len(ordered)),
		RuleVersions: evaluator.ruleVersions(),
	}
	for _, event := range ordered {
		if event.AuditEvent.Category == domain.EventCategoryHealth && isCollectionGap(event.AuditEvent.Action) {
			result.CoverageGapCount++
		}
	}
	for _, rule := range evaluator.rules {
		if err := ctx.Err(); err != nil {
			return Evaluation{}, err
		}
		ruleID := strings.TrimSpace(rule.ID())
		findings, ruleErr := evaluateRuleSafely(ctx, rule, cloneEvents(ordered))
		if ruleErr == nil {
			findings, ruleErr = normalizeFindings(sessionID, ruleID, findings, evidenceByID)
		}
		if ruleErr != nil {
			if errors.Is(ruleErr, context.Canceled) || errors.Is(ruleErr, context.DeadlineExceeded) {
				return Evaluation{}, ruleErr
			}
			result.Failures = append(result.Failures, RuleFailure{RuleID: ruleID, Message: ruleErr.Error()})
			continue
		}
		result.Findings = append(result.Findings, findings...)
	}
	sortFindings(result.Findings)
	return result, nil
}

func (evaluator *Evaluator) ruleVersions() []RuleVersion {
	versions := make([]RuleVersion, 0, len(evaluator.rules))
	for _, rule := range evaluator.rules {
		versioned, ok := rule.(VersionedRule)
		if !ok {
			continue
		}
		version := strings.TrimSpace(versioned.Version())
		if version == "" {
			continue
		}
		versions = append(versions, RuleVersion{RuleID: strings.TrimSpace(rule.ID()), Version: version})
	}
	sort.Slice(versions, func(left, right int) bool {
		return versions[left].RuleID < versions[right].RuleID
	})
	return versions
}

func validateAndOrderEvents(sessionID string, events []Event) ([]Event, map[string]domainEvidence, error) {
	ordered := cloneEvents(events)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].AuditEvent.Sequence < ordered[right].AuditEvent.Sequence
	})
	evidenceByID := make(map[string]domainEvidence, len(ordered))
	var previousSequence uint64
	for index, event := range ordered {
		if err := event.AuditEvent.Validate(); err != nil || event.AuditEvent.SessionID != sessionID {
			return nil, nil, ErrEvaluationInvalid
		}
		if index > 0 && event.AuditEvent.Sequence == previousSequence {
			return nil, nil, ErrEvaluationInvalid
		}
		if _, exists := evidenceByID[event.AuditEvent.EventID]; exists {
			return nil, nil, ErrEvaluationInvalid
		}
		evidenceByID[event.AuditEvent.EventID] = domainEvidence{reference: EvidenceFromEvent(event.AuditEvent)}
		previousSequence = event.AuditEvent.Sequence
	}
	return ordered, evidenceByID, nil
}

type domainEvidence struct {
	reference EvidenceReference
}

func normalizeFindings(
	sessionID string,
	ruleID string,
	findings []Finding,
	evidenceByID map[string]domainEvidence,
) ([]Finding, error) {
	seen := make(map[string]struct{}, len(findings))
	normalized := cloneFindings(findings)
	for index := range normalized {
		finding := &normalized[index]
		finding.RuleID = ruleID
		finding.SessionID = sessionID
		finding.Key = strings.TrimSpace(finding.Key)
		identity := ruleID + "\x00" + finding.Key
		if _, exists := seen[identity]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateFinding, finding.Key)
		}
		seen[identity] = struct{}{}
		for evidenceIndex, supplied := range finding.Evidence {
			stored, exists := evidenceByID[supplied.EventID]
			if !exists || supplied.Sequence != stored.reference.Sequence {
				return nil, fmt.Errorf("%w: %s", ErrEvidenceNotFound, supplied.EventID)
			}
			finding.Evidence[evidenceIndex] = stored.reference
		}
		finding.ID = findingID(sessionID, ruleID, finding.Key)
		if finding.Status == "" {
			finding.Status = FindingStatusPendingReview
		}
		finding.Tags = normalizedTags(finding.Tags)
		if err := finding.Validate(); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

func cloneFindings(findings []Finding) []Finding {
	cloned := make([]Finding, len(findings))
	for index, finding := range findings {
		cloned[index] = finding
		cloned[index].Evidence = append([]EvidenceReference(nil), finding.Evidence...)
		cloned[index].Tags = append([]string(nil), finding.Tags...)
	}
	return cloned
}

func evaluateRuleSafely(ctx context.Context, rule Rule, events []Event) (findings []Finding, evaluationError error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			evaluationError = fmt.Errorf("risk rule panic: %v", recovered)
		}
	}()
	return rule.Evaluate(ctx, events)
}

func findingID(sessionID, ruleID, key string) string {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + ruleID + "\x00" + key))
	return hex.EncodeToString(digest[:16])
}

func normalizedTags(tags []string) []string {
	unique := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			unique[tag] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for tag := range unique {
		result = append(result, tag)
	}
	sort.Strings(result)
	return result
}

func cloneEvents(events []Event) []Event {
	cloned := make([]Event, len(events))
	for index, event := range events {
		cloned[index] = event
		cloned[index].AuditEvent.WindowsSessionID = cloneUint32(event.AuditEvent.WindowsSessionID)
		cloned[index].AuditEvent.UserSIDHash = append([]byte(nil), event.AuditEvent.UserSIDHash...)
		cloned[index].AuditEvent.EncryptedPayload = append([]byte(nil), event.AuditEvent.EncryptedPayload...)
		cloned[index].AuditEvent.PreviousHash = append([]byte(nil), event.AuditEvent.PreviousHash...)
		cloned[index].AuditEvent.EventHash = append([]byte(nil), event.AuditEvent.EventHash...)
		cloned[index].Payload = append([]byte(nil), event.Payload...)
	}
	return cloned
}

func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(left, right int) bool {
		leftRank, rightRank := levelRank(findings[left].Level), levelRank(findings[right].Level)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if findings[left].Score != findings[right].Score {
			return findings[left].Score > findings[right].Score
		}
		if !findings[left].FirstObservedUTC.Equal(findings[right].FirstObservedUTC) {
			return findings[left].FirstObservedUTC.Before(findings[right].FirstObservedUTC)
		}
		return findings[left].ID < findings[right].ID
	})
}

func levelRank(level Level) int {
	switch level {
	case LevelCritical:
		return 5
	case LevelHigh:
		return 4
	case LevelMedium:
		return 3
	case LevelLow:
		return 2
	case LevelInformational:
		return 1
	default:
		return 0
	}
}
