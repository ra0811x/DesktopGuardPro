import { computed, ref } from "vue";

import { Bridge } from "../bindings/desktopguardpro/internal/appbridge";
import {
  MonitoringTargetKind,
  type MonitoringTarget,
} from "../bindings/desktopguardpro/internal/domain";
import type { DirectoryMonitoringResult } from "../bindings/desktopguardpro/internal/service";
import type { CancellableOperation } from "./serviceConnection";

export interface DirectoryMonitoringClient {
  GetDirectoryMonitoring(): CancellableOperation<DirectoryMonitoringResult>;
  UpdateMonitoringTargets(targets: MonitoringTarget[]): CancellableOperation<DirectoryMonitoringResult>;
}

export function useDirectoryMonitoring(client: DirectoryMonitoringClient = Bridge) {
  const targets = ref<MonitoringTarget[]>([]);
  const draftTargets = ref<MonitoringTarget[]>([]);
  const loaded = ref(false);
  const loading = ref(false);
  const saving = ref(false);
  const errorMessage = ref("");
  const directories = computed(() => targets.value
    .filter((target) => target.kind === MonitoringTargetKind.MonitoringTargetKindDirectory)
    .map((target) => target.path));
  const dirty = computed(() => targetSignature(draftTargets.value) !== targetSignature(targets.value));
  let pending: CancellableOperation<DirectoryMonitoringResult> | undefined;
  let stopped = false;

  function apply(result: DirectoryMonitoringResult): void {
    const received = result.targets?.length
      ? result.targets
      : (result.directories ?? []).map((path) => ({
        path, kind: MonitoringTargetKind.MonitoringTargetKindDirectory, recursive: true,
      }));
    targets.value = cloneTargets(received);
    draftTargets.value = cloneTargets(received);
    loaded.value = true;
    errorMessage.value = "";
  }

  async function load(): Promise<boolean> {
    if (loading.value || saving.value || stopped) return false;
    loading.value = true;
    try {
      pending = client.GetDirectoryMonitoring();
      const result = await pending;
      if (stopped) return false;
      apply(result);
      return true;
    } catch (error) {
      loaded.value = false;
      errorMessage.value = configurationError(error, "无法读取监控范围，请确认后台服务连接后重试");
      return false;
    } finally {
      pending = undefined;
      loading.value = false;
    }
  }

  async function save(): Promise<boolean> {
    if (!loaded.value || loading.value || saving.value || stopped) return false;
    saving.value = true;
    errorMessage.value = "";
    try {
      const editable = draftTargets.value.map(({ path, configuredPath, kind, recursive }) => ({
		path: (configuredPath || path).trim(), kind, recursive,
	  }));
      pending = client.UpdateMonitoringTargets(editable);
      const result = await pending;
      if (stopped) return false;
      apply(result);
      return true;
    } catch (error) {
      errorMessage.value = configurationError(error, "无法保存监控范围，请确认后台服务连接后重试");
      return false;
    } finally {
      pending = undefined;
      saving.value = false;
    }
  }

  function stop(): void {
    stopped = true;
    if (pending?.cancel) void pending.cancel(new Error("window closed"));
  }

  return { targets, draftTargets, directories, loaded, loading, saving, dirty, errorMessage, load, save, stop };
}

function cloneTargets(targets: readonly MonitoringTarget[]): MonitoringTarget[] {
  return targets.map((target) => ({ ...target }));
}

function targetSignature(targets: readonly MonitoringTarget[]): string {
  return JSON.stringify(targets.map(({ path, kind, recursive }) => ({ path: path.trim(), kind, recursive })));
}

function configurationError(error: unknown, fallback: string): string {
  const raw = error instanceof Error ? error.message : String(error);
  if (raw.includes("service error")) {
    const separator = raw.indexOf(":");
    if (separator >= 0) return raw.slice(separator + 1).trim();
  }
  return fallback;
}
