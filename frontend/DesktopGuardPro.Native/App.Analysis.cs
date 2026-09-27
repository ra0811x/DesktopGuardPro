using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace DesktopGuardPro.Native;

public partial class App
{
    private readonly AnalysisWorkspace analysisWorkspace = new();
    private readonly TimelinePagingState timelinePaging = new();
    private readonly List<TextBlock> analysisSessionLabels = new();
    private NavigationView? shellNavigation;
    private int timelineRequest;
    private int riskRequest;
    private int assetRequest;
    private string? pendingEvidenceId;
    private TextBox? reportFromSequence;
    private TextBox? reportToSequence;
    private readonly ModeDraftStore<MonitoringPolicyInfo> monitoringDrafts = new();
    private bool monitoringPolicyLoaded;

    private static AnalysisSession AnalysisSessionFrom(SessionInfo session) => new(
        session.Id, session.Name, session.State, session.MonitoringPolicy is { } policy
            ? policy.ProcessAndSoftwareEnabled || policy.SystemAndNetworkEnabled || policy.ExternalDevicesEnabled
            : null);

    private FrameworkElement CreateAnalysisSessionBar(string activePage)
    {
        var label = new TextBlock
        {
            Text = "选择会话后查看结果", TextWrapping = TextWrapping.Wrap,
            FontSize = 15, FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            Foreground = ThemeBrush("DgpDashboardTextBrush"),
        };
        analysisSessionLabels.Add(label);
        var history = CreateWorkspaceButton("选择历史会话");
        history.Click += async (_, _) => await NavigateAnalysisAsync("history");
        var current = CreateWorkspaceButton("当前 / 最近会话");
        current.Click += async (_, _) =>
        {
            try
            {
                var client = new ControlPipeClient();
                var session = (await client.GetHealthAsync(CancellationToken.None)).Session;
                session ??= (await client.ListSessionsAsync("", CancellationToken.None)).Items.FirstOrDefault()?.Session;
                if (session is null) { label.Text = "还没有保护记录，请先在仪表盘开启保护。"; return; }
                SelectAnalysisSession(AnalysisSessionFrom(session));
                await ReloadAnalysisPageAsync();
            }
            catch { label.Text = "无法读取会话，请检查服务后重试。"; }
        };
        var links = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        foreach (var (title, tag) in new[] { ("审计", "audit"), ("风险", "risk"), ("资产", "assets"), ("导出", "reports") })
        {
            var link = CreateWorkspaceButton(title, tag == activePage);
            Microsoft.UI.Xaml.Automation.AutomationProperties.SetHelpText(link,
                tag == activePage ? "当前页面" : $"查看所选会话的{title}");
            link.Click += async (_, _) => await NavigateAnalysisAsync(tag);
            links.Children.Add(link);
        }
        var selectors = new StackPanel
        {
            Orientation = Orientation.Horizontal, Spacing = 8, Children = { history, current },
        };
        return CreateWorkspaceNotice(new StackPanel
        {
            Spacing = 16,
            Children =
            {
                CreateWorkspaceToolbar(new StackPanel
                {
                    Spacing = 6, Children = { CreateWorkspaceNote("查看会话"), label },
                }, selectors),
                links,
            },
        });
    }

    private void SelectAnalysisSession(AnalysisSession session)
    {
        var changed = analysisWorkspace.Session?.Id != session.Id;
        analysisWorkspace.Select(session);
        foreach (var label in analysisSessionLabels)
        {
            label.Text = $"{session.Name} · {FormatSessionState(session.State)}";
            ToolTipService.SetToolTip(label, $"会话标识：{session.Id}");
        }
        if (!changed) return;
        timelineRequest++; riskRequest++; assetRequest++;
        timelineCursor = ""; timelineHasMore = false;
        timelinePaging.Begin("", false);
        pendingEvidenceId = null;
        if (timelineCategoryFilter is not null) timelineCategoryFilter.SelectedIndex = 0;
        if (timelineSeverityFilter is not null) timelineSeverityFilter.SelectedIndex = 0;
        if (timelineUserFilter is not null) timelineUserFilter.Text = "";
        if (timelineProcessFilter is not null) timelineProcessFilter.Text = "";
        if (timelinePathFilter is not null) timelinePathFilter.Text = "";
        if (timelineFromFilter is not null) timelineFromFilter.Text = "";
        if (timelineToFilter is not null) timelineToFilter.Text = "";
        timelineList?.Items.Clear(); riskList?.Items.Clear(); assetList?.Items.Clear();
        if (timelineDetail is not null) timelineDetail.Text = "选择一条事件以查看详情。";
        if (assetDetail is not null) assetDetail.Text = "选择一项资产变化以查看详情。";
        if (loadMoreTimelineButton is not null) loadMoreTimelineButton.IsEnabled = false;
        if (reportFromSequence is not null) reportFromSequence.Text = "";
        if (reportToSequence is not null) reportToSequence.Text = "";
        if (reportStatus is not null) reportStatus.Text = "将导出上方所选会话；留空序号表示整个会话。";
    }

    private async Task<AnalysisSession?> ResolveAnalysisSessionAsync(ControlPipeClient client)
    {
        if (analysisWorkspace.Session is { } selected) return selected;
        var session = (await client.GetHealthAsync(CancellationToken.None)).Session;
        session ??= (await client.ListSessionsAsync("", CancellationToken.None)).Items.FirstOrDefault()?.Session;
        // A user may open history while the initial lookup is still in flight.
        if (analysisWorkspace.Session is null && session is not null)
            SelectAnalysisSession(AnalysisSessionFrom(session));
        return analysisWorkspace.Session;
    }

    private async Task NavigateAnalysisAsync(string tag)
    {
        if (shellNavigation is null) return;
        var target = shellNavigation.MenuItems.OfType<NavigationViewItem>().First(item => Equals(item.Tag, tag));
        if (ReferenceEquals(shellNavigation.SelectedItem, target)) await ReloadAnalysisPageAsync();
        else shellNavigation.SelectedItem = target;
    }

    private async Task ReloadAnalysisPageAsync()
    {
        switch ((shellNavigation?.SelectedItem as NavigationViewItem)?.Tag)
        {
            case "history": await LoadHistoryAsync(false); break;
            case "audit": await LoadTimelineAsync(false); break;
            case "risk": await LoadRiskAsync(); break;
            case "assets": await LoadAssetDifferencesAsync(); break;
            case "reports":
                try { await ResolveAnalysisSessionAsync(new ControlPipeClient()); }
                catch { if (reportStatus is not null) reportStatus.Text = "无法读取会话，请选择历史记录或检查服务。"; }
                break;
        }
    }

    private async Task OpenRiskEvidenceAsync(AnalysisSession session, RiskEvidence evidence)
    {
        SelectAnalysisSession(session);
        if (timelineCategoryFilter is not null) timelineCategoryFilter.SelectedIndex = 0;
        if (timelineSeverityFilter is not null) timelineSeverityFilter.SelectedIndex = 0;
        if (timelineUserFilter is not null) timelineUserFilter.Text = "";
        if (timelineProcessFilter is not null) timelineProcessFilter.Text = "";
        if (timelinePathFilter is not null) timelinePathFilter.Text = "";
        // Include a tick of tolerance for the sub-microsecond precision of Go timestamps.
        if (timelineFromFilter is not null) timelineFromFilter.Text = evidence.ObservedUtc.AddTicks(-1).ToString("O");
        if (timelineToFilter is not null) timelineToFilter.Text = evidence.ObservedUtc.AddTicks(1).ToString("O");
        pendingEvidenceId = evidence.EventId;
        await NavigateAnalysisAsync("audit");
    }

    private async Task ExportSelectedEventAsync()
    {
        if (timelineList?.SelectedItem is not ListViewItem { Tag: ValueTuple<AuditEventInfo, string?, string?, bool> selected })
        {
            if (timelineStatus is not null) timelineStatus.Text = "先选择一条事件，再导出其证据报告。";
            return;
        }
        if (reportFromSequence is not null) reportFromSequence.Text = selected.Item1.Sequence.ToString();
        if (reportToSequence is not null) reportToSequence.Text = selected.Item1.Sequence.ToString();
        await NavigateAnalysisAsync("reports");
    }
}
