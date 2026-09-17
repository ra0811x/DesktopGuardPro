// @vitest-environment happy-dom

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

import { createApp, nextTick, type App as VueApp } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

const testState = vi.hoisted(() => ({
  connection: undefined as any,
  analysis: undefined as any,
  assetDifferences: undefined as any,
}));

vi.mock("../bindings/desktopguardpro/internal/domain", () => ({
	MonitoringLevel: {
		MonitoringLevelStandard: "standard",
		MonitoringLevelStrict: "strict",
	},
  SessionState: {
    SessionStateDraft: "draft",
    SessionStatePreparing: "preparing",
	SessionStateBaselineReview: "baseline_review",
    SessionStateActive: "active",
    SessionStateDegraded: "degraded",
    SessionStateFinalizing: "finalizing",
    SessionStateCompleted: "completed",
    SessionStateFailed: "failed",
  },
}));

vi.mock("./serviceConnection", async () => {
  const { ref } = await import("vue");
  testState.connection = {
    connected: ref(true),
    currentSession: ref(null),
    errorMessage: ref(""),
    phase: ref("online"),
    status: ref("采集服务降级"),
    healthStatus: ref("degraded"),
	baselineReview: ref(null),
    performProtectionAction: vi.fn(),
	previewSessionStart: vi.fn((level) => Promise.resolve({
		monitoringLevel: level,
		monitoredTargetCount: 2,
		targets: [
			{ path: "C:\\Evidence", kind: "directory", recursive: true },
			{ path: "E:\\", kind: "removable_volume", recursive: false },
		],
		impact: { requiresAdministrator: level === "strict", expectedEventVolume: "high", performanceImpact: "high", storageImpact: "high" },
	})),
	loadBaselineReview: vi.fn(),
	resolveBaselineReview: vi.fn(),
    start: vi.fn(),
    stop: vi.fn(),
  };
  return { useServiceConnection: () => testState.connection };
});

vi.mock("./analysisState", async () => {
  const { reactive, ref } = await import("vue");
  testState.analysis = {
    errorMessage: ref(""),
    evaluation: ref({ findings: [] }),
    exporting: ref(false),
    filters: reactive({ categories: [], severities: [], user: "", process: "", path: "", fromUtc: "", toUtc: "" }),
    hasMore: ref(false),
    integrityVerified: ref(true),
    loadingRisk: ref(false),
    loadingTimeline: ref(false),
	updatingFindingID: ref(""),
    timeline: ref([]),
    exportReport: vi.fn(),
    loadMore: vi.fn(),
    refresh: vi.fn(),
	updateFindingStatus: vi.fn(),
    stop: vi.fn(),
  };
  return { useAnalysisState: () => testState.analysis };
});

vi.mock("./assetDifferenceState", async () => {
  const { ref } = await import("vue");
  testState.assetDifferences = {
    categories: ref([]),
    differences: ref([]),
    baselineAvailable: ref(false),
    loading: ref(false),
    errorMessage: ref(""),
    emptyMessage: ref(""),
    refresh: vi.fn(),
    toggleCategory: vi.fn(),
    stop: vi.fn(),
  };
  return { useAssetDifferenceState: () => testState.assetDifferences };
});

vi.mock("./directoryMonitoring", async () => {
  const { ref } = await import("vue");
  const state = {
	targets: ref([]),
	draftTargets: ref([]),
    directories: ref([]),
    loaded: ref(true),
    loading: ref(false),
    saving: ref(false),
    dirty: ref(false),
    errorMessage: ref(""),
    load: vi.fn(),
    save: vi.fn(),
    stop: vi.fn(),
  };
  return { useDirectoryMonitoring: () => state };
});

vi.mock("./sessionHistory", async () => {
  const { computed, ref } = await import("vue");
  return {
    useSessionHistory: (currentSession: any) => ({
      viewedSession: computed(() => currentSession.value),
      viewingHistory: ref(false),
      items: ref([]),
      loading: ref(false),
      loaded: ref(true),
      hasMore: ref(false),
      errorMessage: ref(""),
      load: vi.fn(),
      loadMore: vi.fn(),
      select: vi.fn(),
      viewCurrent: vi.fn(),
      stop: vi.fn(),
    }),
  };
});

vi.mock("./reportExport", () => ({ saveInlineReport: vi.fn() }));
vi.mock("../bindings/desktopguardpro/internal/storage", () => ({
	BaselineReviewResolution: {
		BaselineReviewResolutionContinue: "continue",
		BaselineReviewResolutionCancel: "cancel",
	},
}));
vi.mock("./DirectoryMonitoringPanel.vue", () => ({ default: { render: () => null } }));
vi.mock("./ReportExportPanel.vue", () => ({ default: { render: () => null } }));
vi.mock("./RiskPanel.vue", () => ({ default: { render: () => null } }));
vi.mock("./TimelinePanel.vue", () => ({ default: { render: () => null } }));
vi.mock("./SessionHistoryPanel.vue", () => ({ default: { render: () => null } }));
vi.mock("./AssetDifferencePanel.vue", () => ({ default: { render: () => null } }));

import App from "./App.vue";

const mountedApps: VueApp[] = [];

async function mountApplication(): Promise<HTMLElement> {
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp(App);
  app.mount(host);
  mountedApps.push(app);
  await nextTick();
  return host;
}

function metric(host: HTMLElement, label: string): HTMLElement {
  const result = [...host.querySelectorAll<HTMLElement>(".metric-card")]
    .find((card) => card.querySelector("span")?.textContent === label);
  if (!result) throw new Error(`missing metric card: ${label}`);
  return result;
}

function navigationButton(host: HTMLElement, label: string): HTMLButtonElement {
  const button = [...host.querySelectorAll<HTMLButtonElement>(".nav-item")]
    .find((candidate) => candidate.textContent === label);
  if (!button) throw new Error(`missing navigation button: ${label}`);
  return button;
}

function contrastRatio(foreground: string, background: string): number {
  const luminance = (hex: string) => {
    const normalized = hex.length === 4
      ? [...hex.slice(1)].map((value) => value + value).join("")
      : hex.slice(1);
    const channels = normalized.match(/.{2}/g)!.map((value) => {
      const channel = Number.parseInt(value, 16) / 255;
      return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
  };
  const first = luminance(foreground);
  const second = luminance(background);
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

afterEach(() => {
  for (const app of mountedApps.splice(0)) app.unmount();
  document.body.replaceChildren();
  testState.connection.currentSession.value = null;
	testState.connection.baselineReview.value = null;
	testState.connection.performProtectionAction.mockClear();
	testState.connection.previewSessionStart.mockClear();
	testState.connection.loadBaselineReview.mockClear();
	testState.connection.resolveBaselineReview.mockClear();
  testState.analysis.refresh.mockClear();
  testState.assetDifferences?.refresh.mockClear();
});

describe("application status summary", () => {
  it("uses desktop command, workspace, and status regions", async () => {
    const host = await mountApplication();
    const workspace = host.querySelector<HTMLElement>(".desktop-workspace");
    const commandBar = workspace?.querySelector<HTMLElement>(".desktop-command-bar[aria-label='会话命令栏']");
    const statusBar = workspace?.querySelector<HTMLElement>(".desktop-status-bar[role='status']");

    expect(workspace).not.toBeNull();
    expect(commandBar?.querySelector(".primary-action")?.textContent).toBe("开启保护");
    expect(commandBar?.textContent).toContain("当前会话");
    expect(statusBar?.textContent).toContain("采集健康：降级");
    expect(statusBar?.textContent).toContain("审计数据保存在本机");
    expect(statusBar?.textContent).not.toContain("技术验证版本");
    expect(host.querySelector(".hero-card")).toBeNull();
  });

  it("organizes the home screen around protection status, summary, and scope", async () => {
    const host = await mountApplication();
    const home = host.querySelector<HTMLElement>(".home-workspace");

    expect(home).not.toBeNull();
    expect(home!.querySelector(".protection-overview__status")?.textContent).toContain("采集状态");
    expect(home!.querySelector(".metric-grid")).not.toBeNull();
    expect(home!.querySelector(".home-detail-grid .protection-guidance")).not.toBeNull();
  });

	it("shows the selected monitoring scope and impact before starting", async () => {
		const host = await mountApplication();
		const level = host.querySelector<HTMLSelectElement>(".monitoring-level-select")!;
		level.value = "strict";
		level.dispatchEvent(new Event("change"));
		host.querySelector<HTMLButtonElement>(".primary-action")?.click();
		await nextTick();
		await nextTick();

		const dialog = host.querySelector<HTMLElement>(".start-preview-dialog");
		expect(testState.connection.previewSessionStart).toHaveBeenCalledWith("strict");
		expect(dialog?.textContent).toContain("重点对象2 项");
		expect(dialog?.textContent).toContain("需要");
		expect(dialog?.textContent).toContain("读取审计");
		expect(testState.connection.performProtectionAction).not.toHaveBeenCalled();

		dialog?.querySelector<HTMLButtonElement>(".confirm-start")?.click();
		await nextTick();
		await nextTick();
		expect(testState.connection.performProtectionAction).toHaveBeenCalledWith(expect.any(String), expect.any(String), "strict");
	});

	it("lists baseline failures and requires continue or cancel", async () => {
		testState.connection.currentSession.value = {
			ID: "review-1", Name: "待复核会话", State: "baseline_review", Revision: 2, MonitoringLevel: "standard",
		};
		testState.connection.baselineReview.value = {
			sessionId: "review-1",
			decision: {
				status: "partial_failure", requiresUserChoice: true, canContinue: true, canCancel: true,
				failures: [{ item: "software", reason: "拒绝访问" }],
			},
		};
		const host = await mountApplication();

		const dialog = host.querySelector<HTMLElement>(".baseline-review-dialog");
		expect(dialog?.textContent).toContain("software");
		expect(dialog?.textContent).toContain("拒绝访问");
		dialog?.querySelector<HTMLButtonElement>(".continue-baseline")?.click();
		await nextTick();
		expect(testState.connection.resolveBaselineReview).toHaveBeenCalledWith("continue");
	});

  it("shows degraded collection health in the primary metric", async () => {
    const host = await mountApplication();

    expect(host.querySelector(".protection-overview__status strong")?.textContent).toBe("降级");
  });

  it("describes risk results as a complete-session calculation", async () => {
    const host = await mountApplication();

    expect(metric(host, "风险提示").querySelector("small")?.textContent).toBe("基于当前会话事件计算");
  });

  it("does not refresh the same session when switching analysis views", async () => {
    const host = await mountApplication();
    testState.connection.currentSession.value = {
      ID: "session-active",
      Name: "当前保护",
      State: "active",
      Revision: 2,
    };
    await nextTick();
    expect(testState.analysis.refresh).toHaveBeenCalledTimes(1);
    expect(testState.analysis.refresh).toHaveBeenLastCalledWith("session-active");

    for (const label of ["事件时间线", "风险分析", "报告导出"]) {
      const button = [...host.querySelectorAll<HTMLButtonElement>("button")]
        .find((candidate) => candidate.textContent === label);
      button?.click();
      await nextTick();
    }

    expect(testState.analysis.refresh).toHaveBeenCalledTimes(1);
  });

  it("opens asset changes for the viewed session and queries its baseline", async () => {
    const host = await mountApplication();
    testState.connection.currentSession.value = {
      ID: "session-assets",
      Name: "资产差异会话",
      State: "completed",
      Revision: 2,
    };
    await nextTick();

    const assets = navigationButton(host, "资产差异");
    expect(assets.disabled).toBe(false);
    assets.click();
    await nextTick();

    expect(assets.getAttribute("aria-current")).toBe("page");
    expect(host.querySelector("h1")?.textContent).toBe("资产差异");
    expect(testState.assetDifferences.refresh).toHaveBeenLastCalledWith("session-assets");
  });

  it("identifies the current navigation item", async () => {
    const host = await mountApplication();
    const home = navigationButton(host, "仪表盘");
    const history = navigationButton(host, "历史会话");

    expect(home.getAttribute("aria-current")).toBe("page");
    expect(history.hasAttribute("aria-current")).toBe(false);

    history.click();
    await nextTick();
    expect(home.hasAttribute("aria-current")).toBe(false);
    expect(history.getAttribute("aria-current")).toBe("page");
  });

  it("keeps unimplemented diagnostic navigation and validation copy out of the product shell", async () => {
    const host = await mountApplication();
    const navigationLabels = [...host.querySelectorAll<HTMLButtonElement>(".nav-item")]
      .map((button) => button.textContent);

    expect(navigationLabels).not.toContain("诊断");
    expect(host.textContent).not.toContain("第一里程碑");
    expect(host.textContent).not.toContain("技术验证版本");
  });

  it("uses a navigation focus ring with at least three-to-one contrast", async () => {
    const css = await readFile(resolve(process.cwd(), "src/styles.css"), "utf8");
    const focusBlock = css.match(/\.nav-item:focus-visible\s*\{([^}]*)\}/s)?.[1] ?? "";
    const ringColor = focusBlock.match(/box-shadow:\s*0 0 0 3px\s*(#[0-9a-f]{3,6})/i)?.[1];

    expect(ringColor).toBeDefined();
    expect(contrastRatio(ringColor!, "#18324d")).toBeGreaterThanOrEqual(3);
  });
});
