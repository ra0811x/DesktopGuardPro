// @vitest-environment happy-dom

import { createApp, nextTick, type App } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("../bindings/desktopguardpro/internal/domain", () => ({
  EventCategory: {
    EventCategoryFile: "file",
    EventCategoryProcess: "process",
    EventCategorySoftware: "software",
    EventCategorySystem: "system",
    EventCategoryDevice: "device",
    EventCategoryHealth: "health",
  },
	EventSeverity: {
		EventSeverityHigh: "high",
		EventSeverityMedium: "medium",
		EventSeverityLow: "low",
	},
}));

import TimelinePanel from "./TimelinePanel.vue";

const mountedApps: App[] = [];

function mountPanel(overrides: Record<string, unknown> = {}) {
  const host = document.createElement("div");
  document.body.append(host);
  const onToggleCategory = vi.fn();
  const app = createApp(TimelinePanel, {
    records: [],
    categories: [],
	severities: [],
	user: "",
	process: "",
	path: "",
    fromUtc: "",
    toUtc: "",
    loading: false,
    hasMore: false,
    integrityVerified: true,
    errorMessage: "",
    onToggleCategory,
    ...overrides,
  });
  app.mount(host);
  mountedApps.push(app);
  return { host, onToggleCategory };
}

afterEach(() => {
  for (const app of mountedApps.splice(0)) app.unmount();
  document.body.replaceChildren();
});

describe("TimelinePanel", () => {
  it("uses split filter and event panes for a desktop workspace", () => {
    const { host } = mountPanel();
    const workspace = host.querySelector<HTMLElement>(".timeline-workspace");
    const dataRegion = workspace?.querySelector<HTMLElement>(".timeline-table-wrap[role='region']");

    expect(workspace).not.toBeNull();
    expect(workspace?.querySelector(".timeline-filter-pane[aria-label='时间线筛选']")).not.toBeNull();
    expect(workspace?.querySelector(".timeline-results-pane")).not.toBeNull();
    expect(dataRegion?.getAttribute("aria-label")).toBe("会话事件数据表");
    expect(dataRegion?.getAttribute("tabindex")).toBe("0");
  });

  it("retains category filter interaction", async () => {
    const { host, onToggleCategory } = mountPanel();
    const fileButton = [...host.querySelectorAll<HTMLButtonElement>("button")]
      .find((button) => button.textContent === "文件");

    fileButton?.click();
    await nextTick();

    expect(onToggleCategory).toHaveBeenCalledWith("file");
  });

	it("provides risk, user, process and path filters", async () => {
		const onToggleSeverity = vi.fn();
		const onUpdateUser = vi.fn();
		const onUpdateProcess = vi.fn();
		const onUpdatePath = vi.fn();
		const { host } = mountPanel({ onToggleSeverity, onUpdateUser, onUpdateProcess, onUpdatePath });
		const highButton = host.querySelector<HTMLButtonElement>(".severity-filter button");
		highButton?.click();
		for (const [selector, value] of [[".user-filter", "Raymond"], [".process-filter", "editor.exe"], [".path-filter", "Evidence"]] as const) {
			const input = host.querySelector<HTMLInputElement>(selector)!;
			input.value = value;
			input.dispatchEvent(new Event("input"));
		}
		await nextTick();

		expect(onToggleSeverity).toHaveBeenCalledWith("high");
		expect(onUpdateUser).toHaveBeenCalledWith("Raymond");
		expect(onUpdateProcess).toHaveBeenCalledWith("editor.exe");
		expect(onUpdatePath).toHaveBeenCalledWith("Evidence");
	});

  it("selects an event and exposes its available audit details", async () => {
    const { host } = mountPanel({
      records: [{
        Event: {
          eventId: "event-001",
          sequence: 42,
          category: "file",
          action: "modified",
          severity: "medium",
          observedUtc: "2026-09-06T09:15:00Z",
          objectKey: "C:\\Audit\\Documents\\notes.txt",
          processKey: "process-2026",
          source: "directory-monitor",
          confidence: "direct",
        },
      }],
    });
    const row = host.querySelector<HTMLElement>("tr[role='button']");

    row?.click();
    await nextTick();

    const details = host.querySelector<HTMLElement>(".timeline-detail-pane");
    expect(row?.getAttribute("aria-selected")).toBe("true");
    expect(details?.textContent).toContain("#42");
    expect(details?.textContent).toContain("C:\\Audit\\Documents\\notes.txt");
    expect(details?.textContent).toContain("直接观测");
    expect(details?.textContent).toContain("中风险");
  });

  it("distinguishes loading and page-integrity states", () => {
    const { host } = mountPanel({ loading: true, integrityVerified: false });

    expect(host.querySelector(".integrity-badge")?.textContent).toContain("等待本页完整性验证");
    expect(host.querySelector(".timeline-empty")?.textContent).toContain("正在读取会话事件");
  });
});
