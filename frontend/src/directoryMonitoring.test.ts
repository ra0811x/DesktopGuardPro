import { describe, expect, it, vi } from "vitest";

import { MonitoringTargetKind, MonitoringTargetResolutionStatus } from "../bindings/desktopguardpro/internal/domain";
import { useDirectoryMonitoring, type DirectoryMonitoringClient } from "./directoryMonitoring";

function createClient(): DirectoryMonitoringClient {
  return {
    GetDirectoryMonitoring: vi.fn(() => Promise.resolve({
      directories: ["C:\\Documents"],
      targets: [{ path: "C:\\Documents", kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: true }],
    })),
    UpdateMonitoringTargets: vi.fn((targets) => Promise.resolve({ directories: [], targets })),
  };
}

describe("directory monitoring configuration", () => {
  it("loads and saves typed file-system targets", async () => {
    const client = createClient();
    const state = useDirectoryMonitoring(client);
    expect(await state.load()).toBe(true);
    expect(state.targets.value).toHaveLength(1);
    expect(state.dirty.value).toBe(false);

    state.draftTargets.value = [
      { path: "D:\\重点目录", kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: false },
      { path: "D:\\重点目录\\证据.txt", kind: MonitoringTargetKind.MonitoringTargetKindFile, recursive: false },
      { path: "E:\\", kind: MonitoringTargetKind.MonitoringTargetKindRemovableVolume, recursive: false },
    ];
    expect(state.dirty.value).toBe(true);
    expect(await state.save()).toBe(true);
    expect(client.UpdateMonitoringTargets).toHaveBeenCalledWith(state.draftTargets.value);
    expect(state.targets.value).toHaveLength(3);
    expect(state.dirty.value).toBe(false);
  });

  it("keeps saved scope and unsaved edits when the service rejects an update", async () => {
    const client = createClient();
    vi.mocked(client.UpdateMonitoringTargets).mockRejectedValue(new Error("service error session_in_progress: 会话结束后才能修改监控范围"));
    const state = useDirectoryMonitoring(client);
    await state.load();
    state.draftTargets.value = [{
      path: "D:\\其他资料", kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: true,
    }];

    expect(await state.save()).toBe(false);
    expect(state.targets.value[0].path).toBe("C:\\Documents");
    expect(state.draftTargets.value[0].path).toBe("D:\\其他资料");
    expect(state.dirty.value).toBe(true);
    expect(state.errorMessage.value).toBe("会话结束后才能修改监控范围");
  });

  it("converts legacy directory responses and retries a failed load", async () => {
    const client = createClient();
    vi.mocked(client.GetDirectoryMonitoring)
      .mockRejectedValueOnce(new Error("pipe unavailable"))
      .mockResolvedValueOnce({ directories: ["C:\\Legacy"] });
    const state = useDirectoryMonitoring(client);

    expect(await state.load()).toBe(false);
    expect(state.loaded.value).toBe(false);
    expect(await state.save()).toBe(false);
    expect(await state.load()).toBe(true);
    expect(state.draftTargets.value).toEqual([{
      path: "C:\\Legacy", kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: true,
    }]);
  });

	it("preserves a reparse target's configured path when another row is edited", async () => {
		const client = createClient();
		vi.mocked(client.GetDirectoryMonitoring).mockResolvedValue({
			directories: [],
			targets: [{
				path: "D:\\Resolved", configuredPath: "C:\\EvidenceLink",
				kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: true,
				resolutionStatus: MonitoringTargetResolutionStatus.MonitoringTargetResolutionReparseResolved,
				resolutionDetail: "resolved to D:\\Resolved",
			}],
		});
		const state = useDirectoryMonitoring(client);
		await state.load();
		state.draftTargets.value.push({
			path: "E:\\Extra", kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: false,
		});

		await state.save();

		expect(client.UpdateMonitoringTargets).toHaveBeenCalledWith(expect.arrayContaining([
			expect.objectContaining({ path: "C:\\EvidenceLink" }),
		]));
	});
});
