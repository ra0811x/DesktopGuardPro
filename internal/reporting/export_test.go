package reporting

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"desktopguardpro/internal/domain"
	"desktopguardpro/internal/risk"
)

func TestExportHTMLEscapesUntrustedContent(t *testing.T) {
	report := exportTestReport(t)
	report.Session.Name = `<script>alert("session")</script>`
	report.Summary.SessionName = report.Session.Name
	report.Findings[0].Title = `<img src=x onerror=alert(1)>`
	report.Timeline[0].Action = `</td><script>alert("event")</script>`
	report.Timeline[0].Payload = []byte(`{"html":"<object data='https://example.test'>"}`)

	var output bytes.Buffer
	if err := ExportHTML(report, &output); err != nil {
		t.Fatal(err)
	}
	content := output.String()
	for _, forbidden := range []string{"<script>alert", "<img src=x", "<object data="} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("HTML output contains active content %q", forbidden)
		}
	}
	for _, required := range []string{"&lt;script&gt;", "&lt;img", "Content-Security-Policy", "default-src 'none'", "pending_review"} {
		if !strings.Contains(content, required) {
			t.Fatalf("HTML output does not contain %q", required)
		}
	}
}

func TestExportMarkdownEscapesLinksTablesAndHTML(t *testing.T) {
	report := exportTestReport(t)
	report.Session.Name = `[open](javascript:alert(1)) | <script>alert(1)</script>`
	report.Summary.SessionName = report.Session.Name
	report.Timeline[0].ObjectKey = "line1|line2\n<img src=x>"

	var output bytes.Buffer
	if err := ExportMarkdown(report, &output); err != nil {
		t.Fatal(err)
	}
	content := output.String()
	if strings.Contains(content, "[open](javascript:") || strings.Contains(content, "<script>") || strings.Contains(content, "<img") {
		t.Fatalf("Markdown output contains unsafe syntax: %s", content)
	}
	for _, required := range []string{`\[open\]\(javascript:`, `\|`, `&lt;script&gt;`, "## 人工复核"} {
		if !strings.Contains(content, required) {
			t.Fatalf("Markdown output does not contain %q", required)
		}
	}
}

func TestRangeReportsIdentifyTheirPartialCoverage(t *testing.T) {
	report := exportTestReport(t)
	report.Range = &ReportRange{FirstSequence: 21, LastSequence: 22, SessionEventCount: 100_001, Partial: true}
	for _, export := range []func(Report, io.Writer) error{ExportHTML, ExportMarkdown} {
		var output bytes.Buffer
		if err := export(report, &output); err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"21–22", "100001", "风险与覆盖仅统计所选事件"} {
			if !strings.Contains(output.String(), required) {
				t.Fatalf("missing range disclosure %q", required)
			}
		}
	}
}

func TestExportsIncludeProcessKeyOnlyWhenConfigured(t *testing.T) {
	withProcessKey := exportTestReport(t)
	withoutProcessKey := exportTestReportWithoutProcessKey(t)
	for name, export := range map[string]func(Report, io.Writer) error{
		"html":     ExportHTML,
		"markdown": ExportMarkdown,
	} {
		t.Run(name, func(t *testing.T) {
			var included bytes.Buffer
			if err := export(withProcessKey, &included); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(included.String(), "进程关联键") || !strings.Contains(included.String(), "123:456") {
				t.Fatalf("%s export did not include the process key: %s", name, included.String())
			}

			var omitted bytes.Buffer
			if err := export(withoutProcessKey, &omitted); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(omitted.String(), "123:456") {
				t.Fatalf("%s export included an omitted process key: %s", name, omitted.String())
			}
		})
	}
}

func TestExportsIncludeAssetChangesWithRedaction(t *testing.T) {
	report := exportTestReport(t)
	for name, export := range map[string]func(Report, io.Writer) error{
		"html":     ExportHTML,
		"markdown": ExportMarkdown,
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := export(report, &output); err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{
				"资产变化", "新增", "变更", "software", "Example Tool",
				"software-package-1", "version",
			} {
				if !strings.Contains(output.String(), required) {
					t.Fatalf("%s export does not contain %q", name, required)
				}
			}
		})
	}

	session, records, evaluation, summary := reportTestInputs(t)
	redacted, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now:              reportTestNow,
		AssetDifferences: reportAssetDifferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := ExportMarkdown(redacted, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "software-package-1") {
		t.Fatal("default report exposed a full asset identifier")
	}
	if !strings.Contains(output.String(), "Example Tool") {
		t.Fatal("default report omitted the redacted asset display name")
	}
}

func TestExportHTMLEscapesAssetChangeDetails(t *testing.T) {
	report := exportTestReport(t)
	report.AssetChanges[0].DisplayName = `<script>alert("asset")</script>`

	var output bytes.Buffer
	if err := ExportHTML(report, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), `<script>alert("asset")`) ||
		!strings.Contains(output.String(), `&lt;script&gt;alert`) {
		t.Fatal("HTML asset changes were not escaped")
	}
}

func TestExportJSONPreservesTheValidatedRedactedReportModel(t *testing.T) {
	report := exportTestReportWithoutProcessKey(t)
	var output bytes.Buffer
	if err := ExportJSON(report, &output); err != nil {
		t.Fatalf("ExportJSON() error = %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON report: %v", err)
	}
	if decoded.Session.ID != report.Session.ID || decoded.Summary.EventCount != report.Summary.EventCount ||
		len(decoded.AssetChanges) != len(report.AssetChanges) {
		t.Fatalf("decoded report = %#v", decoded)
	}
	if strings.Contains(output.String(), "123:456") {
		t.Fatal("JSON report exposed a redacted process key")
	}
}

func TestVerificationManifestBindsExportedReportBytes(t *testing.T) {
	report := exportTestReport(t)
	var output bytes.Buffer
	if err := ExportJSON(report, &output); err != nil {
		t.Fatal(err)
	}
	manifest, err := NewVerificationManifest(report, output.Bytes(), "application/json")
	if err != nil {
		t.Fatalf("NewVerificationManifest() error = %v", err)
	}
	if manifest.SessionID != report.Session.ID || manifest.SessionRevision != report.Session.Revision ||
		manifest.SoftwareVersion != report.SoftwareVersion || manifest.ReportSHA256 == "" ||
		manifest.ReportSize != output.Len() || !manifest.IntegrityVerified {
		t.Fatalf("manifest = %#v", manifest)
	}
	if err := VerifyReportContent(manifest, output.Bytes()); err != nil {
		t.Fatalf("VerifyReportContent() error = %v", err)
	}
	if err := VerifyReportContent(manifest, append(output.Bytes(), 'x')); !errors.Is(err, ErrVerificationManifestInvalid) {
		t.Fatalf("changed report verification error = %v", err)
	}
}

func TestExportsIncludeSoftwareVersion(t *testing.T) {
	report := exportTestReport(t)
	report.SoftwareVersion = "1.2.3"
	for name, export := range map[string]func(Report, io.Writer) error{
		"html": ExportHTML, "markdown": ExportMarkdown, "json": ExportJSON,
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := export(report, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "1.2.3") {
				t.Fatalf("%s export is missing software version", name)
			}
		})
	}
}

func TestExportsIncludeRiskRuleVersions(t *testing.T) {
	report := exportTestReport(t)
	report.RuleVersions = []risk.RuleVersion{{
		RuleID: "device.connection", Version: "2026.09.07.1",
	}}
	for name, export := range map[string]func(Report, io.Writer) error{
		"html": ExportHTML, "markdown": ExportMarkdown, "json": ExportJSON,
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := export(report, &output); err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"device.connection", "2026.09.07.1"} {
				if !strings.Contains(output.String(), required) {
					t.Fatalf("%s export is missing %q", name, required)
				}
			}
		})
	}
}

func TestBuildReportRejectsMalformedAssetDifference(t *testing.T) {
	session, records, evaluation, summary := reportTestInputs(t)
	_, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now: reportTestNow,
		AssetDifferences: []domain.AssetDifference{{
			Kind: domain.AssetDifferenceAdded,
		}},
	})
	if !errors.Is(err, ErrReportInputInvalid) {
		t.Fatalf("BuildReport() error = %v, want %v", err, ErrReportInputInvalid)
	}
}

func TestExportReturnsWriterErrorWithoutPartialStreaming(t *testing.T) {
	report := exportTestReport(t)
	want := errors.New("disk full")
	if err := ExportHTML(report, failingWriter{err: want}); !errors.Is(err, want) {
		t.Fatalf("expected writer error, got %v", err)
	}
	if err := ExportMarkdown(report, nil); !errors.Is(err, ErrExportWriterRequired) {
		t.Fatalf("expected writer required error, got %v", err)
	}
}

type failingWriter struct{ err error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.err }

func exportTestReport(t *testing.T) Report {
	t.Helper()
	session, records, evaluation, summary := reportTestInputs(t)
	report, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now:              reportTestNow,
		AssetDifferences: reportAssetDifferences(),
		Redaction: RedactionPolicy{
			ObjectDetails: ObjectDetailFull, IncludeProcessKey: true, IncludePayload: true,
			MaximumTextLength: 512,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func reportAssetDifferences() []domain.AssetDifference {
	before := domain.Asset{
		Category: domain.AssetCategorySoftware, Identifier: "software-package-1",
		DisplayName: "Example Tool", Attributes: map[string]string{"version": "1.0"},
	}
	after := before
	after.Attributes = map[string]string{"version": "2.0"}
	added := domain.Asset{
		Category: domain.AssetCategorySoftware, Identifier: "software-package-2",
		DisplayName: "Added Tool", Attributes: map[string]string{"version": "1.0"},
	}
	return []domain.AssetDifference{
		{Kind: domain.AssetDifferenceAdded, After: &added},
		{
			Kind: domain.AssetDifferenceChanged, Before: &before, After: &after,
			ChangedAttributes: []string{"version"},
		},
	}
}

func exportTestReportWithoutProcessKey(t *testing.T) Report {
	t.Helper()
	session, records, evaluation, summary := reportTestInputs(t)
	report, err := BuildReport(session, records, evaluation, summary, ReportOptions{
		Now: reportTestNow,
		Redaction: RedactionPolicy{
			ObjectDetails: ObjectDetailFull, IncludePayload: true, MaximumTextLength: 512,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}
