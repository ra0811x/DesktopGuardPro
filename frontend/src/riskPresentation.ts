import type { EventRecord } from "../bindings/desktopguardpro/internal/storage";
import {
  Level,
  type Evaluation,
  type Finding,
} from "../bindings/desktopguardpro/internal/risk";

const levelLabels: Record<string, string> = {
  [Level.LevelInformational]: "提示",
  [Level.LevelLow]: "低",
  [Level.LevelMedium]: "中",
  [Level.LevelHigh]: "高",
  [Level.LevelCritical]: "严重",
};

const levelRanks: Record<string, number> = {
  [Level.LevelInformational]: 1,
  [Level.LevelLow]: 2,
  [Level.LevelMedium]: 3,
  [Level.LevelHigh]: 4,
  [Level.LevelCritical]: 5,
};

export interface AnalysisSummary {
  eventCount: number | null;
  findingCount: number;
  highestRisk: Level | null;
  coverageGapCount: number | null;
  ruleFailureCount: number;
}

export function deriveAnalysisSummary(
  evaluation: Evaluation | null,
  _timeline: EventRecord[],
): AnalysisSummary {
  let highestRisk: Level | null = null;
  for (const finding of evaluation?.findings ?? []) {
    if (levelRank(finding.level) > levelRank(highestRisk)) highestRisk = finding.level;
  }
  return {
    eventCount: evaluation?.eventCount ?? null,
    findingCount: evaluation?.findings.length ?? 0,
    highestRisk,
    coverageGapCount: evaluation?.coverageGapCount ?? null,
    ruleFailureCount: evaluation?.failures?.length ?? 0,
  };
}

export function riskLevelLabel(level: Level | null): string {
  if (!level) return "无";
  return levelLabels[level] ?? level;
}

export function riskLevelClass(level: Level | null): string {
  return level ? `risk-${level}` : "risk-none";
}

export function confidenceLabel(confidence: number): string {
  if (!Number.isFinite(confidence)) return "未知";
  return `${Math.round(Math.min(1, Math.max(0, confidence)) * 100)}%`;
}

export function evidenceLabel(finding: Finding): string {
  if (!finding.evidence.length) return "无证据引用";
  return finding.evidence.map((evidence) => `#${evidence.sequence}`).join("、");
}

export function formatRawEventPayload(payload: string): string {
	if (!payload) return "未提供原始字段";
	for (const candidate of [payload, decodeBase64(payload)]) {
		if (!candidate) continue;
		try {
			return JSON.stringify(JSON.parse(candidate), null, 2);
		} catch {
			if (candidate !== payload) return candidate;
		}
	}
	return payload;
}

function decodeBase64(value: string): string {
	try {
		const binary = atob(value);
		const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
		return new TextDecoder().decode(bytes);
	} catch {
		return "";
	}
}

function levelRank(level: Level | null): number {
  return level ? levelRanks[level] ?? 0 : 0;
}
