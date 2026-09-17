// @vitest-environment happy-dom

import { createApp, nextTick, type App } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";

import { MonitoringTargetKind, MonitoringTargetResolutionStatus } from "../bindings/desktopguardpro/internal/domain";
import DirectoryMonitoringPanel from "./DirectoryMonitoringPanel.vue";

const mountedApps: App[] = [];

function mountPanel(overrides: Record<string, unknown> = {}) {
  const host = document.createElement("div");
  document.body.append(host);
  const onUpdate = vi.fn();
  const onSave = vi.fn();
  const app = createApp(DirectoryMonitoringPanel, {
    modelValue: [
      { path: "C:\\Audit\\Documents", kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: true, resolutionStatus: MonitoringTargetResolutionStatus.MonitoringTargetResolutionAvailable, resolutionDetail: "路径已验证，可以监控" },
      { path: "C:\\Audit\\focus.txt", kind: MonitoringTargetKind.MonitoringTargetKindFile, recursive: false },
    ],
    loaded: true,
    loading: false,
    saving: false,
    dirty: false,
    editable: true,
    connected: true,
    errorMessage: "",
    "onUpdate:modelValue": onUpdate,
    onSave,
    ...overrides,
  });
  app.mount(host);
  mountedApps.push(app);
  return { host, onUpdate, onSave };
}

afterEach(() => {
  for (const app of mountedApps.splice(0)) app.unmount();
  document.body.replaceChildren();
});

describe("DirectoryMonitoringPanel", () => {
  it("renders target types, recursion and resolution status", () => {
    const { host } = mountPanel();
    const rows = host.querySelectorAll<HTMLElement>(".directory-row");

    expect(host.querySelector(".directory-capacity")?.textContent).toContain("2 / 16");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.querySelector("select")?.value).toBe("directory");
    expect(rows[0]?.querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(true);
    expect(rows[0]?.textContent).toContain("路径可用");
    expect(rows[1]?.querySelector("select")?.value).toBe("file");
  });

  it("adds a removable volume and removes an existing target", async () => {
    const { host, onUpdate } = mountPanel();
    const kind = host.querySelector<HTMLSelectElement>('[aria-label="新增目标类型"]')!;
    kind.value = "removable_volume";
    kind.dispatchEvent(new Event("change"));
    const input = host.querySelector<HTMLInputElement>("#target-path-to-add")!;
    input.value = "E:\\";
    input.dispatchEvent(new Event("input"));
    host.querySelector<HTMLButtonElement>(".add-target-controls button")?.click();
    await nextTick();

    expect(onUpdate).toHaveBeenLastCalledWith(expect.arrayContaining([
      expect.objectContaining({ path: "E:\\", kind: "removable_volume", recursive: false }),
    ]));

    host.querySelectorAll<HTMLButtonElement>(".directory-row > button")[0]?.click();
    await nextTick();
    expect(onUpdate).toHaveBeenLastCalledWith([
      expect.objectContaining({ path: "C:\\Audit\\focus.txt", kind: "file" }),
    ]);
  });

  it("blocks empty and duplicate additions", async () => {
    const { host, onUpdate } = mountPanel();
    host.querySelector<HTMLButtonElement>(".add-target-controls button")?.click();
    await nextTick();
    expect(host.querySelector(".directory-input-message")?.textContent).toContain("请输入本地路径");

    const input = host.querySelector<HTMLInputElement>("#target-path-to-add")!;
    input.value = "c:\\audit\\documents";
    input.dispatchEvent(new Event("input"));
    host.querySelector<HTMLButtonElement>(".add-target-controls button")?.click();
    await nextTick();
    expect(host.querySelector(".directory-input-message")?.textContent).toContain("已经在监控范围中");
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it("locks editing during a protection session", () => {
    const { host } = mountPanel({ editable: false });
    expect(host.querySelector<HTMLInputElement>('.directory-row input[type="text"]')?.disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>(".add-target-controls button")?.disabled).toBe(true);
    expect(host.textContent).toContain("会话结束后可修改监控范围");
  });
});
