import { describe, expect, it, vi } from "vitest";

import {
  AssetCategory,
  AssetDifferenceKind,
} from "../bindings/desktopguardpro/internal/domain";
import { AssetDifferenceResult } from "../bindings/desktopguardpro/internal/service";
import {
  type AssetDifferenceClient,
  useAssetDifferenceState,
} from "./assetDifferenceState";

function assetDifferenceClient(): AssetDifferenceClient {
  return {
    QueryAssetDifferences: vi.fn(() => Promise.resolve(new AssetDifferenceResult({
      sessionId: "session-1",
      baselineAvailable: true,
      differences: [{
        kind: AssetDifferenceKind.AssetDifferenceAdded,
        after: {
          category: AssetCategory.AssetCategoryDevice,
          identifier: "USB\\VID_1234",
          displayName: "Added USB Device",
        },
      }],
    }))),
  };
}

describe("useAssetDifferenceState", () => {
  it("loads asset differences with selected categories", async () => {
    const client = assetDifferenceClient();
    const state = useAssetDifferenceState(client);
    state.categories.value = [AssetCategory.AssetCategoryDevice];

    await state.refresh("session-1");

    expect(state.baselineAvailable.value).toBe(true);
    expect(state.differences.value).toHaveLength(1);
    expect(client.QueryAssetDifferences).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: "session-1",
      categories: [AssetCategory.AssetCategoryDevice],
    }));
  });

  it("reports when a historical session has no baseline", async () => {
    const client = assetDifferenceClient();
    vi.mocked(client.QueryAssetDifferences).mockResolvedValue(new AssetDifferenceResult({
      sessionId: "old-session", baselineAvailable: false, differences: [],
    }));
    const state = useAssetDifferenceState(client);

    await state.refresh("old-session");

    expect(state.baselineAvailable.value).toBe(false);
    expect(state.emptyMessage.value).toContain("没有资产基线");
  });
});
