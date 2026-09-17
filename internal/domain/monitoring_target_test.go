package domain

import (
	"errors"
	"testing"
)

func TestMonitoringTargetsValidateKindsRecursionAndDuplicates(t *testing.T) {
	targets := []MonitoringTarget{
		{Path: `C:\Evidence`, Kind: MonitoringTargetKindDirectory, Recursive: true},
		{Path: `D:\Exports\report.zip`, Kind: MonitoringTargetKindFile},
		{Path: `E:\`, Kind: MonitoringTargetKindRemovableVolume},
	}
	if err := ValidateMonitoringTargets(targets); err != nil {
		t.Fatalf("ValidateMonitoringTargets() error = %v", err)
	}

	for _, test := range []struct {
		name    string
		targets []MonitoringTarget
		want    error
	}{
		{"empty path", []MonitoringTarget{{Kind: MonitoringTargetKindDirectory}}, ErrMonitoringTargetPathRequired},
		{"unknown kind", []MonitoringTarget{{Path: `C:\Evidence`, Kind: "network"}}, ErrMonitoringTargetKind},
		{"file recursion", []MonitoringTarget{{Path: `C:\Evidence\a.txt`, Kind: MonitoringTargetKindFile, Recursive: true}}, ErrMonitoringTargetRecursion},
		{"duplicate path", []MonitoringTarget{
			{Path: `C:\Evidence`, Kind: MonitoringTargetKindDirectory},
			{Path: `c:\evidence\`, Kind: MonitoringTargetKindDirectory},
		}, ErrDuplicateMonitoringTarget},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateMonitoringTargets(test.targets); !errors.Is(err, test.want) {
				t.Fatalf("ValidateMonitoringTargets() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMonitoringTargetCloneIsIndependent(t *testing.T) {
	targets := []MonitoringTarget{{Path: `C:\Evidence`, Kind: MonitoringTargetKindDirectory, Recursive: true}}
	copyOfTargets := CloneMonitoringTargets(targets)
	copyOfTargets[0].Path = `D:\Other`
	if targets[0].Path != `C:\Evidence` {
		t.Fatalf("source targets were changed: %+v", targets)
	}
}

func TestMonitoringExclusionsValidateAndMatchTheirScope(t *testing.T) {
	rules := []MonitoringExclusion{
		{Kind: MonitoringExclusionKindPath, Pattern: `C:\Evidence\Cache`},
		{Kind: MonitoringExclusionKindFileName, Pattern: "desktop.ini"},
		{Kind: MonitoringExclusionKindExtension, Pattern: ".tmp"},
		{Kind: MonitoringExclusionKindProcess, Pattern: "backup.exe"},
	}
	if err := ValidateMonitoringExclusions(rules); err != nil {
		t.Fatalf("ValidateMonitoringExclusions() error = %v", err)
	}
	for _, test := range []struct {
		path    string
		process string
	}{
		{`C:\Evidence\Cache\index.db`, "writer.exe"},
		{`C:\Evidence\desktop.ini`, "writer.exe"},
		{`C:\Evidence\draft.tmp`, "writer.exe"},
		{`C:\Evidence\report.docx`, `C:\Tools\backup.exe`},
	} {
		if !MatchesMonitoringExclusion(rules, test.path, test.process) {
			t.Fatalf("rule did not match path=%q process=%q", test.path, test.process)
		}
	}
	if MatchesMonitoringExclusion(rules, `C:\Evidence\report.docx`, "writer.exe") {
		t.Fatal("unrelated file unexpectedly matched an exclusion")
	}
	if err := ValidateMonitoringExclusions([]MonitoringExclusion{{Kind: MonitoringExclusionKindExtension, Pattern: "tmp"}}); !errors.Is(err, ErrMonitoringExclusionPattern) {
		t.Fatalf("invalid extension error = %v", err)
	}
}
