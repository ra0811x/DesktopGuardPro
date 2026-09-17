import { afterEach, describe, expect, it, vi } from "vitest";

import { BaselineCaptureStatus, MonitoringImpactLevel, MonitoringLevel, SessionState } from "../bindings/desktopguardpro/internal/domain";
import { SessionBaselineReviewResult } from "../bindings/desktopguardpro/internal/service";
import { BaselineReviewResolution } from "../bindings/desktopguardpro/internal/storage";
import {
  type CancellableOperation,
  type ServiceConnectionClient,
  useServiceConnection,
} from "./serviceConnection";

function createClient(
  getHealth: ServiceConnectionClient["GetHealth"] = vi.fn(() =>
    Promise.resolve({ status: "running", maintenanceGate: true }),
  ),
): ServiceConnectionClient {
  return {
    GetHealth: getHealth,
    CreateSession: vi.fn(() => Promise.resolve({ session: null })),
	CreateSessionWithMonitoringLevel: vi.fn(() => Promise.resolve({ session: null })),
	GetSessionStartPreview: vi.fn((level) => Promise.resolve({
		monitoringLevel: level,
		monitoredTargetCount: 0,
		targets: [],
		impact: {
			requiresAdministrator: false,
			expectedEventVolume: MonitoringImpactLevel.MonitoringImpactMedium,
			performanceImpact: MonitoringImpactLevel.MonitoringImpactLow,
			storageImpact: MonitoringImpactLevel.MonitoringImpactMedium,
		},
	})),
	GetSessionBaselineReview: vi.fn((sessionID) => Promise.resolve(new SessionBaselineReviewResult({
		sessionId: sessionID,
		decision: { status: BaselineCaptureStatus.BaselineCaptureStatusPartialFailure, failures: [], requiresUserChoice: true, canContinue: true, canCancel: true },
	}))),
	ResolveSessionBaselineReview: vi.fn((sessionID, resolution) => Promise.resolve(new SessionBaselineReviewResult({
		sessionId: sessionID,
		resolution,
		decision: { status: BaselineCaptureStatus.BaselineCaptureStatusPartialFailure, failures: [], requiresUserChoice: true, canContinue: true, canCancel: true },
		session: { ID: sessionID, Name: "保护", State: SessionState.SessionStateActive, Revision: 3, MonitoringLevel: MonitoringLevel.MonitoringLevelStandard },
	}))),
    EndSession: vi.fn(() => Promise.resolve({ session: null })),
    TransitionSession: vi.fn(() => Promise.resolve({ session: null })),
  };
}

async function flushPromises(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await Promise.resolve();
}

afterEach(() => {
  vi.useRealTimers();
});

describe("useServiceConnection", () => {
  it("does not time out the native credential prompt after five seconds", async () => {
    vi.useFakeTimers();
    const client = createClient();
    let resolvePrompt!: (value: { session: null }) => void;
    const prompt = new Promise<{ session: null }>((resolve) => { resolvePrompt = resolve; }) as CancellableOperation<{ session: null }>;
    prompt.cancel = vi.fn();
    client.EndSession = vi.fn(() => prompt);
    const connection = useServiceConnection(client);
    await connection.refreshHealth();
    let settled = false;
    const operation = connection.endSession().then(() => { settled = true; }, () => { settled = true; });
    await vi.advanceTimersByTimeAsync(6_000);
    expect(settled).toBe(false);
    expect(prompt.cancel).not.toHaveBeenCalled();
    resolvePrompt({ session: null });
    await operation;
    connection.stop();
  });

  it("allows collector finalization beyond the normal request timeout", async () => {
    vi.useFakeTimers();
    const client = createClient();
    let resolveTransition!: (value: { session: null }) => void;
    client.TransitionSession = vi.fn(() => new Promise<{ session: null }>((resolve) => { resolveTransition = resolve; }));
    const connection = useServiceConnection(client);
    await connection.refreshHealth();
    let failed = false;
    const operation = connection.transitionSession(SessionState.SessionStateCompleted).catch(() => { failed = true; });
    await vi.advanceTimersByTimeAsync(6_000);
    expect(failed).toBe(false);
    resolveTransition({ session: null });
    await operation;
    connection.stop();
  });

  it("does not overwrite a completed transition with an older health response", async () => {
	const active = { ID: "live", Name: "保护", State: SessionState.SessionStateActive, Revision: 2, MonitoringLevel: MonitoringLevel.MonitoringLevelStandard };
    const finalizing = { ...active, State: SessionState.SessionStateFinalizing, Revision: 3 };
    let resolveHealth!: (value: { status: string; maintenanceGate: boolean; session: typeof active }) => void;
    let resolveTransition!: (value: { session: typeof finalizing }) => void;
    const client = createClient(vi.fn().mockResolvedValueOnce({ status: "running", maintenanceGate: true, session: active }).mockImplementationOnce(() => new Promise((resolve) => { resolveHealth = resolve; })));
    client.TransitionSession = vi.fn(() => new Promise<{ session: typeof finalizing }>((resolve) => { resolveTransition = resolve; }));
    const connection = useServiceConnection(client);
    await connection.refreshHealth();
    const transition = connection.transitionSession(SessionState.SessionStateFinalizing);
    const health = connection.refreshHealth();
    resolveTransition({ session: finalizing });
    await transition;
    resolveHealth({ status: "running", maintenanceGate: true, session: active });
    await health;
    expect(connection.currentSession.value?.State).toBe(SessionState.SessionStateFinalizing);
    connection.stop();
  });

  it.each([
    [SessionState.SessionStateDraft, [SessionState.SessionStatePreparing, SessionState.SessionStateActive]],
    [SessionState.SessionStatePreparing, [SessionState.SessionStateActive]],
    [SessionState.SessionStateFinalizing, [SessionState.SessionStateCompleted]],
  ])("continues a restored %s session without creating another", async (state, transitions) => {
	const session = { ID: "restored", Name: "恢复会话", State: state, Revision: 1, MonitoringLevel: MonitoringLevel.MonitoringLevelStandard };
    const client = createClient(vi.fn(() => Promise.resolve({ status: "running", maintenanceGate: true, session: { ...session } })));
    client.TransitionSession = vi.fn((next) => Promise.resolve({ session: { ...session, State: next } }));
    const connection = useServiceConnection(client);
    await connection.refreshHealth();
    await connection.performProtectionAction("unused-new-id", "新会话");
	expect(client.CreateSessionWithMonitoringLevel).not.toHaveBeenCalled();
    expect(vi.mocked(client.TransitionSession).mock.calls.map(([next]) => next)).toEqual(transitions);
    connection.stop();
  });

  it("continues from the persisted state after an acknowledgement is lost", async () => {
	const session = { ID: "restored", Name: "恢复会话", State: SessionState.SessionStateDraft, Revision: 0, MonitoringLevel: MonitoringLevel.MonitoringLevelStandard };
    const client = createClient(vi.fn(() => Promise.resolve({ status: "running", maintenanceGate: true, session: { ...session } })));
    client.TransitionSession = vi.fn((next) => {
      session.State = next;
      if (next === SessionState.SessionStatePreparing) return Promise.reject(new Error("response lost"));
      return Promise.resolve({ session: { ...session } });
    });
    const connection = useServiceConnection(client);
    await connection.refreshHealth();
    await expect(connection.performProtectionAction("unused", "新会话")).rejects.toThrow();
    await connection.performProtectionAction("unused", "新会话");
	expect(client.CreateSessionWithMonitoringLevel).not.toHaveBeenCalled();
    expect(connection.currentSession.value?.State).toBe(SessionState.SessionStateActive);
    connection.stop();
  });

	it("loads the strict start preview and creates a strict session", async () => {
		const client = createClient();
		const connection = useServiceConnection(client);
		await connection.refreshHealth();
		const preview = await connection.previewSessionStart(MonitoringLevel.MonitoringLevelStrict);
		expect(preview.monitoringLevel).toBe(MonitoringLevel.MonitoringLevelStrict);
		await connection.performProtectionAction("strict-1", "严格保护", MonitoringLevel.MonitoringLevelStrict);
		expect(client.GetSessionStartPreview).toHaveBeenCalledWith(MonitoringLevel.MonitoringLevelStrict);
		expect(client.CreateSessionWithMonitoringLevel).toHaveBeenCalledWith("strict-1", "严格保护", MonitoringLevel.MonitoringLevelStrict);
		connection.stop();
	});

	it("loads partial baseline failures and resolves the user's choice", async () => {
		const reviewSession = {
			ID: "review-1", Name: "基线复核", State: SessionState.SessionStateBaselineReview,
			Revision: 2, MonitoringLevel: MonitoringLevel.MonitoringLevelStandard,
		};
		const client = createClient(vi.fn(() => Promise.resolve({ status: "running", maintenanceGate: true, session: reviewSession })));
		const connection = useServiceConnection(client);
		await connection.refreshHealth();

		await connection.loadBaselineReview();
		expect(connection.baselineReview.value?.decision.canContinue).toBe(true);
		await connection.resolveBaselineReview(BaselineReviewResolution.BaselineReviewResolutionContinue);

		expect(client.ResolveSessionBaselineReview).toHaveBeenCalledWith("review-1", BaselineReviewResolution.BaselineReviewResolutionContinue);
		expect(connection.currentSession.value?.State).toBe(SessionState.SessionStateActive);
		expect(connection.baselineReview.value).toBeNull();
		connection.stop();
	});

  it("connects and synchronises the current session", async () => {
    const session = {
      ID: "session-1",
      Name: "离席保护",
      State: SessionState.SessionStateActive,
      Revision: 2,
	  MonitoringLevel: MonitoringLevel.MonitoringLevelStandard,
    };
    const client = createClient(vi.fn(() =>
      Promise.resolve({ status: "running", maintenanceGate: true, session }),
    ));
    const connection = useServiceConnection(client);

    connection.start();
    await flushPromises();

    expect(connection.phase.value).toBe("online");
    expect(connection.connected.value).toBe(true);
    expect(connection.currentSession.value).toEqual(session);
    connection.stop();
  });

  it("retries after failure and synchronises state after recovery", async () => {
    vi.useFakeTimers();
    const session = {
      ID: "session-restored",
      Name: "恢复会话",
      State: SessionState.SessionStatePreparing,
      Revision: 1,
	  MonitoringLevel: MonitoringLevel.MonitoringLevelStandard,
    };
    const getHealth = vi.fn()
      .mockRejectedValueOnce(new Error("pipe unavailable"))
      .mockResolvedValue({ status: "running", maintenanceGate: true, session });
    const connection = useServiceConnection(createClient(getHealth), {
      retryDelaysMilliseconds: [100, 200],
    });

    connection.start();
    await flushPromises();
    expect(connection.phase.value).toBe("reconnecting");
    expect(connection.status.value).toContain("1 秒后重试");

    await vi.advanceTimersByTimeAsync(100);
    await flushPromises();
    expect(connection.phase.value).toBe("online");
    expect(connection.currentSession.value).toEqual(session);
    expect(getHealth).toHaveBeenCalledTimes(2);
    connection.stop();
  });

  it("periodically reflects collector degradation", async () => {
    vi.useFakeTimers();
    const getHealth = vi.fn()
      .mockResolvedValueOnce({ status: "running", maintenanceGate: true })
      .mockResolvedValueOnce({ status: "degraded", maintenanceGate: true })
      .mockResolvedValueOnce({ status: "running", maintenanceGate: true });
    const connection = useServiceConnection(createClient(getHealth), {
      healthCheckIntervalMilliseconds: 100,
    });

    connection.start();
    await flushPromises();
    expect(connection.status.value).toBe("后台服务正常");

    await vi.advanceTimersByTimeAsync(100);
    await flushPromises();
    expect(connection.phase.value).toBe("online");
    expect(connection.status.value).toBe("采集服务降级");
    expect(getHealth).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(100);
    await flushPromises();
    expect(connection.status.value).toBe("后台服务正常");
    expect(getHealth).toHaveBeenCalledTimes(3);
    connection.stop();
  });

  it("stays online while a routine health check is pending", async () => {
    vi.useFakeTimers();
    let resolveRoutineCheck!: (value: { status: string; maintenanceGate: boolean }) => void;
    const routineCheck = new Promise<{ status: string; maintenanceGate: boolean }>((resolve) => {
      resolveRoutineCheck = resolve;
    });
    const getHealth = vi.fn()
      .mockResolvedValueOnce({ status: "running", maintenanceGate: true })
      .mockImplementationOnce(() => routineCheck);
    const connection = useServiceConnection(createClient(getHealth), {
      healthCheckIntervalMilliseconds: 100,
    });

    connection.start();
    await flushPromises();
    expect(connection.phase.value).toBe("online");

    await vi.advanceTimersByTimeAsync(100);
    expect(getHealth).toHaveBeenCalledTimes(2);
    expect(connection.phase.value).toBe("online");
    expect(connection.connected.value).toBe(true);

    resolveRoutineCheck({ status: "running", maintenanceGate: true });
    await flushPromises();
    expect(connection.phase.value).toBe("online");
    connection.stop();
  });

  it("cancels an operation when the request times out", async () => {
    vi.useFakeTimers();
    const client = createClient();
    const pending = new Promise<never>(() => {}) as CancellableOperation<never>;
    pending.cancel = vi.fn();
	client.CreateSessionWithMonitoringLevel = vi.fn(() => pending);
    const connection = useServiceConnection(client, {
      requestTimeoutMilliseconds: 50,
      retryDelaysMilliseconds: [100],
    });
    connection.start();
    await flushPromises();

    const request = connection.createSession("session-1", "超时会话");
		const rejection = expect(request).rejects.toThrow("后台服务请求超时");
    await vi.advanceTimersByTimeAsync(50);
		await rejection;
    expect(pending.cancel).toHaveBeenCalledOnce();
    expect(connection.errorMessage.value).toBe("后台服务请求超时");
    connection.stop();
  });

  it("uses the native system credential flow to end a protection session", async () => {
    const session = {
      ID: "session-1",
      Name: "离席保护",
      State: SessionState.SessionStateFinalizing,
      Revision: 3,
	  MonitoringLevel: MonitoringLevel.MonitoringLevelStandard,
    };
    const client = createClient();
    client.EndSession = vi.fn(() => Promise.resolve({ session }));
    const connection = useServiceConnection(client);

    connection.start();
    await flushPromises();
    await connection.endSession();

    expect(client.EndSession).toHaveBeenCalledOnce();
    expect(connection.currentSession.value).toEqual(session);
    connection.stop();
  });
});
