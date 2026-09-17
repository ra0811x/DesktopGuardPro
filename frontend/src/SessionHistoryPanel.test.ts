// @vitest-environment happy-dom

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

import { createApp, nextTick, type App } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../bindings/desktopguardpro/internal/domain";
import SessionHistoryPanel from "./SessionHistoryPanel.vue";

const savedSession = {
  ID: "session-2026-09-05",
  Name: "研发环境保护",
  State: "completed",
  Revision: 4,
} as Session;

const mountedApps: App[] = [];

function mountPanel(onSelect = vi.fn(), overrides: Record<string, unknown> = {}) {
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp(SessionHistoryPanel, {
    items: [{
      session: savedSession,
      createdUtc: "2026-09-05T01:00:00Z",
      updatedUtc: "2026-09-05T02:00:00Z",
    }],
    loading: false,
    loaded: true,
    hasMore: false,
    connected: true,
    errorMessage: "",
    onSelect,
    ...overrides,
  });
  app.mount(host);
  mountedApps.push(app);
  return { host, onSelect };
}

function contrastRatio(foreground: string, background: string): number {
  const luminance = (hex: string) => {
    const channels = hex.slice(1).match(/.{2}/g)!.map((value) => {
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
});

describe("SessionHistoryPanel", () => {
  it("renders saved sessions with accessible controls", () => {
    const { host } = mountPanel();
    const section = host.querySelector("section");
    const viewButton = host.querySelector<HTMLButtonElement>("button[aria-label='查看 研发环境保护']");

    expect(section?.getAttribute("aria-label")).toBe("已保存的保护会话");
    expect(viewButton?.textContent).toBe("查看记录");
    expect(host.textContent).toContain("已完成");
  });

  it("uses a desktop data workspace with command and status bars", () => {
    const { host } = mountPanel();
    const workspace = host.querySelector<HTMLElement>(".history-workspace");
    const dataRegion = workspace?.querySelector<HTMLElement>(".history-table-wrap[role='region']");

    expect(workspace).not.toBeNull();
    expect(workspace?.querySelector(".history-command-strip")).not.toBeNull();
    expect(dataRegion?.getAttribute("aria-label")).toBe("历史会话数据表");
    expect(dataRegion?.getAttribute("tabindex")).toBe("0");
    expect(workspace?.querySelector(".history-status-bar")?.textContent).toContain("已显示 1 条");
  });

  it("emits the selected session from the mounted component", async () => {
    const { host, onSelect } = mountPanel();
    const viewButton = host.querySelector<HTMLButtonElement>("button[aria-label='查看 研发环境保护']");

    viewButton?.click();
    await nextTick();

    expect(onSelect).toHaveBeenCalledWith(savedSession);
  });

  it("keeps the selected session identifier above normal-text contrast", async () => {
    const { host } = mountPanel(vi.fn(), { selectedID: savedSession.ID });
    expect(host.querySelector("tbody tr")?.classList.contains("selected")).toBe(true);

    const [componentSource, globalStyles] = await Promise.all([
      readFile(resolve(process.cwd(), "src/SessionHistoryPanel.vue"), "utf8"),
      readFile(resolve(process.cwd(), "src/styles.css"), "utf8"),
    ]);
    const selectedRule = componentSource.match(/tbody tr\.selected small\s*\{([^}]*)\}/s)?.[1] ?? "";
    const colorToken = selectedRule.match(/color:\s*var\((--[\w-]+)\)/)?.[1];
    const color = colorToken
      ? globalStyles.match(new RegExp(`${colorToken}:\\s*(#[0-9a-f]{6})`, "i"))?.[1]
      : undefined;

    expect(color).toBeDefined();
    expect(contrastRatio(color!, "#dce7f0")).toBeGreaterThanOrEqual(4.5);
  });
});
