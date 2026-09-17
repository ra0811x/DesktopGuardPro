package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

const defaultSecurityLogInterval = 2 * time.Second

type SecurityLogClearRecord struct {
	ID          string
	ObservedUTC time.Time
	UserSID     string
	UserName    string
	DomainName  string
	LogonID     string
}

type securityLogClearQuery func(context.Context, time.Time) ([]SecurityLogClearRecord, error)

type SecurityLogCollector struct {
	query    securityLogClearQuery
	interval time.Duration
	now      func() time.Time
	seen     map[string]struct{}
}

func NewSecurityLogCollector() (*SecurityLogCollector, error) {
	return newSecurityLogCollector(querySecurityLogClearEvents, defaultSecurityLogInterval, time.Now)
}

func newSecurityLogCollector(query securityLogClearQuery, interval time.Duration, now func() time.Time) (*SecurityLogCollector, error) {
	if query == nil || now == nil || interval <= 0 {
		return nil, errors.New("security log collector dependency is invalid")
	}
	return &SecurityLogCollector{query: query, interval: interval, now: now, seen: make(map[string]struct{})}, nil
}

func (*SecurityLogCollector) Name() string { return "windows_security_log" }

func (collector *SecurityLogCollector) Run(ctx context.Context, sink Sink) error {
	lastQuery := collector.now().UTC().Add(-collector.interval)
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()
	for {
		records, err := collector.query(ctx, lastQuery)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			payload, _ := json.Marshal(struct {
				Error string `json:"error"`
			}{Error: err.Error()})
			if emitErr := sink.Emit(ctx, Observation{
				Category: domain.EventCategoryHealth, Action: "security_log_monitor_unavailable", Severity: domain.EventSeverityHigh,
				ObservedUTC: collector.now().UTC(), Source: collector.Name(), Confidence: domain.EventConfidenceDirect, Payload: payload,
			}); emitErr != nil {
				return emitErr
			}
		} else {
			for _, record := range records {
				if err := collector.emit(ctx, sink, record); err != nil {
					return err
				}
			}
		}
		lastQuery = collector.now().UTC()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (collector *SecurityLogCollector) emit(ctx context.Context, sink Sink, record SecurityLogClearRecord) error {
	if record.ID == "" || record.ObservedUTC.IsZero() {
		return nil
	}
	if _, exists := collector.seen[record.ID]; exists {
		return nil
	}
	collector.seen[record.ID] = struct{}{}
	payload, err := json.Marshal(struct {
		EventID    string `json:"eventId"`
		UserSID    string `json:"userSid,omitempty"`
		UserName   string `json:"userName,omitempty"`
		DomainName string `json:"domainName,omitempty"`
		LogonID    string `json:"logonId,omitempty"`
	}{record.ID, record.UserSID, record.UserName, record.DomainName, record.LogonID})
	if err != nil {
		return fmt.Errorf("encode security log clear observation: %w", err)
	}
	return sink.Emit(ctx, Observation{
		Category: domain.EventCategorySystem, Action: "windows_security_log_cleared", Severity: domain.EventSeverityHigh,
		ObservedUTC: record.ObservedUTC.UTC(), MonotonicTicks: record.ObservedUTC.UnixNano(), ObjectKey: "Windows Security",
		Source: collector.Name(), Confidence: domain.EventConfidenceDirect, Payload: payload,
	})
}

func querySecurityLogClearEvents(ctx context.Context, since time.Time) ([]SecurityLogClearRecord, error) {
	milliseconds := time.Since(since).Milliseconds() + 2_000
	if milliseconds < 2_000 {
		milliseconds = 2_000
	}
	query := fmt.Sprintf("*[System[(EventID=1102) and TimeCreated[timediff(@SystemTime) <= %d]]]", milliseconds)
	output, err := exec.CommandContext(ctx, "wevtutil.exe", "qe", "Security", "/q:"+query, "/f:RenderedXml", "/rd:true", "/c:64").Output()
	if err != nil {
		return nil, fmt.Errorf("read Security log clear events: %w", err)
	}
	return parseSecurityLogClearEvents(output)
}

func parseSecurityLogClearEvents(output []byte) ([]SecurityLogClearRecord, error) {
	decoder := xml.NewDecoder(bytes.NewReader(output))
	records := []SecurityLogClearRecord{}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode Security log clear XML: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Event" {
			continue
		}
		var event securityEventXML
		if err := decoder.DecodeElement(&event, &start); err != nil {
			return nil, fmt.Errorf("decode Security log clear event: %w", err)
		}
		observedUTC, err := time.Parse(time.RFC3339Nano, event.System.Created.SystemTime)
		if err != nil {
			continue
		}
		record := SecurityLogClearRecord{ID: strings.TrimSpace(event.System.RecordID), ObservedUTC: observedUTC.UTC()}
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
			}
		}
		if record.ID != "" {
			records = append(records, record)
		}
	}
}
