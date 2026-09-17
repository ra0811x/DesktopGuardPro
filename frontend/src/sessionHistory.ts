import { computed, ref, type Ref } from "vue";
import { Bridge } from "../bindings/desktopguardpro/internal/appbridge";
import type { Session } from "../bindings/desktopguardpro/internal/domain";
import type {
  SessionRetentionLockRequest,
  SessionRetentionLockResult,
  SessionRetentionPruneRequest,
  SessionRetentionPruneResult,
} from "../bindings/desktopguardpro/internal/service";
import type { CancellableOperation } from "./serviceConnection";

export interface SessionHistoryItem {
  session: Session;
  createdUtc: string;
  updatedUtc: string;
  retentionLocked: boolean;
}
export interface SessionHistoryPage {
  items: SessionHistoryItem[];
  hasMore: boolean;
  nextCursor?: string;
}
export interface SessionHistoryClient {
  ListSessions(cursor: string): CancellableOperation<SessionHistoryPage>;
  UpdateSessionRetentionLock?(request: SessionRetentionLockRequest): CancellableOperation<SessionRetentionLockResult>;
  PruneSessionRetention?(request: SessionRetentionPruneRequest): CancellableOperation<SessionRetentionPruneResult>;
}

export function useSessionHistory(currentSession: Ref<Session | null>, client: SessionHistoryClient = Bridge) {
  const items = ref<SessionHistoryItem[]>([]);
  const loaded = ref(false);
  const loading = ref(false);
  const hasMore = ref(false);
  const errorMessage = ref("");
  const retentionBusy = ref(false);
  const retentionFeedback = ref("");
  const selected = ref<Session | null>(null);
  const viewedSession = computed(() => selected.value ?? currentSession.value);
  const viewingHistory = computed(() => selected.value !== null);
  let cursor = "";
  let generation = 0;
  let stopped = false;
  let pending: CancellableOperation<SessionHistoryPage> | undefined;

  function cancelPending(): void {
    if (pending?.cancel) void Promise.resolve(pending.cancel(new Error("history request superseded"))).catch(() => {});
    pending = undefined;
  }

  async function load(append = false): Promise<void> {
    if (stopped || (append && (loading.value || !hasMore.value))) return;
    const requestedGeneration = ++generation;
    cancelPending();
    loading.value = true;
    errorMessage.value = "";
    try {
      pending = client.ListSessions(append ? cursor : "");
      const page = await pending;
      if (stopped || requestedGeneration !== generation) return;
      items.value = append ? [...items.value, ...(page.items ?? [])] : [...(page.items ?? [])];
      cursor = page.nextCursor ?? "";
      hasMore.value = page.hasMore && !!cursor;
      loaded.value = true;
    } catch {
      if (!stopped && requestedGeneration === generation) errorMessage.value = "无法读取历史会话，请确认后台服务连接后重试。";
    } finally {
      if (requestedGeneration === generation) { pending = undefined; loading.value = false; }
    }
  }

  function select(session: Session): void {
    selected.value = session.ID === currentSession.value?.ID ? null : { ...session };
  }
  function viewCurrent(): void { selected.value = null; }
  async function updateRetentionLock(sessionID: string, locked: boolean): Promise<void> {
    if (stopped || retentionBusy.value) return;
    if (!client.UpdateSessionRetentionLock) {
      retentionFeedback.value = "当前服务版本不支持会话保留设置。";
      return;
    }
    retentionBusy.value = true;
    retentionFeedback.value = "";
    try {
      const result = await client.UpdateSessionRetentionLock({ sessionId: sessionID, locked });
      if (stopped) return;
      items.value = items.value.map((item) => item.session.ID === result.sessionId
        ? { ...item, retentionLocked: result.locked }
        : item);
      retentionFeedback.value = result.locked ? "已锁定保留该会话。" : "已解除该会话的保留锁。";
    } catch {
      if (!stopped) retentionFeedback.value = "无法更新会话保留状态，请稍后重试。";
    } finally {
      retentionBusy.value = false;
    }
  }
  async function prune(beforeUtc: string): Promise<void> {
    if (stopped || retentionBusy.value) return;
    if (!client.PruneSessionRetention || Number.isNaN(Date.parse(beforeUtc))) {
      retentionFeedback.value = "清理日期无效，或当前服务版本不支持会话清理。";
      return;
    }
    retentionBusy.value = true;
    retentionFeedback.value = "";
    try {
      const result = await client.PruneSessionRetention({ beforeUtc, limit: 100 });
      if (stopped) return;
      const deleted = new Set(result.deletedSessionIds ?? []);
      items.value = items.value.filter((item) => !deleted.has(item.session.ID));
      if (selected.value && deleted.has(selected.value.ID)) selected.value = null;
      retentionFeedback.value = deleted.size === 0 ? "没有符合清理条件的会话。" : `已清理 ${deleted.size} 个会话。`;
    } catch {
      if (!stopped) retentionFeedback.value = "无法完成会话清理，请稍后重试。";
    } finally {
      retentionBusy.value = false;
    }
  }
  function stop(): void { stopped = true; generation++; cancelPending(); loading.value = false; }

  return {
    items, loaded, loading, hasMore, errorMessage, retentionBusy, retentionFeedback, viewedSession, viewingHistory,
    load, loadMore: () => load(true), select, viewCurrent, updateRetentionLock, prune, stop,
  };
}
