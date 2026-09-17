// @vitest-environment happy-dom

import { createApp, nextTick, type App } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

import SessionSummaryPanel from "./SessionSummaryPanel.vue";

const mountedApps: App[] = [];

function mountPanel(overrides: Record<string, unknown> = {}) {
  const host = document.createElement("div");
  document.body.append(host);
  const onOpenTimeline = vi.fn();
  const onOpenRisk = vi.fn();
  const onOpenReport = vi.fn();
  const app = createApp(SessionSummaryPanel, {
    session: { ID: "session-001", Name: "离席保护 2026-09-06", State: "completed", Revision: 3 },
    viewingHistory: true,
    loading: false,
    integrityVerified: true,
    evaluation: {
      eventCount: 4,
      coverageGapCount: 1,
      findings: [{ id: "risk-001", level: "high", title: "系统时间发生变化", evidence: [] }],
    },
    timeline: [{ Event: { eventId: "event-001", sequence: 1, category: "system", action: "time_changed", objectKey: "系统时间", source: "registry-monitor", observedUtc: "2026-09-06T09:15:00Z" } }],
    onOpenTimeline,
    onOpenRisk,
    onOpenReport,
    ...overrides,
  });
  app.mount(host);
  mountedApps.push(app);
  return { host, onOpenTimeline, onOpenRisk, onOpenReport };
}

afterEach(() => {
  for (const app of mountedApps.splice(0)) app.unmount();
  document.body.replaceChildren();
});

describe("SessionSummaryPanel", () => {
  it("summarizes a historical session with risk, evidence, and integrity status", () => {
    const { host } = mountPanel();

    expect(host.querySelector(".session-summary-workspace")?.textContent).toContain("离席保护 2026-09-06");
    expect(host.querySelector(".session-summary-workspace")?.textContent).toContain("正在查看历史会话");
    expect(host.querySelector(".summary-metric--risk")?.textContent).toContain("1");
    expect(host.querySelector(".summary-metric--integrity")?.textContent).toContain("已验证");
    expect(host.querySelector(".summary-event-list")?.textContent).toContain("系统时间");
  });

  it("opens the related analysis views from the session summary", async () => {
    const { host, onOpenTimeline, onOpenRisk, onOpenReport } = mountPanel();
    const buttons = [...host.querySelectorAll<HTMLButtonElement>(".summary-actions button")];

    buttons[0]?.click();
    buttons[1]?.click();
    buttons[2]?.click();
    await nextTick();

    expect(onOpenTimeline).toHaveBeenCalledOnce();
    expect(onOpenRisk).toHaveBeenCalledOnce();
    expect(onOpenReport).toHaveBeenCalledOnce();
  });

  it("explains pending analysis and an empty loaded event page", () => {
    const { host } = mountPanel({ loading: true, integrityVerified: false, evaluation: null, timeline: [] });

    expect(host.querySelector(".summary-analysis-state")?.textContent).toContain("正在读取会话分析");
    expect(host.querySelector(".summary-metric--integrity")?.textContent).toContain("等待验证");
    expect(host.querySelector(".summary-event-empty")?.textContent).toContain("当前页面尚未加载事件");
  });
});
