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

const defaultSoftwareRepairInterval = 3 * time.Second

type SoftwareRepairRecord struct {
	ID              string    `json:"eventId"`
	ProductName     string    `json:"name"`
	ProductVersion  string    `json:"version"`
	Publisher       string    `json:"publisher"`
	InstallScope    string    `json:"installScope"`
	InstallLocation string    `json:"installLocation"`
	Status          string    `json:"status"`
	ObservedUTC     time.Time `json:"observedUtc"`
}

type softwareRepairQuery func(context.Context, time.Time) ([]SoftwareRepairRecord, error)

type SoftwareRepairCollector struct {
	query    softwareRepairQuery
	interval time.Duration
	now      func() time.Time
	seen     map[string]struct{}
}

func NewSoftwareRepairCollector() (*SoftwareRepairCollector, error) {
	return newSoftwareRepairCollector(querySoftwareRepairEvents, defaultSoftwareRepairInterval, time.Now)
}

func newSoftwareRepairCollector(query softwareRepairQuery, interval time.Duration, now func() time.Time) (*SoftwareRepairCollector, error) {
	if query == nil || now == nil || interval <= 0 {
		return nil, errors.New("software repair collector dependency is invalid")
	}
	return &SoftwareRepairCollector{query: query, interval: interval, now: now, seen: make(map[string]struct{})}, nil
}

func (*SoftwareRepairCollector) Name() string { return "windows_msi_repair_log" }

func (collector *SoftwareRepairCollector) Run(ctx context.Context, sink Sink) error {
	lastQuery := collector.now().UTC().Add(-collector.interval)
	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()
	for {
		records, err := collector.query(ctx, lastQuery)
		if err == nil {
			for _, record := range records {
				if _, exists := collector.seen[record.ID]; exists || record.ID == "" || record.ObservedUTC.IsZero() {
					continue
				}
				collector.seen[record.ID] = struct{}{}
				if record.Publisher == "" {
					record.Publisher = "unavailable"
				}
				if record.InstallScope == "" {
					record.InstallScope = "unavailable"
				}
				if record.InstallLocation == "" {
					record.InstallLocation = "unavailable"
				}
				payload, _ := json.Marshal(record)
				if err := sink.Emit(ctx, Observation{
					Category: domain.EventCategorySoftware, Action: "software_repaired", Severity: domain.EventSeverityMedium,
					ObservedUTC: record.ObservedUTC.UTC(), ObjectKey: record.ProductName, Source: collector.Name(),
					Confidence: domain.EventConfidenceDirect, Payload: payload,
				}); err != nil {
					return err
				}
			}
		} else if ctx.Err() == nil {
			payload, _ := json.Marshal(map[string]string{"error": err.Error()})
			if err := sink.Emit(ctx, Observation{Category: domain.EventCategoryHealth, Action: "software_repair_monitor_unavailable",
				Severity: domain.EventSeverityMedium, ObservedUTC: collector.now().UTC(), Source: collector.Name(),
				Confidence: domain.EventConfidenceDirect, Payload: payload}); err != nil {
				return err
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

func querySoftwareRepairEvents(ctx context.Context, since time.Time) ([]SoftwareRepairRecord, error) {
	milliseconds := time.Since(since).Milliseconds() + 3_000
	if milliseconds < 3_000 {
		milliseconds = 3_000
	}
	query := fmt.Sprintf("*[System[Provider[@Name='MsiInstaller'] and (EventID=1035) and TimeCreated[timediff(@SystemTime) <= %d]]]", milliseconds)
	output, err := exec.CommandContext(ctx, "wevtutil.exe", "qe", "Application", "/q:"+query, "/f:RenderedXml", "/rd:true", "/c:64").Output()
	if err != nil {
		return nil, fmt.Errorf("read MSI repair events: %w", err)
	}
	return parseSoftwareRepairEvents(output)
}

func parseSoftwareRepairEvents(output []byte) ([]SoftwareRepairRecord, error) {
	decoder := xml.NewDecoder(bytes.NewReader(output))
	var records []SoftwareRepairRecord
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return records, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode MSI repair XML: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Event" {
			continue
		}
		var event securityEventXML
		if err := decoder.DecodeElement(&event, &start); err != nil {
			return nil, err
		}
		observed, err := time.Parse(time.RFC3339Nano, event.System.Created.SystemTime)
		if err != nil {
			continue
		}
		record := SoftwareRepairRecord{ID: strings.TrimSpace(event.System.RecordID), ObservedUTC: observed.UTC()}
		for index, entry := range event.Data.Entries {
			value := strings.TrimSpace(entry.Value)
			switch entry.Name {
			case "ProductName":
				record.ProductName = value
			case "ProductVersion":
				record.ProductVersion = value
			case "Manufacturer":
				record.Publisher = value
			case "InstallScope":
				record.InstallScope = value
			case "InstallLocation":
				record.InstallLocation = value
			case "Status":
				record.Status = value
			default:
				switch index {
				case 0:
					record.ProductName = value
				case 1:
					record.ProductVersion = value
				case 3:
					record.Publisher = value
				case 4:
					record.Status = value
				}
			}
		}
		if record.ID != "" {
			records = append(records, record)
		}
	}
}
