package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"desktopguardpro/internal/domain"
)

func TestParseSoftwareRepairEvents(t *testing.T) {
	const xml = `<Event><System><EventRecordID>91</EventRecordID><TimeCreated SystemTime="2026-09-13T03:00:00Z" /></System><EventData><Data Name="ProductName">Editor</Data><Data Name="ProductVersion">2.1</Data><Data Name="Manufacturer">Contoso</Data><Data Name="InstallScope">machine</Data><Data Name="InstallLocation">C:\Program Files\Editor</Data><Data Name="Status">0</Data></EventData></Event>`
	records, err := parseSoftwareRepairEvents([]byte(xml))
	if err != nil || len(records) != 1 || records[0].ProductName != "Editor" || records[0].Publisher != "Contoso" ||
		records[0].InstallScope != "machine" || records[0].InstallLocation != `C:\Program Files\Editor` {
		t.Fatalf("records=%#v error=%v", records, err)
	}
}

func TestSoftwareRepairCollectorEmitsRepairOnce(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC)
	collector, err := newSoftwareRepairCollector(func(context.Context, time.Time) ([]SoftwareRepairRecord, error) {
		return []SoftwareRepairRecord{{ID: "91", ProductName: "Editor", ProductVersion: "2.1", Publisher: "Contoso", InstallScope: "machine", InstallLocation: `C:\Program Files\Editor`, ObservedUTC: now}}, nil
	}, time.Millisecond, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	sink := &observationSink{}
	if err := collector.Run(ctx, sink); err != nil {
		t.Fatal(err)
	}
	observations := sink.snapshot()
	if len(observations) != 1 || observations[0].Action != "software_repaired" || observations[0].Category != domain.EventCategorySoftware {
		t.Fatalf("observations=%#v", observations)
	}
	var payload map[string]any
	if err := json.Unmarshal(observations[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "version", "publisher", "installScope", "installLocation"} {
		if payload[field] == nil || payload[field] == "" {
			t.Errorf("software repair payload field %q missing: %s", field, observations[0].Payload)
		}
	}
}
