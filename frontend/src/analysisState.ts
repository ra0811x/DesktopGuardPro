import { computed, reactive, ref } from "vue";

import { Bridge } from "../bindings/desktopguardpro/internal/appbridge";
import type { EventCategory, EventSeverity } from "../bindings/desktopguardpro/internal/domain";
import type { RedactionPolicy } from "../bindings/desktopguardpro/internal/reporting";
import type { Evaluation, FindingStatus } from "../bindings/desktopguardpro/internal/risk";
import {
  ReportExportRequest,
  type ReportFormat,
  type ReportResult,
	RiskFindingStatusUpdateRequest,
	type RiskFindingStatusResult,
  TimelineQueryRequest,
} from "../bindings/desktopguardpro/internal/service";
import type {
  EventRecord,
  TimelinePage,
} from "../bindings/desktopguardpro/internal/storage";

type CancellableOperation<T> = Promise<T> & {
  cancel?: (cause?: unknown) => void | PromiseLike<void>;
};

export interface AnalysisClient {
  QueryTimeline(request: TimelineQueryRequest): CancellableOperation<TimelinePage>;
  EvaluateRisk(sessionID: string): CancellableOperation<Evaluation>;
	UpdateRiskFindingStatus(request: RiskFindingStatusUpdateRequest): CancellableOperation<RiskFindingStatusResult>;
  ExportReport(request: ReportExportRequest): CancellableOperation<ReportResult>;
}

export interface AnalysisFilters {
  categories: EventCategory[];
	severities: EventSeverity[];
	user: string;
	process: string;
	path: string;
  fromUtc: string;
  toUtc: string;
}

export interface ReportSequenceRange {
  fromSequence?: number;
  toSequence?: number;
}

const timelinePageSize = 100;

export function useAnalysisState(client: AnalysisClient = Bridge) {
  const sessionID = ref("");
  const timeline = ref<EventRecord[]>([]);
  const nextCursor = ref("");
  const hasMore = ref(false);
  const integrityVerified = ref(false);
  const evaluation = ref<Evaluation | null>(null);
  const errorMessage = ref("");
  const loadingTimeline = ref(false);
  const loadingRisk = ref(false);
  const exporting = ref(false);
	const updatingFindingID = ref("");
  const filters = reactive<AnalysisFilters>({
    categories: [],
	severities: [],
	user: "",
	process: "",
	path: "",
    fromUtc: "",
    toUtc: "",
  });
	let appliedFilters: AnalysisFilters = snapshotFilters();

  const activeOperations = new Set<CancellableOperation<unknown>>();
  let generation = 0;

  const busy = computed(
    () => loadingTimeline.value || loadingRisk.value || exporting.value,
  );

  async function refresh(nextSessionID: string): Promise<void> {
    const normalizedSessionID = nextSessionID.trim();
    const currentGeneration = ++generation;
		appliedFilters = snapshotFilters();
    cancelActiveOperations("analysis refreshed");
    resetResults(normalizedSessionID);
    if (!normalizedSessionID) return;

    loadingTimeline.value = true;
    loadingRisk.value = true;
    const timelineOperation = client.QueryTimeline(timelineRequest(normalizedSessionID, "", appliedFilters));
    const riskOperation = client.EvaluateRisk(normalizedSessionID);
    const [timelineResult, riskResult] = await Promise.allSettled([
      track(timelineOperation),
      track(riskOperation),
    ]);
    if (currentGeneration !== generation) return;

    loadingTimeline.value = false;
    loadingRisk.value = false;
    const errors: string[] = [];
    if (timelineResult.status === "fulfilled") {
      applyTimelinePage(timelineResult.value, false);
    } else {
      errors.push(friendlyAnalysisError(timelineResult.reason));
    }
    if (riskResult.status === "fulfilled") {
      evaluation.value = riskResult.value;
    } else {
      errors.push(friendlyAnalysisError(riskResult.reason));
    }
    errorMessage.value = [...new Set(errors)].join("；");
  }

  async function loadMore(): Promise<void> {
    if (!sessionID.value || !hasMore.value || loadingTimeline.value) return;
    const currentGeneration = generation;
    loadingTimeline.value = true;
    errorMessage.value = "";
    try {
      const page = await track(client.QueryTimeline(timelineRequest(sessionID.value, nextCursor.value, appliedFilters)));
      if (currentGeneration !== generation) return;
      applyTimelinePage(page, true);
    } catch (error) {
      if (currentGeneration === generation) {
        errorMessage.value = friendlyAnalysisError(error);
      }
    } finally {
      if (currentGeneration === generation) loadingTimeline.value = false;
    }
  }

  async function exportReport(
    format: ReportFormat,
    redaction: RedactionPolicy,
    range: ReportSequenceRange = {},
  ): Promise<ReportResult> {
    if (!sessionID.value) throw new Error("当前没有可导出的保护会话");
    exporting.value = true;
    errorMessage.value = "";
    try {
      return await track(client.ExportReport(new ReportExportRequest({
        sessionId: sessionID.value,
        format,
        redaction,
        fromSequence: range.fromSequence,
        toSequence: range.toSequence,
      })));
    } catch (error) {
      errorMessage.value = friendlyAnalysisError(error);
      throw error;
    } finally {
      exporting.value = false;
    }
  }

	async function updateFindingStatus(findingID: string, status: FindingStatus): Promise<void> {
		const normalizedFindingID = findingID.trim();
		if (!sessionID.value || !normalizedFindingID || updatingFindingID.value) return;
		const currentGeneration = generation;
		updatingFindingID.value = normalizedFindingID;
		errorMessage.value = "";
		try {
			await track(client.UpdateRiskFindingStatus(new RiskFindingStatusUpdateRequest({
				sessionId: sessionID.value,
				findingId: normalizedFindingID,
				status,
			})));
			const nextEvaluation = await track(client.EvaluateRisk(sessionID.value));
			if (currentGeneration === generation) evaluation.value = nextEvaluation;
		} catch (error) {
			if (currentGeneration === generation) errorMessage.value = friendlyAnalysisError(error);
			throw error;
		} finally {
			if (currentGeneration === generation) updatingFindingID.value = "";
		}
	}

  function stop(): void {
    generation++;
    cancelActiveOperations("analysis stopped");
    loadingTimeline.value = false;
    loadingRisk.value = false;
    exporting.value = false;
		updatingFindingID.value = "";
  }

  function timelineRequest(
		currentSessionID: string,
		cursor: string,
		currentFilters: AnalysisFilters,
	): TimelineQueryRequest {
    return new TimelineQueryRequest({
      sessionId: currentSessionID,
      cursor: cursor || undefined,
      limit: timelinePageSize,
      categories: currentFilters.categories.length ? [...currentFilters.categories] : undefined,
	  severities: currentFilters.severities.length ? [...currentFilters.severities] : undefined,
	  user: currentFilters.user.trim() || undefined,
	  process: currentFilters.process.trim() || undefined,
	  path: currentFilters.path.trim() || undefined,
      fromUtc: currentFilters.fromUtc || undefined,
      toUtc: currentFilters.toUtc || undefined,
    });
  }

	function snapshotFilters(): AnalysisFilters {
		return {
			categories: [...filters.categories],
			severities: [...filters.severities],
			user: filters.user,
			process: filters.process,
			path: filters.path,
			fromUtc: filters.fromUtc,
			toUtc: filters.toUtc,
		};
	}

  function applyTimelinePage(page: TimelinePage, append: boolean): void {
    timeline.value = append ? [...timeline.value, ...page.records] : [...page.records];
    nextCursor.value = page.nextCursor ?? "";
    hasMore.value = page.hasMore;
    integrityVerified.value = page.integrityVerified;
  }

  function resetResults(nextSessionID: string): void {
    sessionID.value = nextSessionID;
    timeline.value = [];
    nextCursor.value = "";
    hasMore.value = false;
    integrityVerified.value = false;
    evaluation.value = null;
    errorMessage.value = "";
  }

  async function track<T>(operation: CancellableOperation<T>): Promise<T> {
    activeOperations.add(operation);
    try {
      return await operation;
    } finally {
      activeOperations.delete(operation);
    }
  }

  function cancelActiveOperations(reason: string): void {
    for (const operation of activeOperations) {
      if (operation.cancel) void operation.cancel(new Error(reason));
    }
    activeOperations.clear();
  }

  return {
    busy,
    errorMessage,
    evaluation,
    exporting,
    filters,
    hasMore,
    integrityVerified,
    loadingRisk,
    loadingTimeline,
    sessionID,
    timeline,
		updatingFindingID,
    exportReport,
    loadMore,
    refresh,
		updateFindingStatus,
    stop,
  };
}

function friendlyAnalysisError(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  if (message.includes("report_too_large")) return "单次报告最多 100000 条事件，原始负载最多 32 MiB；请填写较小的起止序列范围分批导出。";
  if (message.includes("service error")) {
    const separator = message.indexOf(":");
    return separator >= 0 ? message.slice(separator + 1).trim() : message;
  }
  if (message.toLowerCase().includes("cancel")) return "分析请求已取消";
  return "无法读取会话分析结果";
}
