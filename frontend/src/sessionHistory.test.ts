import { ref } from "vue";
import { describe, expect, it, vi } from "vitest";
import { Session, SessionState } from "../bindings/desktopguardpro/internal/domain";
import { useSessionHistory, type SessionHistoryPage } from "./sessionHistory";

const session = (ID: string) => new Session({ ID, Name: ID, State: SessionState.SessionStateCompleted, Revision: 4 });
const page = (ID: string, nextCursor?: string): SessionHistoryPage => ({
  items: [{ session: session(ID), createdUtc: "2026-09-04T01:00:00Z", updatedUtc: "2026-09-04T02:00:00Z", retentionLocked: false }],
  hasMore: !!nextCursor, nextCursor,
});

describe("historical sessions", () => {
  it("loads and pages without a current session", async () => {
    const client = { ListSessions: vi.fn().mockResolvedValueOnce(page("recent", "5")).mockResolvedValueOnce(page("older")) };
    const state = useSessionHistory(ref(null), client);
    await state.load();
    await state.loadMore();
    expect(state.items.value.map((item) => item.session.ID)).toEqual(["recent", "older"]);
    expect(client.ListSessions).toHaveBeenNthCalledWith(2, "5");
  });

  it("keeps historical viewing separate from live protection", () => {
    const current = ref<Session | null>(session("live"));
    const state = useSessionHistory(current, { ListSessions: vi.fn() });
    state.select(session("old"));
    current.value = session("new-live");
    expect(state.viewedSession.value?.ID).toBe("old");
    expect(current.value.ID).toBe("new-live");
    state.viewCurrent();
    expect(state.viewedSession.value?.ID).toBe("new-live");
  });

  it("ignores an old response after refresh or window closure", async () => {
    let resolveOld!: (value: SessionHistoryPage) => void;
    const client = { ListSessions: vi.fn().mockReturnValueOnce(new Promise<SessionHistoryPage>((resolve) => { resolveOld = resolve; })).mockResolvedValueOnce(page("latest")) };
    const state = useSessionHistory(ref(null), client);
    const old = state.load();
    await state.load();
    resolveOld(page("stale"));
    await old;
    expect(state.items.value[0]?.session.ID).toBe("latest");
    state.stop();
    await state.load();
    expect(client.ListSessions).toHaveBeenCalledTimes(2);
  });

  it("updates a retention lock and removes sessions returned by cleanup", async () => {
    const client = {
      ListSessions: vi.fn().mockResolvedValue(page("expired")),
      UpdateSessionRetentionLock: vi.fn().mockResolvedValue({ sessionId: "expired", locked: true }),
      PruneSessionRetention: vi.fn().mockResolvedValue({ deletedSessionIds: ["expired"] }),
    };
    const state = useSessionHistory(ref(null), client);
    await state.load();
    await state.updateRetentionLock("expired", true);
    expect(state.items.value[0]?.retentionLocked).toBe(true);
    await state.prune("2026-08-01T00:00:00.000Z");
    expect(state.items.value).toEqual([]);
    expect(state.retentionFeedback.value).toContain("1");
  });
});
