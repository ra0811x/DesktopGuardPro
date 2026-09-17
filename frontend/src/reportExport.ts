import { ObjectDetailMode } from "../bindings/desktopguardpro/internal/reporting";
import {
  ReportFormat,
  type ReportResult,
} from "../bindings/desktopguardpro/internal/service";

export interface ReportExportSelection {
  format: ReportFormat;
  objectDetails: ObjectDetailMode;
  includeProcessKey: boolean;
  includePayload: boolean;
}

export function reportExtension(format: ReportFormat): string {
  if (format === ReportFormat.ReportFormatHTML) return "html";
  if (format === ReportFormat.ReportFormatJSON) return "json";
  return "md";
}

export function safeReportFilename(
  sessionName: string,
  format: ReportFormat,
  generatedUtc: string,
): string {
  const normalized = sessionName
    .normalize("NFKC")
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, "-")
		.replace(/\.+/g, "-")
    .replace(/\s+/g, " ")
    .trim();
  const reserved = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/i.test(normalized);
  const base = reserved || !normalized ? "保护会话" : [...normalized].slice(0, 64).join("");
  const parsed = new Date(generatedUtc);
  const date = Number.isNaN(parsed.getTime())
    ? "unknown-time"
    : parsed.toISOString().replace(/[:.]/g, "-");
  return `${base}-${date}.${reportExtension(format)}`;
}

export function verificationManifestFilename(
  sessionName: string,
  format: ReportFormat,
  generatedUtc: string,
): string {
  return safeReportFilename(sessionName, format, generatedUtc).replace(
    /\.[^.]+$/,
    ".verification.json",
  );
}

function saveBlob(blob: Blob, filename: string): void {
  const objectURL = URL.createObjectURL(blob);
  try {
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = filename;
    link.rel = "noopener";
    link.style.display = "none";
    document.body.append(link);
    link.click();
    link.remove();
  } finally {
    URL.revokeObjectURL(objectURL);
  }
}

export function saveInlineReport(result: ReportResult, sessionName: string): void {
  if (!result.content) throw new Error("报告内容为空");
  const blob = new Blob([result.content], { type: result.mediaType });
  saveBlob(blob, safeReportFilename(sessionName, result.format, result.generatedUtc));
  if (result.verification) {
    const manifest = new Blob([JSON.stringify(result.verification, null, 2)], {
      type: "application/json",
    });
    saveBlob(manifest, verificationManifestFilename(sessionName, result.format, result.generatedUtc));
  }
}
