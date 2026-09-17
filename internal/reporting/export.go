package reporting

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"strconv"
	"strings"
	"time"

	"desktopguardpro/internal/domain"
)

var (
	ErrExportWriterRequired        = errors.New("report export writer is required")
	ErrExportReportInvalid         = errors.New("report export input is invalid")
	ErrVerificationManifestInvalid = errors.New("report verification manifest is invalid")
)

const verificationManifestSchemaVersion = 1

type VerificationManifest struct {
	SchemaVersion      int       `json:"schemaVersion"`
	SoftwareVersion    string    `json:"softwareVersion"`
	Algorithm          string    `json:"algorithm"`
	ReportSHA256       string    `json:"reportSha256"`
	ReportSize         int       `json:"reportSize"`
	MediaType          string    `json:"mediaType"`
	SessionID          string    `json:"sessionId"`
	SessionRevision    uint64    `json:"sessionRevision"`
	ReportGeneratedUTC time.Time `json:"reportGeneratedUtc"`
	IntegrityVerified  bool      `json:"integrityVerified"`
}

func NewVerificationManifest(report Report, content []byte, mediaType string) (VerificationManifest, error) {
	if err := validateExportReport(report); err != nil || len(content) == 0 || strings.TrimSpace(mediaType) == "" {
		return VerificationManifest{}, ErrVerificationManifestInvalid
	}
	digest := sha256.Sum256(content)
	return VerificationManifest{
		SchemaVersion:   verificationManifestSchemaVersion,
		SoftwareVersion: report.SoftwareVersion,
		Algorithm:       "SHA-256", ReportSHA256: hex.EncodeToString(digest[:]), ReportSize: len(content),
		MediaType: mediaType, SessionID: report.Session.ID, SessionRevision: report.Session.Revision,
		ReportGeneratedUTC: report.GeneratedUTC, IntegrityVerified: report.Summary.IntegrityVerified,
	}, nil
}

func VerifyReportContent(manifest VerificationManifest, content []byte) error {
	_, offset := manifest.ReportGeneratedUTC.Zone()
	if manifest.SchemaVersion != verificationManifestSchemaVersion || manifest.Algorithm != "SHA-256" ||
		strings.TrimSpace(manifest.SoftwareVersion) == "" ||
		strings.TrimSpace(manifest.ReportSHA256) == "" || manifest.ReportSize != len(content) ||
		strings.TrimSpace(manifest.MediaType) == "" || strings.TrimSpace(manifest.SessionID) == "" ||
		manifest.ReportGeneratedUTC.IsZero() || offset != 0 {
		return ErrVerificationManifestInvalid
	}
	digest := sha256.Sum256(content)
	if !strings.EqualFold(manifest.ReportSHA256, hex.EncodeToString(digest[:])) {
		return ErrVerificationManifestInvalid
	}
	return nil
}

func ExportHTML(report Report, writer io.Writer) error {
	if writer == nil {
		return ErrExportWriterRequired
	}
	if err := validateExportReport(report); err != nil {
		return err
	}
	var output bytes.Buffer
	if err := htmlReportTemplate.Execute(&output, report); err != nil {
		return fmt.Errorf("render HTML report: %w", err)
	}
	if _, err := writer.Write(output.Bytes()); err != nil {
		return fmt.Errorf("write HTML report: %w", err)
	}
	return nil
}

func ExportMarkdown(report Report, writer io.Writer) error {
	if writer == nil {
		return ErrExportWriterRequired
	}
	if err := validateExportReport(report); err != nil {
		return err
	}
	var output bytes.Buffer
	writeMarkdownReport(&output, report)
	if _, err := writer.Write(output.Bytes()); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func ExportJSON(report Report, writer io.Writer) error {
	if writer == nil {
		return ErrExportWriterRequired
	}
	if err := validateExportReport(report); err != nil {
		return err
	}
	output, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode JSON report: %w", err)
	}
	if _, err := writer.Write(output); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	return nil
}

func validateExportReport(report Report) error {
	if report.SchemaVersion != reportSchemaVersion || strings.TrimSpace(report.SoftwareVersion) == "" || report.GeneratedUTC.IsZero() ||
		strings.TrimSpace(report.Session.ID) == "" || strings.TrimSpace(report.Session.Name) == "" ||
		report.Summary.SessionID != report.Session.ID || report.Summary.SessionName != report.Session.Name ||
		report.Summary.EventCount != uint64(len(report.Timeline)) ||
		report.Summary.FindingCount != uint64(len(report.Findings)) ||
		report.Summary.RuleFailureCount != uint64(len(report.RuleFailures)) {
		return ErrExportReportInvalid
	}
	_, offset := report.GeneratedUTC.Zone()
	if offset != 0 {
		return ErrExportReportInvalid
	}
	for _, change := range report.AssetChanges {
		if !validReportAssetChange(change) {
			return ErrExportReportInvalid
		}
	}
	if _, err := normalizeRuleVersions(report.RuleVersions); err != nil {
		return ErrExportReportInvalid
	}
	return nil
}

func validReportAssetChange(change ReportAssetChange) bool {
	switch change.Category {
	case domain.AssetCategorySoftware, domain.AssetCategoryDevice,
		domain.AssetCategoryNetwork, domain.AssetCategoryAccount,
		domain.AssetCategorySystem:
	default:
		return false
	}
	switch change.Kind {
	case domain.AssetDifferenceAdded, domain.AssetDifferenceRemoved:
		return len(change.ChangedAttributes) == 0
	case domain.AssetDifferenceChanged:
		if len(change.ChangedAttributes) == 0 {
			return false
		}
		for _, attribute := range change.ChangedAttributes {
			if strings.TrimSpace(attribute) == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func assetChangeKindLabel(kind domain.AssetDifferenceKind) string {
	switch kind {
	case domain.AssetDifferenceAdded:
		return "新增"
	case domain.AssetDifferenceRemoved:
		return "移除"
	case domain.AssetDifferenceChanged:
		return "变更"
	default:
		return "未知"
	}
}

var htmlReportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"timestamp": formatReportTimestamp,
	"percent": func(value float64) string {
		return strconv.FormatFloat(value*100, 'f', 0, 64) + "%"
	},
	"payload":         func(value []byte) string { return string(value) },
	"assetChangeKind": assetChangeKindLabel,
	"changedAttributes": func(values []string) string {
		return strings.Join(values, ", ")
	},
}).Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Desktop Guard Pro 保护报告</title>
<style>
body{font-family:"Microsoft YaHei",sans-serif;color:#172033;background:#fff;margin:0;padding:32px;line-height:1.6}main{max-width:1120px;margin:auto}h1,h2{color:#102a43}section{margin:28px 0}table{border-collapse:collapse;width:100%;font-size:14px}th,td{border:1px solid #ccd6e0;padding:8px;text-align:left;vertical-align:top}th{background:#eef3f8}code,pre{font-family:Consolas,monospace;white-space:pre-wrap;overflow-wrap:anywhere}.notice{border-left:4px solid #c47f00;background:#fff7df;padding:12px}.muted{color:#52606d}
</style>
</head>
<body><main>
<h1>Desktop Guard Pro 保护报告</h1>
<p>本报告汇总保护会话内已验证的事件、风险发现和采集覆盖情况。</p>
<section><h2>会话信息</h2>
<p>以下信息标识本次保护会话和报告生成时间。</p>
<table><tbody>
<tr><th>会话名称</th><td>{{.Session.Name}}</td></tr>
<tr><th>会话标识</th><td>{{.Session.ID}}</td></tr>
<tr><th>会话状态</th><td>{{.Session.State}}</td></tr>
<tr><th>软件版本</th><td>{{.SoftwareVersion}}</td></tr>
<tr><th>生成时间</th><td>{{timestamp .GeneratedUTC}}</td></tr>
<tr><th>完整性验证</th><td>{{if .Summary.IntegrityVerified}}通过{{else}}未通过{{end}}</td></tr>
{{with .Range}}<tr><th>报告范围</th><td>序列 {{.FirstSequence}}–{{.LastSequence}}；会话共 {{.SessionEventCount}} 条事件。{{if .Partial}}范围报告：风险与覆盖仅统计所选事件。{{else}}完整会话。{{end}}</td></tr>{{end}}
</tbody></table></section>
<section><h2>资产变化</h2>
<p>该表比较会话开始与结束时的软件、设备、网络、账户和系统资产。</p>
{{if .AssetChanges}}<table><thead><tr><th>变化</th><th>类别</th><th>显示名</th><th>标识</th><th>变更字段</th></tr></thead><tbody>
{{range .AssetChanges}}<tr><td>{{assetChangeKind .Kind}}</td><td>{{.Category}}</td><td>{{.DisplayName}}</td><td>{{.Identifier}}</td><td>{{changedAttributes .ChangedAttributes}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="muted">当前报告没有资产变化记录。</p>{{end}}</section>
<section><h2>会话摘要</h2>
<p>摘要显示事件规模、风险数量和采集器健康状态。</p>
<table><tbody>
<tr><th>事件数量</th><td>{{.Summary.EventCount}}</td></tr>
<tr><th>风险数量</th><td>{{.Summary.FindingCount}}</td></tr>
<tr><th>最高风险</th><td>{{.Summary.HighestRisk}}</td></tr>
<tr><th>采集健康</th><td>{{.Summary.CollectorHealth}}</td></tr>
<tr><th>覆盖缺口</th><td>{{len .Summary.CoverageGaps}}</td></tr>
</tbody></table></section>
<section><h2>风险发现</h2>
<p>风险发现用于提示需要人工复核的活动，并保留对应事件证据。</p>
{{if .Findings}}<table><thead><tr><th>等级</th><th>处置状态</th><th>标题</th><th>说明</th><th>评分</th><th>置信度</th><th>证据序列</th></tr></thead><tbody>
{{range .Findings}}<tr><td>{{.Level}}</td><td>{{.Status}}</td><td>{{.Title}}</td><td>{{.Summary}}</td><td>{{.Score}}</td><td>{{percent .Confidence}}</td><td>{{range .Evidence}}#{{.Sequence}} {{.Action}}<br>{{end}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="muted">当前报告没有风险发现。</p>{{end}}</section>
<section><h2>风险规则版本</h2>
<p>版本清单标识本报告生成时参与评估的规则实现。</p>
{{if .RuleVersions}}<table><thead><tr><th>规则标识</th><th>版本</th></tr></thead><tbody>
{{range .RuleVersions}}<tr><td>{{.RuleID}}</td><td>{{.Version}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="muted">当前报告未记录规则版本。</p>{{end}}</section>
<section><h2>事件时间线</h2>
<p>时间线按采集顺序列出报告包含的事件，敏感字段按导出策略处理。</p>
{{if .Timeline}}<table><thead><tr><th>序列</th><th>时间</th><th>类别</th><th>动作</th><th>对象</th>{{if .Redaction.IncludeProcessKey}}<th>进程关联键</th>{{end}}<th>来源</th><th>负载</th></tr></thead><tbody>
{{range .Timeline}}<tr><td>{{.Sequence}}</td><td>{{timestamp .ObservedUTC}}</td><td>{{.Category}}</td><td>{{.Action}}</td><td>{{.ObjectKey}}</td>{{if $.Redaction.IncludeProcessKey}}<td>{{.ProcessKey}}</td>{{end}}<td>{{.Source}}</td><td>{{if .Payload}}<pre>{{payload .Payload}}</pre>{{end}}</td></tr>{{end}}
</tbody></table>{{else}}<p class="muted">当前报告没有事件记录。</p>{{end}}</section>
{{if or .Summary.CoverageGaps .RuleFailures}}<section><h2>覆盖提示</h2>
<p class="notice">报告包含采集缺口或规则执行异常。复核结论时需要结合覆盖范围。</p>
</section>{{end}}
<section><h2>人工复核</h2>
<p>请结合设备使用场景复核高风险发现，并妥善保存报告和原始加密审计数据。</p>
</section>
</main></body></html>`))

func writeMarkdownReport(output *bytes.Buffer, report Report) {
	writeMarkdownLine(output, "# Desktop Guard Pro 保护报告")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "本报告汇总保护会话内已验证的事件、风险发现和采集覆盖情况。")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 会话信息")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "以下信息标识本次保护会话和报告生成时间。")
	writeMarkdownLine(output, "")
	writeMarkdownTableHeader(output, "项目", "内容")
	writeMarkdownTableRow(output, "会话名称", report.Session.Name)
	writeMarkdownTableRow(output, "会话标识", report.Session.ID)
	writeMarkdownTableRow(output, "会话状态", string(report.Session.State))
	writeMarkdownTableRow(output, "软件版本", report.SoftwareVersion)
	writeMarkdownTableRow(output, "生成时间", formatReportTimestamp(report.GeneratedUTC))
	integrity := "未通过"
	if report.Summary.IntegrityVerified {
		integrity = "通过"
	}
	writeMarkdownTableRow(output, "完整性验证", integrity)
	if report.Range != nil {
		rangeText := fmt.Sprintf("序列 %d–%d；会话共 %d 条事件。", report.Range.FirstSequence, report.Range.LastSequence, report.Range.SessionEventCount)
		if report.Range.Partial {
			rangeText += "范围报告：风险与覆盖仅统计所选事件。"
		} else {
			rangeText += "完整会话。"
		}
		writeMarkdownTableRow(output, "报告范围", rangeText)
	}

	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 会话摘要")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "摘要显示事件规模、风险数量和采集器健康状态。")
	writeMarkdownLine(output, "")
	writeMarkdownTableHeader(output, "项目", "内容")
	writeMarkdownTableRow(output, "事件数量", strconv.FormatUint(report.Summary.EventCount, 10))
	writeMarkdownTableRow(output, "风险数量", strconv.FormatUint(report.Summary.FindingCount, 10))
	writeMarkdownTableRow(output, "最高风险", string(report.Summary.HighestRisk))
	writeMarkdownTableRow(output, "采集健康", string(report.Summary.CollectorHealth))
	writeMarkdownTableRow(output, "覆盖缺口", strconv.Itoa(len(report.Summary.CoverageGaps)))

	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 资产变化")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "该表比较会话开始与结束时的软件、设备、网络、账户和系统资产。")
	writeMarkdownLine(output, "")
	if len(report.AssetChanges) == 0 {
		writeMarkdownLine(output, "当前报告没有资产变化记录。")
	} else {
		writeMarkdownTableHeader(output, "变化", "类别", "显示名", "标识", "变更字段")
		for _, change := range report.AssetChanges {
			writeMarkdownTableRow(
				output,
				assetChangeKindLabel(change.Kind),
				string(change.Category),
				change.DisplayName,
				change.Identifier,
				strings.Join(change.ChangedAttributes, ", "),
			)
		}
	}

	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 风险规则版本")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "版本清单标识本报告生成时参与评估的规则实现。")
	writeMarkdownLine(output, "")
	if len(report.RuleVersions) == 0 {
		writeMarkdownLine(output, "当前报告未记录规则版本。")
	} else {
		writeMarkdownTableHeader(output, "规则标识", "版本")
		for _, version := range report.RuleVersions {
			writeMarkdownTableRow(output, version.RuleID, version.Version)
		}
	}

	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 风险发现")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "风险发现用于提示需要人工复核的活动，并保留对应事件证据。")
	writeMarkdownLine(output, "")
	if len(report.Findings) == 0 {
		writeMarkdownLine(output, "当前报告没有风险发现。")
	} else {
		writeMarkdownTableHeader(output, "等级", "处置状态", "标题", "说明", "评分", "证据序列")
		for _, finding := range report.Findings {
			sequences := make([]string, 0, len(finding.Evidence))
			for _, evidence := range finding.Evidence {
				sequences = append(sequences, "#"+strconv.FormatUint(evidence.Sequence, 10)+" "+evidence.Action)
			}
			writeMarkdownTableRow(output, string(finding.Level), string(finding.Status), finding.Title, finding.Summary,
				strconv.Itoa(int(finding.Score)), strings.Join(sequences, "; "))
		}
	}

	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 事件时间线")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "时间线按采集顺序列出报告包含的事件，敏感字段按导出策略处理。")
	writeMarkdownLine(output, "")
	if len(report.Timeline) == 0 {
		writeMarkdownLine(output, "当前报告没有事件记录。")
	} else {
		headers := []string{"序列", "时间", "类别", "动作", "对象"}
		if report.Redaction.IncludeProcessKey {
			headers = append(headers, "进程关联键")
		}
		headers = append(headers, "来源", "负载")
		writeMarkdownTableHeader(output, headers...)
		for _, event := range report.Timeline {
			row := []string{strconv.FormatUint(event.Sequence, 10), formatReportTimestamp(event.ObservedUTC),
				string(event.Category), event.Action, event.ObjectKey}
			if report.Redaction.IncludeProcessKey {
				row = append(row, event.ProcessKey)
			}
			row = append(row, event.Source, string(event.Payload))
			writeMarkdownTableRow(output, row...)
		}
	}

	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "## 人工复核")
	writeMarkdownLine(output, "")
	writeMarkdownLine(output, "请结合设备使用场景复核高风险发现，并妥善保存报告和原始加密审计数据。")
}

func writeMarkdownTableHeader(output *bytes.Buffer, columns ...string) {
	writeMarkdownTableRow(output, columns...)
	separators := make([]string, len(columns))
	for index := range separators {
		separators[index] = "---"
	}
	writeMarkdownLine(output, "| "+strings.Join(separators, " | ")+" |")
}

func writeMarkdownTableRow(output *bytes.Buffer, columns ...string) {
	escaped := make([]string, len(columns))
	for index, column := range columns {
		escaped[index] = escapeMarkdownCell(column)
	}
	writeMarkdownLine(output, "| "+strings.Join(escaped, " | ")+" |")
}

func escapeMarkdownCell(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.NewReplacer(
		"&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\",
		"|", "\\|", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)",
		"\r", " ", "\n", " ",
	).Replace(value)
	return value
}

func writeMarkdownLine(output *bytes.Buffer, value string) {
	output.WriteString(value)
	output.WriteByte('\n')
}

func formatReportTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}
