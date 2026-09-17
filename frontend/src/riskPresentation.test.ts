import { describe, expect, it } from "vitest";

import {
  Evaluation,
  Finding,
  Level,
} from "../bindings/desktopguardpro/internal/risk";
import type { EventRecord } from "../bindings/desktopguardpro/internal/storage";
import {
  confidenceLabel,
  deriveAnalysisSummary,
  evidenceLabel,
	formatRawEventPayload,
  riskLevelLabel,
} from "./riskPresentation";

describe("risk presentation", () => {
  it("derives the highest risk and collection gaps", () => {
    const evaluation = new Evaluation({
      sessionId: "session-1",
      eventCount: 101,
      coverageGapCount: 1,
      findings: [
        new Finding({ level: Level.LevelMedium, evidence: [] }),
        new Finding({ level: Level.LevelCritical, evidence: [] }),
      ],
      failures: [{ ruleId: "failed", message: "failure" }],
    });
    const timeline = [
      { Event: { action: "process_started" } },
      { Event: { action: "observation_queue_overflow" } },
    ] as EventRecord[];

    expect(deriveAnalysisSummary(evaluation, timeline)).toEqual({
      eventCount: 101,
      findingCount: 2,
      highestRisk: Level.LevelCritical,
      coverageGapCount: 1,
      ruleFailureCount: 1,
    });
  });

  it("keeps session coverage when health events are filtered out or not loaded", () => {
    const evaluation = new Evaluation({ sessionId: "session-1", eventCount: 101, coverageGapCount: 1 });
    expect(deriveAnalysisSummary(evaluation, []).coverageGapCount).toBe(1);
    expect(deriveAnalysisSummary(evaluation, [{ Event: { action: "process_started" } }] as EventRecord[]).coverageGapCount).toBe(1);
    expect(deriveAnalysisSummary(null, []).coverageGapCount).toBeNull();
  });

  it("formats risk labels, confidence and evidence sequences", () => {
    expect(riskLevelLabel(Level.LevelHigh)).toBe("高");
    expect(confidenceLabel(1.4)).toBe("100%");
    expect(confidenceLabel(-1)).toBe("0%");
    expect(evidenceLabel(new Finding({
      evidence: [
        { sequence: 3 },
        { sequence: 8 },
      ] as never,
    }))).toBe("#3、#8");
  });

	it("formats JSON and base64 event payloads as readable raw fields", () => {
		expect(formatRawEventPayload('{"path":"C:\\\\Evidence\\\\report.txt","processId":42}')).toContain('"processId": 42');
		const encoded = btoa(new TextEncoder().encode('{"status":"available"}').reduce((text, byte) => text + String.fromCharCode(byte), ""));
		expect(formatRawEventPayload(encoded)).toContain('"status": "available"');
		expect(formatRawEventPayload("")).toBe("未提供原始字段");
	});
});
