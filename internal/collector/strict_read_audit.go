package collector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const defaultStrictReadAuditInterval = 2 * time.Second

var queryStrictAuditPolicy = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "auditpol.exe", "/get", "/subcategory:"+fileSystemAuditPolicySubcategory).CombinedOutput()
}

type SecurityObjectAccessRecord struct {
	ID          string
	ObservedUTC time.Time
	ObjectPath  string
	ProcessPath string
	ProcessID   uint32
	AccessMask  string
	UserSID     string
	UserName    string
	DomainName  string
	LogonID     string
}

type securityObjectAccessQuery func(context.Context, time.Time) ([]SecurityObjectAccessRecord, error)
type strictAuditPreparation func(context.Context, []domain.MonitoringTarget) (func() error, error)

type StrictReadAuditCollector struct {
	targets      []domain.MonitoringTarget
	exclusions   []domain.MonitoringExclusion
	query        securityObjectAccessQuery
	interval     time.Duration
	now          func() time.Time
	seen         map[string]struct{}
	verifyPolicy bool
	prepare      strictAuditPreparation
}

func NewStrictReadAuditCollector(
	targets []domain.MonitoringTarget,
	exclusions []domain.MonitoringExclusion,
) (*StrictReadAuditCollector, error) {
	collector, err := newStrictReadAuditCollector(targets, exclusions, querySecurityObjectAccessEvents, defaultStrictReadAuditInterval, time.Now)
	if err != nil {
		return nil, err
	}
	collector.verifyPolicy = true
	collector.prepare = configureStrictReadAuditTargets
	return collector, nil
}

func newStrictReadAuditCollector(
	targets []domain.MonitoringTarget,
	exclusions []domain.MonitoringExclusion,
	query securityObjectAccessQuery,
	interval time.Duration,
	now func() time.Time,
) (*StrictReadAuditCollector, error) {
	if err := domain.ValidateMonitoringTargets(targets); err != nil {
		return nil, err
	}
	if err := domain.ValidateMonitoringExclusions(exclusions); err != nil {
		return nil, err
	}
	if query == nil || now == nil || interval <= 0 {
		return nil, errors.New("strict read audit collector dependency is invalid")
	}
	return &StrictReadAuditCollector{
		targets: domain.CloneMonitoringTargets(targets), exclusions: append([]domain.MonitoringExclusion(nil), exclusions...),
		query: query, interval: interval, now: now, seen: make(map[string]struct{}),
	}, nil
}

func (*StrictReadAuditCollector) Name() string { return "windows_security_object_access" }

func (collector *StrictReadAuditCollector) Run(ctx context.Context, sink Sink) (runErr error) {
	if collector.prepare != nil {
		cleanup, err := collector.prepare(ctx, collector.targets)
		if err != nil {
			_ = collector.emitUnavailable(ctx, sink, err)
			return fmt.Errorf("prepare Windows object access auditing: %w", err)
		}
		if cleanup != nil {
			defer func() {
				if err := cleanup(); err != nil {
					runErr = errors.Join(runErr, fmt.Errorf("restore Windows object access auditing: %w", err))
				}
			}()
		}
	}
	if collector.verifyPolicy {
		if err := verifyFileSystemObjectAccessAudit(ctx); err != nil {
			_ = collector.emitUnavailable(ctx, sink, err)
			return err
		}
	}
	lastQuery := collector.now().UTC().Add(-collector.interval)
	for {
		records, err := collector.query(ctx, lastQuery)
		if err != nil {
			_ = collector.emitUnavailable(ctx, sink, err)
			return fmt.Errorf("query Windows object access audit: %w", err)
		}
		for _, record := range records {
			if err := collector.emit(ctx, sink, record); err != nil {
				return err
			}
		}
		lastQuery = collector.now().UTC()
		timer := time.NewTimer(collector.interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (collector *StrictReadAuditCollector) emitUnavailable(ctx context.Context, sink Sink, cause error) error {
	payload, _ := json.Marshal(struct {
		Reason string `json:"reason"`
		Error  string `json:"error"`
	}{Reason: strictAuditUnavailableReason(cause), Error: cause.Error()})
	return sink.Emit(ctx, Observation{
		Category: domain.EventCategoryHealth, Action: "strict_read_audit_unavailable", Severity: domain.EventSeverityHigh,
		ObservedUTC: collector.now().UTC(), Source: collector.Name(), Confidence: domain.EventConfidenceDirect,
		Payload: payload,
	})
}

func verifyFileSystemObjectAccessAudit(ctx context.Context) error {
	output, err := queryStrictAuditPolicy(ctx)
	if err != nil {
		return fmt.Errorf("read File System audit policy: %w", err)
	}
	if err := fileSystemAuditPolicyEnabled(string(output)); err != nil {
		return err
	}
	return nil
}

func fileSystemAuditPolicyEnabled(output string) error {
	state, err := parseFileSystemAuditPolicyState(output)
	if err != nil {
		return err
	}
	if !state.Success {
		return errors.New("File System object access audit is disabled")
	}
	return nil
}

func strictAuditUnavailableReason(cause error) string {
	if cause == nil {
		return "unknown"
	}
	message := strings.ToLower(cause.Error())
	switch {
	case strings.Contains(message, "access is denied") || strings.Contains(message, "access denied") || strings.Contains(message, "拒绝访问"):
		return "security_log_access_denied"
	case strings.Contains(message, "no auditing") || strings.Contains(message, "无审核") ||
		strings.Contains(message, "audit is disabled"):
		return "file_system_audit_disabled"
	case strings.Contains(message, "auditpol") || strings.Contains(message, "executable file not found"):
		return "audit_policy_tool_unavailable"
	case strings.Contains(message, "security event log") || strings.Contains(message, "specified channel"):
		return "security_event_log_unavailable"
	default:
		return "security_object_access_audit_unavailable"
	}
}

func (collector *StrictReadAuditCollector) emit(ctx context.Context, sink Sink, record SecurityObjectAccessRecord) error {
	if record.ID == "" || record.ObservedUTC.IsZero() || strings.TrimSpace(record.ObjectPath) == "" {
		return nil
	}
	if _, exists := collector.seen[record.ID]; exists {
		return nil
	}
	collector.seen[record.ID] = struct{}{}
	if len(collector.seen) > 10_000 {
		collector.seen = map[string]struct{}{record.ID: {}}
	}
	if !strictReadAuditTargetMatches(collector.targets, record.ObjectPath) ||
		!isReadOrOpenAccess(record.AccessMask) ||
		domain.MatchesMonitoringExclusion(collector.exclusions, record.ObjectPath, record.ProcessPath) {
		return nil
	}
	metadata := inspectProcessImage(record.ProcessPath)
	payload, err := json.Marshal(struct {
		ObjectPath       string `json:"objectPath"`
		ProcessPath      string `json:"processPath,omitempty"`
		ProcessID        uint32 `json:"processId,omitempty"`
		ProcessSHA256    string `json:"processSha256,omitempty"`
		ProcessPublisher string `json:"processPublisher,omitempty"`
		SignatureStatus  string `json:"signatureStatus,omitempty"`
		AccessMask       string `json:"accessMask"`
		EventID          string `json:"eventId"`
		UserSID          string `json:"userSid,omitempty"`
		UserName         string `json:"userName,omitempty"`
		DomainName       string `json:"domainName,omitempty"`
		LogonID          string `json:"logonId,omitempty"`
	}{
		ObjectPath: record.ObjectPath, ProcessPath: record.ProcessPath, ProcessID: record.ProcessID,
		ProcessSHA256: metadata.sha256, ProcessPublisher: metadata.publisher, SignatureStatus: metadata.signatureStatus,
		AccessMask: record.AccessMask, EventID: record.ID, UserSID: record.UserSID, UserName: record.UserName,
		DomainName: record.DomainName, LogonID: record.LogonID,
	})
	if err != nil {
		return fmt.Errorf("encode strict read audit observation: %w", err)
	}
	processKey := ""
	if record.ProcessID != 0 {
		processKey = strconv.FormatUint(uint64(record.ProcessID), 10)
	}
	var userSIDHash []byte
	if record.UserSID != "" {
		digest := sha256.Sum256([]byte(record.UserSID))
		userSIDHash = digest[:]
	}
	return sink.Emit(ctx, Observation{
		Category: domain.EventCategoryFile, Action: "file_read_or_opened", Severity: domain.EventSeverityLow,
		ObservedUTC: record.ObservedUTC.UTC(), MonotonicTicks: record.ObservedUTC.UnixNano(),
		UserSIDHash: userSIDHash, ProcessKey: processKey, ObjectKey: record.ObjectPath, Source: collector.Name(),
		Confidence: domain.EventConfidenceDirect, Payload: payload,
	})
}

func isReadOrOpenAccess(accessMask string) bool {
	value, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(accessMask), "0x"), 16, 32)
	if err != nil {
		return false
	}
	const readDataOrOpenMask = 0x00000001 | 0x00000008 | 0x00000080 | 0x00020000
	return value&readDataOrOpenMask != 0
}

func strictReadAuditTargetMatches(targets []domain.MonitoringTarget, path string) bool {
	path = strings.ToLower(filepath.Clean(path))
	for _, target := range targets {
		root := strings.ToLower(filepath.Clean(target.Path))
		switch target.Kind {
		case domain.MonitoringTargetKindFile:
			if path == root {
				return true
			}
		case domain.MonitoringTargetKindDirectory:
			if path == root || strings.HasPrefix(path, strings.TrimRight(root, `\\`)+`\`) {
				return target.Recursive || filepath.Dir(path) == root
			}
		case domain.MonitoringTargetKindRemovableVolume:
			if path == root || strings.HasPrefix(path, strings.TrimRight(root, `\\`)+`\`) {
				return true
			}
		}
	}
	return false
}

func querySecurityObjectAccessEvents(ctx context.Context, since time.Time) ([]SecurityObjectAccessRecord, error) {
	milliseconds := time.Since(since).Milliseconds() + 2_000
	if milliseconds < 2_000 {
		milliseconds = 2_000
	}
	query := fmt.Sprintf("*[System[(EventID=4663) and TimeCreated[timediff(@SystemTime) <= %d]]]", milliseconds)
	command := exec.CommandContext(ctx, "wevtutil.exe", "qe", "Security", "/q:"+query, "/f:RenderedXml", "/rd:true", "/c:256")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read Security event log: %w", err)
	}
	return parseSecurityObjectAccessEvents(output)
}

type securityEventXML struct {
	System struct {
		RecordID string `xml:"EventRecordID"`
		Created  struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
	} `xml:"System"`
	Data struct {
		Entries []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"Data"`
	} `xml:"EventData"`
}

func parseSecurityObjectAccessEvents(output []byte) ([]SecurityObjectAccessRecord, error) {
	decoder := xml.NewDecoder(bytes.NewReader(output))
	records := []SecurityObjectAccessRecord{}
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return records, nil
			}
			return nil, fmt.Errorf("decode Security event XML: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Event" {
			continue
		}
		var event securityEventXML
		if err := decoder.DecodeElement(&event, &start); err != nil {
			return nil, fmt.Errorf("decode Security event: %w", err)
		}
		observedUTC, err := time.Parse(time.RFC3339Nano, event.System.Created.SystemTime)
		if err != nil {
			continue
		}
		record := SecurityObjectAccessRecord{ID: strings.TrimSpace(event.System.RecordID), ObservedUTC: observedUTC.UTC()}
		for _, entry := range event.Data.Entries {
			switch entry.Name {
			case "SubjectUserSid":
				record.UserSID = strings.TrimSpace(entry.Value)
			case "SubjectUserName":
				record.UserName = strings.TrimSpace(entry.Value)
			case "SubjectDomainName":
				record.DomainName = strings.TrimSpace(entry.Value)
			case "SubjectLogonId":
				record.LogonID = strings.TrimSpace(entry.Value)
			case "ObjectName":
				record.ObjectPath = strings.TrimSpace(entry.Value)
			case "ProcessName":
				record.ProcessPath = strings.TrimSpace(entry.Value)
			case "ProcessId":
				value, parseErr := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(entry.Value), "0x"), 16, 32)
				if parseErr == nil {
					record.ProcessID = uint32(value)
				}
			case "AccessMask":
				record.AccessMask = strings.TrimSpace(entry.Value)
			}
		}
		if record.ID != "" && record.ObjectPath != "" {
			records = append(records, record)
		}
	}
}
