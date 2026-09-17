import { describe, expect, it, vi } from "vitest";

import { EventCategory, EventSeverity } from "../bindings/desktopguardpro/internal/domain";
import {
  ObjectDetailMode,
  RedactionPolicy,
} from "../bindings/desktopguardpro/internal/reporting";
import { Evaluation } from "../bindings/desktopguardpro/internal/risk";
import { FindingStatus } from "../bindings/desktopguardpro/internal/risk";
import {
  ReportFormat,
  ReportResult,
	RiskFindingStatusResult,
} from "../bindings/desktopguardpro/internal/service";
import { TimelinePage } from "../bindings/desktopguardpro/internal/storage";
import {
  type AnalysisClient,
  useAnalysisState,
} from "./analysisState";

function analysisClient(): AnalysisClient {
  return {
    QueryTimeline: vi.fn(() => Promise.resolve(new TimelinePage({
      records: [], hasMore: false, integrityVerified: true,
    }))),
    EvaluateRisk: vi.fn((sessionID: string) => Promise.resolve(new Evaluation({
      sessionId: sessionID, findings: [],
    }))),
	UpdateRiskFindingStatus: vi.fn((request) => Promise.resolve(new RiskFindingStatusResult(request))),
    ExportReport: vi.fn(() => Promise.resolve(new ReportResult({
      format: ReportFormat.ReportFormatMarkdown,
      mediaType: "text/markdown; charset=utf-8",
      content: "# report",
      generatedUtc: "2026-08-23T14:00:00Z",
    }))),
  };
}

describe("useAnalysisState", () => {
  it("loads filtered analysis and appends the next timeline page", async () => {
    const client = analysisClient();
    vi.mocked(client.QueryTimeline)
      .mockResolvedValueOnce(new TimelinePage({
        records: [{ Event: { Sequence: 1 } } as never],
        nextCursor: "cursor-1", hasMore: true, integrityVerified: true,
      }))
      .mockResolvedValueOnce(new TimelinePage({
        records: [{ Event: { Sequence: 2 } } as never],
        hasMore: false, integrityVerified: true,
      }));
    const state = useAnalysisState(client);
    state.filters.categories = [EventCategory.EventCategoryProcess];
	state.filters.severities = [EventSeverity.EventSeverityHigh];
	state.filters.user = "Raymond";
	state.filters.process = "editor.exe";
	state.filters.path = "Evidence";
    state.filters.fromUtc = "2026-08-23T10:00:00Z";

    await state.refresh("session-1");
    expect(state.timeline.value).toHaveLength(1);
    expect(state.hasMore.value).toBe(true);
    expect(client.QueryTimeline).toHaveBeenNthCalledWith(1, expect.objectContaining({
      sessionId: "session-1",
      categories: [EventCategory.EventCategoryProcess],
	  severities: [EventSeverity.EventSeverityHigh],
	  user: "Raymond",
	  process: "editor.exe",
	  path: "Evidence",
      fromUtc: "2026-08-23T10:00:00Z",
    }));

    await state.loadMore();
    expect(state.timeline.value).toHaveLength(2);
    expect(client.QueryTimeline).toHaveBeenNthCalledWith(2, expect.objectContaining({ cursor: "cursor-1" }));
  });

  it("loads more with the filters applied to the first page", async () => {
    const client = analysisClient();
    vi.mocked(client.QueryTimeline)
      .mockResolvedValueOnce(new TimelinePage({
        records: [{ Event: { Sequence: 1 } } as never],
        nextCursor: "cursor-1", hasMore: true, integrityVerified: true,
      }))
      .mockResolvedValueOnce(new TimelinePage({ records: [], hasMore: false, integrityVerified: true }));
    const state = useAnalysisState(client);
    state.filters.categories = [EventCategory.EventCategoryProcess];

    await state.refresh("session-1");
    state.filters.categories = [EventCategory.EventCategoryDevice];
    await state.loadMore();

    expect(client.QueryTimeline).toHaveBeenNthCalledWith(2, expect.objectContaining({
      cursor: "cursor-1",
      categories: [EventCategory.EventCategoryProcess],
    }));
  });

  it("ignores an older session response", async () => {
    let resolveOld: ((page: TimelinePage) => void) | undefined;
    const oldPage = new Promise<TimelinePage>((resolve) => { resolveOld = resolve; });
    const client = analysisClient();
    vi.mocked(client.QueryTimeline)
      .mockReturnValueOnce(oldPage)
      .mockResolvedValueOnce(new TimelinePage({ records: [], hasMore: false, integrityVerified: true }));
    const state = useAnalysisState(client);

    const oldRefresh = state.refresh("old-session");
    await state.refresh("new-session");
    resolveOld?.(new TimelinePage({ records: [{ Event: { Sequence: 99 } } as never] }));
    await oldRefresh;

    expect(state.sessionID.value).toBe("new-session");
    expect(state.timeline.value).toHaveLength(0);
  });

  it("builds an explicit report export request", async () => {
    const client = analysisClient();
    const state = useAnalysisState(client);
    await state.refresh("session-1");
    const redaction = new RedactionPolicy({
      objectDetails: ObjectDetailMode.ObjectDetailBasename,
      includeProcessKey: false,
      includePayload: false,
      maximumTextLength: 512,
    });

    const result = await state.exportReport(ReportFormat.ReportFormatMarkdown, redaction, { fromSequence: 10001, toSequence: 20000 });
    expect(result.content).toBe("# report");
    expect(client.ExportReport).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: "session-1",
      format: ReportFormat.ReportFormatMarkdown,
      redaction,
      fromSequence: 10001,
      toSequence: 20000,
    }));
  });

	it("persists a finding disposition and refreshes the evaluation", async () => {
		const client = analysisClient();
		vi.mocked(client.EvaluateRisk)
			.mockResolvedValueOnce(new Evaluation({ sessionId: "session-1", findings: [] }))
			.mockResolvedValueOnce(new Evaluation({
				sessionId: "session-1",
				findings: [{ id: "finding-1", sessionId: "session-1", status: FindingStatus.FindingStatusKnown } as never],
			}));
		const state = useAnalysisState(client);
		await state.refresh("session-1");

		await state.updateFindingStatus("finding-1", FindingStatus.FindingStatusKnown);

		expect(client.UpdateRiskFindingStatus).toHaveBeenCalledWith(expect.objectContaining({
			sessionId: "session-1", findingId: "finding-1", status: FindingStatus.FindingStatusKnown,
		}));
		expect(state.evaluation.value?.findings[0]?.status).toBe(FindingStatus.FindingStatusKnown);
	});
});
