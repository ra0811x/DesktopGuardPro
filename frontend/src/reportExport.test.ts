// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from "vitest";

import { VerificationManifest } from "../bindings/desktopguardpro/internal/reporting";
import { ReportFormat, ReportResult } from "../bindings/desktopguardpro/internal/service";
import {
  reportExtension,
  safeReportFilename,
  saveInlineReport,
  verificationManifestFilename,
} from "./reportExport";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("report export", () => {
  it("creates a bounded Windows-safe filename", () => {
    const filename = safeReportFilename(
      `../CON:<危险>|${"长".repeat(90)}`,
      ReportFormat.ReportFormatHTML,
      "2026-08-23T14:30:15Z",
    );
    expect(filename).not.toMatch(/[<>:"/\\|?*]/);
    expect(filename).not.toContain("..");
    expect(filename.endsWith(".html")).toBe(true);
    expect([...filename].length).toBeLessThan(100);
  });

  it("replaces empty and reserved device names", () => {
    expect(safeReportFilename("CON", ReportFormat.ReportFormatMarkdown, "invalid"))
      .toBe("保护会话-unknown-time.md");
    expect(reportExtension(ReportFormat.ReportFormatMarkdown)).toBe("md");
  });

  it("uses the JSON suffix for JSON reports", () => {
    expect(reportExtension(ReportFormat.ReportFormatJSON)).toBe("json");
  });

  it("uses a companion filename for the report verification manifest", () => {
    expect(verificationManifestFilename(
      "离席保护",
      ReportFormat.ReportFormatHTML,
      "2026-09-07T10:45:00Z",
    )).toBe("离席保护-2026-09-07T10-45-00-000Z.verification.json");
  });

  it("saves the report and its verification manifest together", () => {
    const createObjectURL = vi.fn(() => "blob:report");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", { createObjectURL, revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    saveInlineReport(new ReportResult({
      format: ReportFormat.ReportFormatJSON,
      mediaType: "application/json",
      content: `{"schemaVersion":1}`,
      generatedUtc: "2026-09-07T10:45:00Z",
      verification: new VerificationManifest({
        schemaVersion: 1,
        algorithm: "SHA-256",
        reportSha256: "a".repeat(64),
        reportSize: 19,
        mediaType: "application/json",
        sessionId: "session-1",
        reportGeneratedUtc: "2026-09-07T10:45:00Z",
        integrityVerified: true,
      }),
    }), "离席保护");

    expect(createObjectURL).toHaveBeenCalledTimes(2);
    expect(click).toHaveBeenCalledTimes(2);
    expect(revokeObjectURL).toHaveBeenCalledTimes(2);
  });
});
