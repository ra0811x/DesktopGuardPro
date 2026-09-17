import { computed, ref } from "vue";

import { Bridge } from "../bindings/desktopguardpro/internal/appbridge";
import type {
  AssetCategory,
  AssetDifference,
} from "../bindings/desktopguardpro/internal/domain";
import {
  AssetDifferenceQueryRequest,
  type AssetDifferenceResult,
} from "../bindings/desktopguardpro/internal/service";

type CancellableOperation<T> = Promise<T> & {
  cancel?: (cause?: unknown) => void | PromiseLike<void>;
};

export interface AssetDifferenceClient {
  QueryAssetDifferences(
    request: AssetDifferenceQueryRequest,
  ): CancellableOperation<AssetDifferenceResult>;
}

export function useAssetDifferenceState(
  client: AssetDifferenceClient = Bridge,
) {
  const sessionID = ref("");
  const categories = ref<AssetCategory[]>([]);
  const differences = ref<AssetDifference[]>([]);
  const baselineAvailable = ref(false);
  const loading = ref(false);
  const errorMessage = ref("");
  const emptyMessage = computed(() => {
    if (!sessionID.value) return "请选择要查看的会话。";
    if (loading.value || errorMessage.value) return "";
    if (!baselineAvailable.value) return "该会话没有资产基线。";
    if (differences.value.length === 0) return "开始与结束基线之间没有资产变化。";
    return "";
  });

  let generation = 0;
  let stopped = false;
  let pending: CancellableOperation<AssetDifferenceResult> | undefined;

  async function refresh(nextSessionID: string): Promise<void> {
    const normalizedSessionID = nextSessionID.trim();
    const requestGeneration = ++generation;
    cancelPending();
    sessionID.value = normalizedSessionID;
    differences.value = [];
    baselineAvailable.value = false;
    errorMessage.value = "";
    if (!normalizedSessionID || stopped) {
      loading.value = false;
      return;
    }
    loading.value = true;
    try {
      pending = client.QueryAssetDifferences(new AssetDifferenceQueryRequest({
        sessionId: normalizedSessionID,
        categories: [...categories.value],
      }));
      const result = await pending;
      if (stopped || requestGeneration !== generation) return;
      if (result.sessionId !== normalizedSessionID) {
        errorMessage.value = "服务返回了不匹配的资产差异结果。";
        return;
      }
      baselineAvailable.value = result.baselineAvailable;
      differences.value = [...(result.differences ?? [])];
    } catch {
      if (!stopped && requestGeneration === generation) {
        errorMessage.value = "无法读取资产变化，请确认后台服务连接后重试。";
      }
    } finally {
      if (requestGeneration === generation) {
        pending = undefined;
        loading.value = false;
      }
    }
  }

  async function toggleCategory(category: AssetCategory): Promise<void> {
    const index = categories.value.indexOf(category);
    if (index >= 0) categories.value.splice(index, 1);
    else categories.value.push(category);
    await refresh(sessionID.value);
  }

  function stop(): void {
    stopped = true;
    generation++;
    cancelPending();
    loading.value = false;
  }

  function cancelPending(): void {
    if (pending?.cancel) {
      void Promise.resolve(pending.cancel(new Error("asset difference request superseded"))).catch(() => {});
    }
    pending = undefined;
  }

  return {
    baselineAvailable,
    categories,
    differences,
    emptyMessage,
    errorMessage,
    loading,
    refresh,
    sessionID,
    stop,
    toggleCategory,
  };
}
