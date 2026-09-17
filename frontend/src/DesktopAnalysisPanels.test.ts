// @vitest-environment happy-dom

import { createApp, nextTick, type App } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../bindings/desktopguardpro/internal/risk", () => ({
  Level: {
    LevelInformational: "informational",
    LevelLow: "low",
    LevelMedium: "medium",
    LevelHigh: "high",
    LevelCritical: "critical",
  },
	FindingStatus: {
		FindingStatusKnown: "known",
		FindingStatusPendingReview: "pending_review",
		FindingStatusActionNeeded: "action_needed",
	},
}));

vi.mock("../bindings/desktopguardpro/internal/reporting", () => ({
  ObjectDetailMode: {
    ObjectDetailOmit: "omit",
    ObjectDetailBasename: "basename",
    ObjectDetailFull: "full",
  },
  RedactionPolicy: class RedactionPolicy {
    constructor(values: unknown) { Object.assign(this, values); }
  },
}));

vi.mock("../bindings/desktopguardpro/internal/service", () => ({
  ReportFormat: {
    ReportFormatHTML: "html",
    ReportFormatMarkdown: "markdown",
    ReportFormatJSON: "json",
  },
}));

import ReportExportPanel from "./ReportExportPanel.vue";
import RiskPanel from "./RiskPanel.vue";
import AssetDifferencePanel from "./AssetDifferencePanel.vue";

const mountedApps: App[] = [];

function mount(component: Parameters<typeof createApp>[0], props: Record<string, unknown>) {
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp(component, props);
  app.mount(host);
  mountedApps.push(app);
  return host;
}

afterEach(() => {
  for (const app of mountedApps.splice(0)) app.unmount();
  document.body.replaceChildren();
});

describe("desktop analysis panels", () => {
  it("splits risk findings from the summary inspector", () => {
    const host = mount(RiskPanel, { evaluation: null, timeline: [], loading: false });
    const workspace = host.querySelector<HTMLElement>(".risk-workspace");

    expect(workspace).not.toBeNull();
    expect(workspace?.querySelector(".risk-findings-pane")).not.toBeNull();
    expect(workspace?.querySelector(".risk-summary-pane[aria-label='风险摘要检查器']")).not.toBeNull();
  });

  it("selects a risk finding and resolves its loaded event evidence", async () => {
    const host = mount(RiskPanel, {
      loading: false,
      evaluation: {
        eventCount: 1,
        coverageGapCount: 0,
        findings: [{
          id: "risk-001",
          ruleId: "system-time-change",
          title: "系统时间发生变化",
          summary: "检测到时间设置被修改。",
          level: "high",
          score: 86,
          confidence: 0.92,
          evidence: [{
            eventId: "event-042",
            sequence: 42,
            observedUtc: "2026-09-06T09:15:00Z",
            category: "system",
            action: "time_changed",
            objectKey: "系统时间",
          }],
        }],
      },
      timeline: [{
        Event: {
          eventId: "event-042",
          sequence: 42,
          category: "system",
          action: "time_changed",
          objectKey: "系统时间",
          source: "registry-monitor",
          observedUtc: "2026-09-06T09:15:00Z",
        },
      }],
    });

    host.querySelector<HTMLElement>(".finding-card")?.click();
    await nextTick();

    const evidence = host.querySelector<HTMLElement>(".risk-evidence-pane");
    expect(host.querySelector(".finding-card")?.getAttribute("aria-selected")).toBe("true");
    expect(evidence?.textContent).toContain("#42");
    expect(evidence?.textContent).toContain("系统时间");
    expect(evidence?.textContent).toContain("已加载");
  });

  it("states when a finding evidence record is outside the loaded timeline page", async () => {
    const host = mount(RiskPanel, {
      loading: false,
      evaluation: {
        findings: [{
          id: "risk-002",
          ruleId: "service-stop",
          title: "审计服务停止",
          summary: "服务状态发生变化。",
          level: "high",
          score: 90,
          confidence: 1,
          evidence: [{ eventId: "event-099", sequence: 99, category: "health", action: "stopped" }],
        }],
      },
      timeline: [],
    });

    host.querySelector<HTMLElement>(".finding-card")?.click();
    await nextTick();

    expect(host.querySelector(".risk-evidence-pane")?.textContent).toContain("尚未加载");
  });

  it("keeps collection gaps and rule failures visible while findings are reviewed", () => {
    const host = mount(RiskPanel, {
      loading: false,
      timeline: [],
      evaluation: {
        eventCount: 8,
        coverageGapCount: 2,
        findings: [],
        failures: [{ ruleId: "rule-system", message: "规则输入不完整" }],
      },
    });

    expect(host.querySelector(".risk-summary-pane")?.textContent).toContain("2");
    expect(host.querySelector(".rule-failures")?.textContent).toContain("rule-system");
  });

  it("shows asset changes with category filters and detail selection", async () => {
    const onToggleCategory = vi.fn();
    const host = mount(AssetDifferencePanel, {
      loading: false,
      baselineAvailable: true,
      categories: [],
      errorMessage: "",
      emptyMessage: "",
      onToggleCategory,
      differences: [{
        kind: "changed",
        before: { category: "software", identifier: "example-tool", displayName: "Example Tool", attributes: { version: "1.0" } },
        after: { category: "software", identifier: "example-tool", displayName: "Example Tool", attributes: { version: "2.0" } },
        changedAttributes: ["version"],
      }],
    });

    expect(host.querySelector(".asset-difference-table")?.textContent).toContain("Example Tool");
    expect(host.querySelector(".asset-difference-table")?.textContent).toContain("版本");
    host.querySelector<HTMLElement>(".asset-category-filter button")?.click();
    await nextTick();
    expect(onToggleCategory).toHaveBeenCalledOnce();
    host.querySelector<HTMLElement>(".asset-difference-row")?.click();
    await nextTick();
    expect(host.querySelector(".asset-detail-pane")?.textContent).toContain("example-tool");
  });

  it("explains when a historical session has no asset baseline", () => {
    const host = mount(AssetDifferencePanel, {
      loading: false,
      baselineAvailable: false,
      categories: [],
      differences: [],
      errorMessage: "",
      emptyMessage: "该会话没有资产基线。",
    });

    expect(host.querySelector(".asset-empty")?.textContent).toContain("没有资产基线");
  });

  it("uses a property sheet and command bar for report export", () => {
    const host = mount(ReportExportPanel, { disabled: false, exporting: false, exportFeedback: "" });
    const workspace = host.querySelector<HTMLElement>(".report-workspace");

    expect(workspace).not.toBeNull();
    expect(workspace?.querySelector(".report-properties")).not.toBeNull();
    expect(workspace?.querySelector(".report-command-bar .export-button")?.textContent).toBe("生成并保存报告");
  });

  it("offers JSON as a report format and displays the selected format", async () => {
    const host = mount(ReportExportPanel, { disabled: false, exporting: false, exportFeedback: "" });
    const format = host.querySelector<HTMLSelectElement>("#report-format")!;

    expect([...format.options].map((option) => option.value)).toContain("json");
    format.value = "json";
    format.dispatchEvent(new Event("change"));
    await nextTick();

    expect(host.querySelector(".report-format-badge")?.textContent).toBe("JSON");
    expect(host.querySelector(".report-review dd")?.textContent).toBe("JSON 报告");
  });

  it("requires an explicit acknowledgement before exporting sensitive report details", async () => {
    const onExport = vi.fn();
    const host = mount(ReportExportPanel, { disabled: false, exporting: false, exportFeedback: "", onExport });
    const objectDetails = host.querySelector<HTMLSelectElement>("#report-object-details")!;
    objectDetails.value = "full";
    objectDetails.dispatchEvent(new Event("change"));
    await nextTick();

    const exportButton = host.querySelector<HTMLButtonElement>(".export-button")!;
    const acknowledgement = host.querySelector<HTMLInputElement>("#sensitive-export-acknowledgement")!;
    expect(exportButton.disabled).toBe(true);
    expect(host.querySelector(".sensitive-confirmation")?.textContent).toContain("我已确认");

    acknowledgement.click();
    await nextTick();
    expect(exportButton.disabled).toBe(false);
    exportButton.click();
    expect(onExport).toHaveBeenCalledOnce();
  });

  it("shows export progress and delivery feedback", () => {
    const inProgress = mount(ReportExportPanel, { disabled: false, exporting: true, exportFeedback: "" });
    const completed = mount(ReportExportPanel, { disabled: false, exporting: false, exportFeedback: "已发起报告下载：离席保护-2026.html" });

    expect(inProgress.querySelector(".export-button")?.textContent).toBe("正在生成报告");
    expect(completed.querySelector(".export-feedback")?.textContent).toContain("已发起报告下载");
  });

	it("lets the user persist one of the three finding review states", async () => {
		const updateStatus = vi.fn();
		const host = mount(RiskPanel, {
			loading: false,
			timeline: [],
			updatingFindingID: "",
			onUpdateFindingStatus: updateStatus,
			evaluation: {
				findings: [{
					id: "risk-003", sessionId: "session-1", ruleId: "rule-1", title: "待复核风险",
					summary: "需要用户处置。", level: "medium", score: 60, confidence: 1,
					status: "pending_review", evidence: [],
				}],
			},
		});
		host.querySelector<HTMLElement>(".finding-card")?.click();
		await nextTick();

		const knownButton = Array.from(host.querySelectorAll<HTMLButtonElement>(".finding-status-actions button"))
			.find((button) => button.textContent?.includes("已知"));
		knownButton?.click();

		expect(host.querySelector(".finding-status-actions")?.textContent).toContain("待确认");
		expect(host.querySelector(".finding-status-actions")?.textContent).toContain("需处理");
		expect(updateStatus).toHaveBeenCalledWith("risk-003", "known");
	});
});
