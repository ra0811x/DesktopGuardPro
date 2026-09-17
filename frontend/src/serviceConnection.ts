import { computed, ref } from "vue";

import { Bridge } from "../bindings/desktopguardpro/internal/appbridge";
import type { Session } from "../bindings/desktopguardpro/internal/domain";
import { MonitoringLevel, SessionState } from "../bindings/desktopguardpro/internal/domain";
import type { SessionResult } from "../bindings/desktopguardpro/internal/service";
import type { HealthResult } from "../bindings/desktopguardpro/internal/service";
import type { SessionStartPreviewResult } from "../bindings/desktopguardpro/internal/service";
import type { SessionBaselineReviewResult } from "../bindings/desktopguardpro/internal/service";
import type { BaselineReviewResolution } from "../bindings/desktopguardpro/internal/storage";

export type ConnectionPhase =
  | "connecting"
  | "online"
  | "reconnecting"
  | "offline";

export type CancellableOperation<T> = Promise<T> & {
  cancel?: (cause?: unknown) => void | PromiseLike<void>;
};

export interface ServiceConnectionClient {
  GetHealth(): CancellableOperation<HealthResult>;
  CreateSession(id: string, name: string): CancellableOperation<SessionResult>;
	CreateSessionWithMonitoringLevel(id: string, name: string, level: MonitoringLevel): CancellableOperation<SessionResult>;
	GetSessionStartPreview(level: MonitoringLevel): CancellableOperation<SessionStartPreviewResult>;
	GetSessionBaselineReview(sessionID: string): CancellableOperation<SessionBaselineReviewResult>;
	ResolveSessionBaselineReview(sessionID: string, resolution: BaselineReviewResolution): CancellableOperation<SessionBaselineReviewResult>;
  EndSession(): CancellableOperation<SessionResult>;
  TransitionSession(state: SessionState): CancellableOperation<SessionResult>;
}

export interface ServiceConnectionOptions {
	healthCheckIntervalMilliseconds?: number;
  requestTimeoutMilliseconds?: number;
  retryDelaysMilliseconds?: readonly number[];
}

const defaultHealthCheckIntervalMilliseconds = 10_000;
const defaultRequestTimeoutMilliseconds = 5_000;
const sessionRequestTimeoutMilliseconds = 35_000;
const defaultRetryDelaysMilliseconds = [1_000, 2_000, 5_000, 10_000, 30_000] as const;

class RequestTimeoutError extends Error {
  constructor() {
    super("后台服务请求超时");
    this.name = "RequestTimeoutError";
  }
}

export function useServiceConnection(
  client: ServiceConnectionClient = Bridge,
  options: ServiceConnectionOptions = {},
) {
  const requestTimeoutMilliseconds =
    options.requestTimeoutMilliseconds ?? defaultRequestTimeoutMilliseconds;
  const healthCheckIntervalMilliseconds =
    options.healthCheckIntervalMilliseconds ?? defaultHealthCheckIntervalMilliseconds;
  const retryDelaysMilliseconds = options.retryDelaysMilliseconds?.length
    ? options.retryDelaysMilliseconds
    : defaultRetryDelaysMilliseconds;
  const phase = ref<ConnectionPhase>("connecting");
  const currentSession = ref<Session | null>(null);
  const errorMessage = ref("");
  const retryDelaySeconds = ref(0);
  const retryAttempt = ref(0);
  const healthStatus = ref("running");
	const baselineReview = ref<SessionBaselineReviewResult | null>(null);

  let stopped = false;
	let healthTimer: ReturnType<typeof setTimeout> | undefined;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let pendingHealthCheck: Promise<boolean> | undefined;
  const activeOperations = new Set<CancellableOperation<unknown>>();
  let sessionGeneration = 0;

  const connected = computed(() => phase.value === "online");
  const status = computed(() => {
    switch (phase.value) {
      case "online":
        return healthStatus.value === "degraded" ? "采集服务降级" : "后台服务正常";
      case "reconnecting":
        return `正在重连，${retryDelaySeconds.value} 秒后重试`;
      case "offline":
        return `后台服务离线，${retryDelaySeconds.value} 秒后重试`;
      default:
        return "正在连接后台服务";
    }
  });

  function start(): void {
    stopped = false;
    void refreshHealth(true);
		scheduleHealthCheck();
  }

  function stop(): void {
    stopped = true;
    if (retryTimer !== undefined) {
      clearTimeout(retryTimer);
      retryTimer = undefined;
    }
		if (healthTimer !== undefined) {
			clearTimeout(healthTimer);
			healthTimer = undefined;
		}
    for (const operation of activeOperations) {
      if (operation.cancel) void Promise.resolve(operation.cancel(new Error("window closed"))).catch(() => {});
    }
  }

  async function refreshHealth(initial = false): Promise<boolean> {
    if (pendingHealthCheck) return pendingHealthCheck;

    pendingHealthCheck = checkHealth(initial).finally(() => {
      pendingHealthCheck = undefined;
    });
    return pendingHealthCheck;
  }

  async function checkHealth(initial: boolean): Promise<boolean> {
    if (stopped) return false;
    clearRetryTimer();
    if (phase.value !== "online") {
      phase.value = initial && retryAttempt.value === 0 ? "connecting" : "reconnecting";
    }

    const observedGeneration = sessionGeneration;
    try {
      const health = await callWithTimeout(client.GetHealth());
      if (stopped) return false;

      if (observedGeneration === sessionGeneration) currentSession.value = health.session ?? null;
		if (health.session?.State !== SessionState.SessionStateBaselineReview) baselineReview.value = null;
		healthStatus.value = health.status;
      phase.value = "online";
      retryAttempt.value = 0;
      retryDelaySeconds.value = 0;
      errorMessage.value = "";
      return true;
    } catch (error) {
      if (stopped) return false;

      errorMessage.value = friendlyError(error);
      scheduleRetry();
      return false;
    }
  }

  async function createSession(id: string, name: string, level = MonitoringLevel.MonitoringLevelStandard): Promise<SessionResult> {
    return runSessionOperation(() => client.CreateSessionWithMonitoringLevel(id, name, level));
  }

	async function previewSessionStart(level: MonitoringLevel): Promise<SessionStartPreviewResult> {
		if (!connected.value) throw new Error("后台服务尚未连接");
		return callWithTimeout(client.GetSessionStartPreview(level));
	}

	async function loadBaselineReview(): Promise<SessionBaselineReviewResult | null> {
		const session = currentSession.value;
		if (!session || session.State !== SessionState.SessionStateBaselineReview) {
			baselineReview.value = null;
			return null;
		}
		try {
			const result = await callWithTimeout(client.GetSessionBaselineReview(session.ID));
			baselineReview.value = result;
			errorMessage.value = "";
			return result;
		} catch (error) {
			errorMessage.value = friendlyError(error);
			throw error;
		}
	}

	async function resolveBaselineReview(resolution: BaselineReviewResolution): Promise<SessionBaselineReviewResult> {
		const session = currentSession.value;
		if (!session || session.State !== SessionState.SessionStateBaselineReview) throw new Error("当前没有待处理的基线复核");
		try {
			const result = await callWithTimeout(
				client.ResolveSessionBaselineReview(session.ID, resolution),
				sessionRequestTimeoutMilliseconds,
			);
			currentSession.value = result.session ?? null;
			baselineReview.value = null;
			errorMessage.value = "";
			return result;
		} catch (error) {
			errorMessage.value = friendlyError(error);
			throw error;
		}
	}

  async function transitionSession(state: SessionState): Promise<SessionResult> {
    return runSessionOperation(() => client.TransitionSession(state), sessionRequestTimeoutMilliseconds);
  }

  async function endSession(): Promise<SessionResult> {
    // The native credential window is controlled by the user. Its network
    // requests retain the bridge/service deadlines; do not cancel the dialog.
    return runSessionOperation(() => client.EndSession(), 0);
  }

  async function performProtectionAction(id: string, name: string, level = MonitoringLevel.MonitoringLevelStandard): Promise<void> {
    const state = currentSession.value?.State;
    switch (state) {
      case SessionState.SessionStateActive:
      case SessionState.SessionStateDegraded:
        await endSession();
        await transitionSession(SessionState.SessionStateCompleted);
        return;
      case SessionState.SessionStateFinalizing:
        await transitionSession(SessionState.SessionStateCompleted);
        return;
      case SessionState.SessionStateDraft:
        await transitionSession(SessionState.SessionStatePreparing);
        await transitionSession(SessionState.SessionStateActive);
        return;
      case SessionState.SessionStatePreparing:
        await transitionSession(SessionState.SessionStateActive);
        return;
	  case SessionState.SessionStateBaselineReview:
		await loadBaselineReview();
		return;
      default:
		await createSession(id, name, level);
        await transitionSession(SessionState.SessionStatePreparing);
        await transitionSession(SessionState.SessionStateActive);
    }
  }

  async function runSessionOperation(
    operation: () => CancellableOperation<SessionResult>,
    timeoutMilliseconds = requestTimeoutMilliseconds,
  ): Promise<SessionResult> {
    if (!connected.value) {
      throw new Error("后台服务尚未连接");
    }

    sessionGeneration++;
    try {
      const result = await callWithTimeout(operation(), timeoutMilliseconds);
      if (!stopped) {
        currentSession.value = result.session ?? null;
        errorMessage.value = "";
      }
      return result;
    } catch (error) {
      if (stopped) throw error;
      const operationMessage = friendlyError(error);
      const stillOnline = await refreshHealth(false);
      errorMessage.value = operationMessage;
      if (!stillOnline) scheduleRetry();
      throw error;
    } finally {
      sessionGeneration++;
    }
  }

  function scheduleRetry(): void {
    if (stopped || retryTimer !== undefined) return;

    const index = Math.min(
      retryAttempt.value,
      retryDelaysMilliseconds.length - 1,
    );
    const delay = retryDelaysMilliseconds[index];
    retryAttempt.value += 1;
    retryDelaySeconds.value = Math.ceil(delay / 1_000);
    phase.value = retryAttempt.value >= retryDelaysMilliseconds.length
      ? "offline"
      : "reconnecting";
    retryTimer = setTimeout(() => {
      retryTimer = undefined;
      void refreshHealth(false);
    }, delay);
  }

	function scheduleHealthCheck(): void {
		if (stopped || healthTimer !== undefined) return;

		healthTimer = setTimeout(() => {
			healthTimer = undefined;
			void refreshHealth(false).finally(scheduleHealthCheck);
		}, healthCheckIntervalMilliseconds);
	}

  function clearRetryTimer(): void {
    if (retryTimer === undefined) return;
    clearTimeout(retryTimer);
    retryTimer = undefined;
  }

  async function callWithTimeout<T>(operation: CancellableOperation<T>, timeoutMilliseconds = requestTimeoutMilliseconds): Promise<T> {
    activeOperations.add(operation);
    let timer: ReturnType<typeof setTimeout> | undefined;
    const timeout = timeoutMilliseconds > 0 ? new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        const error = new RequestTimeoutError();
        if (operation.cancel) void Promise.resolve(operation.cancel(error)).catch(() => {});
        reject(error);
      }, timeoutMilliseconds);
    }) : undefined;

    try {
      return await (timeout ? Promise.race([operation, timeout]) : operation);
    } finally {
      if (timer !== undefined) clearTimeout(timer);
      activeOperations.delete(operation);
    }
  }

  return {
    connected,
	baselineReview,
    currentSession,
    errorMessage,
    healthStatus,
    phase,
    status,
    createSession,
    endSession,
    performProtectionAction,
	previewSessionStart,
	loadBaselineReview,
	resolveBaselineReview,
    refreshHealth,
    start,
    stop,
    transitionSession,
  };
}

function friendlyError(error: unknown): string {
  if (error instanceof RequestTimeoutError) return error.message;

  const raw = error instanceof Error ? error.message : String(error);
  if (raw.includes("service error")) {
    const separator = raw.indexOf(":");
    return separator >= 0 ? raw.slice(separator + 1).trim() : raw;
  }
  if (raw.toLowerCase().includes("cancel")) return "后台服务请求已取消";
  return "无法连接后台服务，桌面端将自动重试";
}
