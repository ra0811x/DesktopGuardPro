namespace DesktopGuardPro.Native;

internal sealed record AnalysisSession(string Id, string Name, string State, bool? AssetMonitoringEnabled = null);

internal sealed class ModeDraftStore<T> where T : notnull
{
    private readonly Dictionary<string, T> drafts = new();
    public void Keep(string mode, T value, T saved)
    {
        if (EqualityComparer<T>.Default.Equals(value, saved)) drafts.Remove(mode);
        else drafts[mode] = value;
    }
    public T Read(string mode, T saved) => drafts.TryGetValue(mode, out var draft) ? draft : saved;
    public void Remove(string mode) => drafts.Remove(mode);
}

internal sealed class AnalysisWorkspace
{
    public AnalysisSession? Session { get; private set; }
    public long Revision { get; private set; }

    public void Select(AnalysisSession session)
    {
        if (Session?.Id != session.Id) Revision++;
        Session = session;
    }

    public bool IsCurrent(string id, long revision) => Session?.Id == id && Revision == revision;
}

internal sealed class TimelinePagingState
{
    private string key = "";
    private string cursor = "";
    public string Begin(string queryKey, bool append)
    {
        if (!append || queryKey != key) cursor = "";
        key = queryKey;
        return cursor;
    }
    public void Complete(string? nextCursor) => cursor = nextCursor ?? "";
}

internal static class ReportSequenceRange
{
    public static bool TryParse(string first, string last, out ulong? from, out ulong? to, out string error)
    {
        from = to = null;
        error = "";
        if (string.IsNullOrWhiteSpace(first) && string.IsNullOrWhiteSpace(last)) return true;
        if (!ulong.TryParse(first, out var start) || !ulong.TryParse(last, out var end) || start == 0 || end < start)
        {
            error = "请同时填写有效的起止事件序号，起始序号至少为 1，结束序号不能小于起始序号。";
            return false;
        }
        if (end - start >= 100_000)
        {
            error = "每份报告最多 100,000 条事件，请缩小序号范围。";
            return false;
        }
        from = start;
        to = end;
        return true;
    }
}
