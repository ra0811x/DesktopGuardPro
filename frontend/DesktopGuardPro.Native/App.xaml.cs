using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Microsoft.UI.Windowing;
using StatusEllipse = Microsoft.UI.Xaml.Shapes.Ellipse;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Windows.Graphics;
using Windows.Storage;
using Windows.Storage.Pickers;
using Windows.System;

namespace DesktopGuardPro.Native;

public partial class App : Application
{
    private const int DefaultWindowWidth = 1734;
    private const int DefaultWindowHeight = 1070;
    private const int MinimumWindowWidth = 1280;
    private const int MinimumWindowHeight = 760;
    private const double InterfaceScale = 0.8;
    private const double CompactLayoutWidth = 512;
    private const double BrandLabelVisibilityWidth = 608;
    private const double ExpandedLayoutWidth = 800;
    private const int WindowLongWindowProcedure = -4;
    private const uint WindowMessageGetMinMaxInfo = 0x0024;

    private Window? mainWindow;
    private IntPtr windowHandle;
    private IntPtr originalWindowProcedure;
    private WindowProcedure? windowProcedure;
    private FrameworkElement? titleBarDragRegion;
    private NativeTrayIcon? trayIcon;
    private TextBlock? serviceStatus;
    private TextBlock? globalServiceStatusText;
    private StatusEllipse? globalServiceStatusIndicator;
    private TextBlock? globalProtectionStatusText;
    private StatusEllipse? globalProtectionStatusIndicator;
    private DispatcherTimer? statusRefreshTimer;
    private bool healthRefreshBusy;
    private bool useCompactGlobalStatusLabels;
    private string globalServiceStatusFullText = "服务：连接中";
    private string globalServiceStatusCompactText = "服务：连接";
    private string globalProtectionStatusFullText = "保护：未开启";
    private string globalProtectionStatusCompactText = "保护：关闭";
    private StackPanel? directoryInputs;
    private TextBlock? directoryStatus;
    private StackPanel? exclusionInputs;
    private TextBlock? exclusionStatus;
    private ToggleSwitch? inputActivityEnabled;
    private TextBlock? inputActivityStatus;
    private ToggleSwitch? windowTitleEnabled;
    private TextBlock? windowTitleStatus;
    private ToggleSwitch? highRiskShortcutsEnabled;
    private TextBlock? highRiskShortcutsStatus;
    private ToggleSwitch? fileActivityPolicyEnabled;
    private ToggleSwitch? processAndSoftwarePolicyEnabled;
    private ToggleSwitch? systemAndNetworkPolicyEnabled;
    private ToggleSwitch? externalDevicesPolicyEnabled;
    private ToggleSwitch? userSessionActivityPolicyEnabled;
    private ToggleSwitch? strictReadAuditPolicyEnabled;
    private MonitoringPolicyInfo currentMonitoringPolicy = MonitoringPolicyInfo.CreateDefault();
    private MonitoringProfilesInfo? currentMonitoringProfiles;
    private string editingMonitoringMode = "custom";
    private ComboBox? settingsMonitoringMode;
    private TextBlock? filePolicyDetailSummary;
    private TextBlock? processPolicyDetailSummary;
    private TextBlock? systemPolicyDetailSummary;
    private TextBlock? devicePolicyDetailSummary;
    private TextBlock? userSessionPolicyDetailSummary;
    private TextBlock? monitoringPolicySummary;
    private TextBlock? monitoringPolicyStatus;
    private TextBlock? inputShieldRuntimeStatus;
    private TextBlock? inputShieldCredentialStatus;
    private TextBlock? inputShieldUnlockHint;
    private TextBlock? temporaryInputUnlockHint;
    private IReadOnlyList<InputShieldDeviceInfo> inputShieldDevices = Array.Empty<InputShieldDeviceInfo>();
    private bool inputShieldDevicesAvailable;
    private NumberBox? temporaryInputDuration;
    private ComboBox? temporaryInputDurationMode;
    private TextBlock? temporaryInputControlStatus;
    private Button? startTemporaryInputControlButton;
    private Button? stopTemporaryInputControlButton;
    private ComboBox? monitoringMode;
    private TextBlock? sessionStartPreviewStatus;
    private TextBlock? dashboardModeSummary;
    private TextBlock? dashboardTargetSummary;
    private Border? dashboardProtectionBadge;
    private Expander? dashboardScopeExpander;
    private TextBox? sessionName;
    private TextBlock? sessionStatus;
    private Button? protectionButton;
    private Button? pauseProtectionButton;
    private ListView? historyList;
    private TextBlock? historyStatus;
    private Button? loadMoreHistoryButton;
    private string historyCursor = "";
    private bool historyHasMore;
    private bool historyLoading;
    private ListView? timelineList;
    private TextBlock? timelineStatus;
    private TextBlock? timelineDetail;
    private Button? loadMoreTimelineButton;
    private string timelineCursor = "";
    private bool timelineHasMore;
    private ComboBox? timelineCategoryFilter;
    private ComboBox? timelineSeverityFilter;
    private TextBox? timelineUserFilter;
    private TextBox? timelineProcessFilter;
    private TextBox? timelinePathFilter;
    private TextBox? timelineFromFilter;
    private TextBox? timelineToFilter;
    private ListView? riskList;
    private TextBlock? riskStatus;
    private ListView? assetList;
    private TextBlock? assetStatus;
    private TextBlock? assetDetail;
    private readonly HashSet<string> assetCategories = new(StringComparer.Ordinal);
    private ComboBox? reportFormat;
    private ComboBox? reportObjectDetails;
    private CheckBox? reportIncludeProcessKey;
    private CheckBox? reportIncludePayload;
    private CheckBox? reportIncludeUsernames;
    private CheckBox? reportIncludeWindowTitles;
    private CheckBox? reportSensitiveAcknowledgement;
    private TextBlock? reportStatus;
    private bool reportExporting;
    private bool protectionBusy;

    public App()
    {
        InitializeComponent();
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        LoadUiPreferences();
        var shell = CreateShell(
            out serviceStatus,
            out directoryInputs,
            out directoryStatus,
            out exclusionInputs,
            out exclusionStatus,
            out monitoringMode,
            out sessionStartPreviewStatus,
            out sessionName,
            out sessionStatus,
            out protectionButton,
            out historyList,
            out historyStatus,
            out loadMoreHistoryButton,
            out timelineList,
            out timelineStatus,
            out timelineDetail,
            out loadMoreTimelineButton,
            out riskList,
            out riskStatus,
            out assetList,
            out assetStatus,
            out assetDetail,
            out reportFormat,
            out reportObjectDetails,
            out reportIncludeProcessKey,
            out reportIncludePayload,
            out reportSensitiveAcknowledgement,
            out reportStatus,
            RefreshHealthAsync,
            SaveDirectoriesAsync,
            PerformProtectionActionAsync,
            LoadHistoryAsync,
            LoadTimelineAsync,
            LoadRiskAsync,
            LoadAssetDifferencesAsync,
            ExportReportAsync);
        mainWindow = new Window
        {
            Title = "Desktop Guard Pro",
            Content = shell,
        };
        mainWindow.ExtendsContentIntoTitleBar = true;
        if (titleBarDragRegion is not null)
        {
            mainWindow.SetTitleBar(titleBarDragRegion);
        }
        ConfigureTitleBar(mainWindow);
        ConfigureWindowSize(mainWindow);
        mainWindow.Activate();
        EnsureInteractiveAgentStarted();
        trayIcon = NativeTrayIcon.Create(mainWindow, "Desktop Guard Pro", RestoreMainWindow, ExitMainWindow);
        mainWindow.Closed += (_, _) =>
        {
            startupConnectionCancellation.Cancel();
            statusRefreshTimer?.Stop();
            RestoreWindowProcedure();
            trayIcon?.Dispose();
        };
        _ = InitializeServiceViewsAsync();
        statusRefreshTimer = new DispatcherTimer { Interval = TimeSpan.FromSeconds(5) };
        statusRefreshTimer.Tick += async (_, _) => await RefreshHealthAsync();
        ConfigureWindowPreferences();
    }

    private static void EnsureInteractiveAgentStarted()
    {
        try
        {
            var currentSession = System.Diagnostics.Process.GetCurrentProcess().SessionId;
            foreach (var process in System.Diagnostics.Process.GetProcessesByName("desktop-guard-agent"))
            {
                using (process)
                {
                    try
                    {
                        if (process.SessionId == currentSession)
                        {
                            return;
                        }
                    }
                    catch
                    {
                        // The process may exit while it is being inspected.
                    }
                }
            }
            var directory = AppContext.BaseDirectory;
            var executable = Path.Combine(directory, "desktop-guard-agent.exe");
            if (!File.Exists(executable))
            {
                return;
            }
            _ = System.Diagnostics.Process.Start(new System.Diagnostics.ProcessStartInfo
            {
                FileName = executable,
                Arguments = "--ui-launch",
                WorkingDirectory = directory,
                UseShellExecute = false,
                CreateNoWindow = true,
                WindowStyle = System.Diagnostics.ProcessWindowStyle.Hidden,
            });
        }
        catch
        {
            // Health and system settings expose Agent availability to the user.
        }
    }

    private static void ConfigureTitleBar(Window window)
    {
        var titleBar = window.AppWindow.TitleBar;
        var background = Windows.UI.Color.FromArgb(255, 8, 43, 94);
        var hover = Windows.UI.Color.FromArgb(255, 16, 69, 127);
        var pressed = Windows.UI.Color.FromArgb(255, 11, 57, 119);
        var foreground = Windows.UI.Color.FromArgb(255, 255, 255, 255);
        var inactiveForeground = Windows.UI.Color.FromArgb(255, 191, 208, 230);
        titleBar.BackgroundColor = background;
        titleBar.InactiveBackgroundColor = background;
        titleBar.ButtonBackgroundColor = background;
        titleBar.ButtonInactiveBackgroundColor = background;
        titleBar.ButtonHoverBackgroundColor = hover;
        titleBar.ButtonPressedBackgroundColor = pressed;
        titleBar.ButtonForegroundColor = foreground;
        titleBar.ButtonInactiveForegroundColor = inactiveForeground;
        titleBar.ButtonHoverForegroundColor = foreground;
        titleBar.ButtonPressedForegroundColor = foreground;
    }

    private void ConfigureWindowSize(Window window)
    {
        windowHandle = WinRT.Interop.WindowNative.GetWindowHandle(window);
        if (windowHandle != IntPtr.Zero)
        {
            windowProcedure = HandleWindowMessage;
            originalWindowProcedure = SetWindowLongPtr(
                windowHandle,
                WindowLongWindowProcedure,
                Marshal.GetFunctionPointerForDelegate(windowProcedure));
        }
        var targetWidth = DefaultWindowWidth;
        var targetHeight = DefaultWindowHeight;
        var displayArea = DisplayArea.GetFromWindowId(window.AppWindow.Id, DisplayAreaFallback.Primary);
        if (displayArea is not null)
        {
            var workArea = displayArea.WorkArea;
            targetWidth = Math.Min(targetWidth, Math.Max(1, (int)Math.Floor(workArea.Width * 0.92)));
            targetHeight = Math.Min(targetHeight, Math.Max(1, (int)Math.Floor(workArea.Height * 0.92)));
        }
        window.AppWindow.Resize(new SizeInt32(targetWidth, targetHeight));
    }

    private IntPtr HandleWindowMessage(IntPtr handle, uint message, IntPtr wParam, IntPtr lParam)
    {
        if (trayIcon?.TryHandleMessage(message, lParam) == true) return IntPtr.Zero;
        if (message == WindowMessageGetMinMaxInfo && lParam != IntPtr.Zero)
        {
            var sizeInfo = Marshal.PtrToStructure<MinMaxInfo>(lParam);
            sizeInfo.MinimumTrackingSize = new NativePoint
            {
                X = MinimumWindowWidth,
                Y = MinimumWindowHeight,
            };
            Marshal.StructureToPtr(sizeInfo, lParam, false);
        }
        return originalWindowProcedure == IntPtr.Zero
            ? IntPtr.Zero
            : CallWindowProcedure(originalWindowProcedure, handle, message, wParam, lParam);
    }

    private void RestoreWindowProcedure()
    {
        if (windowHandle != IntPtr.Zero && originalWindowProcedure != IntPtr.Zero)
        {
            _ = SetWindowLongPtr(windowHandle, WindowLongWindowProcedure, originalWindowProcedure);
        }
        originalWindowProcedure = IntPtr.Zero;
        windowProcedure = null;
        windowHandle = IntPtr.Zero;
    }

    private async Task<bool> RefreshHealthAsync()
    {
        if (serviceStatus is null || healthRefreshBusy)
        {
            return false;
        }
        healthRefreshBusy = true;
        serviceStatus.Text = "正在连接后台服务...";
        UpdateGlobalServiceStatus("connecting");
        try
        {
            var health = await new ControlPipeClient().GetHealthAsync(CancellationToken.None);
            serviceStatus.Text = health.Status == "degraded"
                ? "后台服务降级运行"
                : "后台服务正常";
            UpdateGlobalServiceStatus(health.Status == "degraded" ? "degraded" : "running");
            ApplySession(health.Session);
            await LoadTemporaryInputControlAsync();
            return true;
        }
        catch
        {
            serviceStatus.Text = "无法连接后台服务";
            UpdateGlobalServiceStatus("offline");
            ApplySession(null);
            return false;
        }
        finally
        {
            healthRefreshBusy = false;
        }
    }

    private async Task PerformProtectionActionAsync()
    {
        if (protectionBusy || protectionButton is null || sessionStatus is null)
        {
            return;
        }
        protectionBusy = true;
        protectionButton.IsEnabled = false;
        sessionStatus.Text = "正在处理保护会话...";
        SessionInfo? updatedSession = null;
        var completed = false;
        try
        {
            var client = new ControlPipeClient();
            var health = await client.GetHealthAsync(CancellationToken.None);
            var session = health.Session;
            var shouldEndExistingSession = session?.State is "active" or "degraded" or "paused";
            if (session is null || session.State is "completed" or "failed")
            {
                var preview = await client.GetSessionStartPreviewAsync(
                    SelectedMonitoringMode(), CancellationToken.None);
                ApplySessionStartPreview(preview);
                if (!await ConfirmSessionStartAsync(preview))
                {
                    sessionStatus.Text = "已取消开启保护。";
                    return;
                }
                var name = string.IsNullOrWhiteSpace(sessionName?.Text)
                    ? $"离席保护 {DateTime.Now:yyyy-MM-dd HH:mm}"
                    : sessionName.Text.Trim();
                session = (await client.CreateSessionAsync(
	                    Guid.NewGuid().ToString("D"), name, SelectedMonitoringMode(), CancellationToken.None)).Session;
            }
            if (session?.State == "draft")
            {
                session = (await client.TransitionSessionAsync("preparing", CancellationToken.None, session)).Session;
            }
            if (session?.State == "preparing")
            {
                session = (await client.TransitionSessionAsync("active", CancellationToken.None, session)).Session;
            }
            if (session?.State == "baseline_review")
            {
                var review = await client.GetSessionBaselineReviewAsync(session.Id, CancellationToken.None);
                var resolution = await ConfirmBaselineReviewAsync(review);
                if (resolution is null)
                {
                    sessionStatus.Text = "基线失败项仍待处理。";
                    return;
                }
                session = (await client.ResolveSessionBaselineReviewAsync(
                    session.Id, resolution, CancellationToken.None)).Session;
            }
            if (shouldEndExistingSession && session?.State is "active" or "degraded" or "paused")
            {
                sessionStatus.Text = "请在 Windows 系统凭据窗口中确认结束保护。";
                var challenge = await client.CreateEndVerificationChallengeAsync(CancellationToken.None);
                using var credentials = WindowsCredentialPrompt.PromptForEndProtection();
                session = (await client.EndSessionAsync(challenge, credentials, CancellationToken.None, session)).Session;
            }
            if (session?.State == "finalizing")
            {
                session = (await client.TransitionSessionAsync("completed", CancellationToken.None, session)).Session;
            }
            updatedSession = session;
            completed = true;
        }
        catch (OperationCanceledException)
        {
            sessionStatus.Text = "已取消 Windows 系统凭据确认。";
        }
        catch
        {
            sessionStatus.Text = "保护会话操作未完成。";
        }
        finally
        {
            protectionBusy = false;
            if (completed)
            {
                ApplySession(updatedSession);
            }
            else if (protectionButton is not null)
            {
                protectionButton.IsEnabled = true;
            }
        }
    }

    private async Task ToggleProtectionPauseAsync()
    {
        if (protectionBusy || pauseProtectionButton is null || sessionStatus is null)
        {
            return;
        }
        protectionBusy = true;
        pauseProtectionButton.IsEnabled = false;
        try
        {
            var client = new ControlPipeClient();
            var health = await client.GetHealthAsync(CancellationToken.None);
            if (health.Session?.State is not ("active" or "degraded" or "paused"))
            {
                sessionStatus.Text = "当前保护会话不能暂停或恢复。";
                return;
            }
            var target = health.Session.State == "paused" ? "active" : "paused";
            var result = await client.TransitionSessionAsync(target, CancellationToken.None, health.Session);
            protectionBusy = false;
            ApplySession(result.Session);
        }
        catch
        {
            sessionStatus.Text = "暂停或恢复保护未完成。";
        }
        finally
        {
            protectionBusy = false;
            if (pauseProtectionButton is not null)
            {
                pauseProtectionButton.IsEnabled = true;
            }
        }
    }

    private void ApplySession(SessionInfo? session)
    {
        if (session is not null && analysisWorkspace.Session?.Id == session.Id)
            SelectAnalysisSession(AnalysisSessionFrom(session));
        UpdateGlobalProtectionStatus(session);
        if (sessionStatus is null || protectionButton is null || protectionBusy)
        {
            return;
        }
        if (pauseProtectionButton is not null)
        {
            pauseProtectionButton.Visibility = Visibility.Collapsed;
        }
		trayIcon?.UpdateToolTip(FormatTrayProtectionStatus(session));
        switch (session?.State)
        {
            case "draft":
                sessionStatus.Text = $"保护会话草稿：{session.Name} · {FormatSessionMode(session)}";
                protectionButton.Content = "继续启动";
                protectionButton.IsEnabled = true;
                break;
            case "preparing":
                sessionStatus.Text = $"正在准备保护：{session.Name}";
                protectionButton.Content = "继续启动";
                protectionButton.IsEnabled = true;
                break;
            case "baseline_review":
                sessionStatus.Text = $"基线采集存在失败项：{session.Name}";
                protectionButton.Content = "处理失败项";
                protectionButton.IsEnabled = true;
                break;
            case "active":
            case "degraded":
                sessionStatus.Text = session.State == "degraded"
                    ? $"保护已降级运行：{session.Name} · {FormatSessionMode(session)}"
                    : $"保护正在运行：{session.Name} · {FormatSessionMode(session)}";
                protectionButton.Content = "结束保护";
                protectionButton.IsEnabled = true;
                if (pauseProtectionButton is not null)
                {
                    pauseProtectionButton.Visibility = Visibility.Visible;
                    pauseProtectionButton.Content = "暂停保护";
                }
                break;
            case "paused":
                sessionStatus.Text = $"保护已暂停：{session.Name} · {FormatSessionMode(session)}";
                protectionButton.Content = "结束保护";
                protectionButton.IsEnabled = true;
                if (pauseProtectionButton is not null)
                {
                    pauseProtectionButton.Visibility = Visibility.Visible;
                    pauseProtectionButton.Content = "恢复保护";
                }
                break;
            case "finalizing":
                sessionStatus.Text = $"保护会话正在收尾：{session.Name}";
                protectionButton.Content = "完成收尾";
                protectionButton.IsEnabled = true;
                if (pauseProtectionButton is not null)
                {
                    pauseProtectionButton.Visibility = Visibility.Collapsed;
                }
                break;
            case "completed":
            case "failed":
                sessionStatus.Text = $"{session.Name} · {FormatSessionState(session.State)}。可查看本次结果或从历史会话回看。";
                protectionButton.Content = "开启新保护";
                protectionButton.IsEnabled = true;
                break;
            default:
                sessionStatus.Text = "尚未开启保护。";
                protectionButton.Content = "开启保护";
                protectionButton.IsEnabled = true;
                if (pauseProtectionButton is not null)
                {
                    pauseProtectionButton.Visibility = Visibility.Collapsed;
                }
                break;
        }
    }

    private void UpdateGlobalServiceStatus(string state)
    {
        var (fullText, compactText, color) = state switch
        {
            "running" => ("服务：运行中", "服务：正常", Windows.UI.Color.FromArgb(255, 93, 170, 122)),
            "degraded" => ("服务：降级运行", "服务：降级", Windows.UI.Color.FromArgb(255, 214, 170, 83)),
            "offline" => ("服务：连接异常", "服务：异常", Windows.UI.Color.FromArgb(255, 207, 111, 120)),
            _ => ("服务：正在连接", "服务：连接", Windows.UI.Color.FromArgb(255, 191, 208, 230)),
        };
        globalServiceStatusFullText = fullText;
        globalServiceStatusCompactText = compactText;
        if (globalServiceStatusIndicator is not null)
        {
            globalServiceStatusIndicator.Fill = new SolidColorBrush(color);
        }
        ApplyGlobalStatusLabels();
    }

    private void UpdateGlobalProtectionStatus(SessionInfo? session)
    {
        var serviceUnavailable = globalServiceStatusFullText == "服务：连接异常";
        var (fullText, compactText, color) = serviceUnavailable
            ? ("保护：状态未知", "保护：未知", Windows.UI.Color.FromArgb(255, 207, 111, 120))
            : session?.State switch
            {
                "active" => ("保护：保护中", "保护：开启", Windows.UI.Color.FromArgb(255, 93, 170, 122)),
                "degraded" => ("保护：降级运行", "保护：降级", Windows.UI.Color.FromArgb(255, 214, 170, 83)),
                "paused" => ("保护：已暂停", "保护：暂停", Windows.UI.Color.FromArgb(255, 214, 170, 83)),
                "draft" or "preparing" or "baseline_review" =>
                    ("保护：准备中", "保护：准备", Windows.UI.Color.FromArgb(255, 111, 159, 211)),
                "finalizing" => ("保护：正在收尾", "保护：收尾", Windows.UI.Color.FromArgb(255, 111, 159, 211)),
                "failed" => ("保护：会话异常", "保护：异常", Windows.UI.Color.FromArgb(255, 207, 111, 120)),
                _ => ("保护：未开启", "保护：关闭", Windows.UI.Color.FromArgb(255, 139, 161, 186)),
            };
        globalProtectionStatusFullText = fullText;
        globalProtectionStatusCompactText = compactText;
        if (dashboardProtectionBadge is not null && sessionStatus is not null)
        {
            var healthy = !serviceUnavailable && session?.State == "active";
            var attention = !serviceUnavailable && session?.State is "paused" or "degraded" or "baseline_review";
            dashboardProtectionBadge.Background = ThemeBrush(healthy ? "DgpTealSoftBrush"
                : attention ? "DgpAmberSurfaceBrush" : serviceUnavailable ? "DgpErrorSurfaceBrush" : "DgpInformationBrush");
            sessionStatus.Foreground = ThemeBrush(healthy ? "DgpTealBrush"
                : attention ? "DgpAmberTextBrush" : "DgpDashboardTextBrush");
        }
        if (globalProtectionStatusIndicator is not null)
        {
            globalProtectionStatusIndicator.Fill = new SolidColorBrush(color);
        }
        ApplyGlobalStatusLabels();
    }

    private void ApplyGlobalStatusLabels()
    {
        if (globalServiceStatusText is not null)
        {
            globalServiceStatusText.Text = useCompactGlobalStatusLabels
                ? globalServiceStatusCompactText
                : globalServiceStatusFullText;
        }
        if (globalProtectionStatusText is not null)
        {
            globalProtectionStatusText.Text = useCompactGlobalStatusLabels
                ? globalProtectionStatusCompactText
                : globalProtectionStatusFullText;
        }
    }

	private static string FormatTrayProtectionStatus(SessionInfo? session)
	{
		var status = session?.State switch
		{
			"draft" => "会话草稿",
			"preparing" => "正在准备",
			"baseline_review" => "基线待处理",
			"active" => "保护中",
			"degraded" => "降级保护中",
			"paused" => "已暂停",
			"finalizing" => "正在收尾",
			"completed" => "已完成",
			"failed" => "会话失败",
			_ => "未开启保护",
		};
		return session is null ? $"Desktop Guard Pro · {status}" : $"Desktop Guard Pro · {status} · {session.Name}";
	}

    private async Task LoadDirectoriesAsync()
    {
        if (directoryInputs is null || directoryStatus is null)
        {
            return;
        }
        directoryStatus.Text = "正在读取重点目录...";
        try
        {
            var result = await new ControlPipeClient().GetDirectoryMonitoringAsync(CancellationToken.None);
            ReplaceDirectoryInputs(result.Targets ?? result.Directories.Select(directory =>
                new MonitoringTargetInfo(directory, "directory", true)));
            ReplaceExclusionInputs(result.Exclusions ?? Array.Empty<MonitoringExclusionInfo>());
            if (inputActivityEnabled is not null)
            {
                inputActivityEnabled.IsOn = result.InputActivityEnabled ?? true;
            }
            if (windowTitleEnabled is not null)
            {
                windowTitleEnabled.IsOn = result.WindowTitleEnabled ?? false;
            }
            if (highRiskShortcutsEnabled is not null)
            {
                highRiskShortcutsEnabled.IsOn = result.HighRiskShortcutsEnabled ?? false;
            }
			if (result.MonitoringProfiles is not null)
			{
				ApplyMonitoringProfiles(result.MonitoringProfiles);
			}
			else
			{
				ApplyMonitoringPolicy(result.MonitoringPolicy);
			}
            directoryStatus.Text = $"已加载 {result.Targets?.Count ?? result.Directories.Count} 条监控规则。";
            if (exclusionStatus is not null)
            {
                exclusionStatus.Text = $"已加载 {result.Exclusions?.Count ?? 0} 条排除规则。";
            }
            if (inputActivityStatus is not null)
            {
                inputActivityStatus.Text = result.InputActivityEnabled is false
                    ? "输入活动统计已关闭；前台应用时间线仍会记录。"
                    : "输入活动统计已开启；只保存汇总次数，不保存按键内容。";
            }
            if (windowTitleStatus is not null)
            {
                windowTitleStatus.Text = result.WindowTitleEnabled is true
                    ? "窗口标题记录已开启，标题可能包含文件名、网页标题和其他敏感信息。"
                    : "窗口标题记录已关闭。";
            }
            if (highRiskShortcutsStatus is not null)
            {
                highRiskShortcutsStatus.Text = result.HighRiskShortcutsEnabled is true
                    ? "高风险组合键类别记录已开启；只保存类别和次数，不保存具体按键内容。"
                    : "高风险组合键类别记录已关闭。";
            }
            await LoadSessionStartPreviewAsync();
        }
        catch
        {
            directoryStatus.Text = "无法读取重点目录。";
        }
    }

    private async Task SaveInputActivityPreferenceAsync()
    {
        if (inputActivityEnabled is null || inputActivityStatus is null)
        {
            return;
        }
        inputActivityStatus.Text = "正在保存输入活动设置...";
        try
        {
            var result = await new ControlPipeClient().UpdateInputActivityPreferenceAsync(
                inputActivityEnabled.IsOn,
                CancellationToken.None);
            inputActivityEnabled.IsOn = result.InputActivityEnabled ?? inputActivityEnabled.IsOn;
            inputActivityStatus.Text = inputActivityEnabled.IsOn
                ? "输入活动统计已开启；只保存汇总次数，不保存按键内容。"
                : "输入活动统计已关闭；前台应用时间线仍会记录。";
        }
        catch
        {
            inputActivityStatus.Text = "无法保存输入活动设置。";
        }
    }

    private async Task SaveWindowTitlePreferenceAsync()
    {
        if (windowTitleEnabled is null || windowTitleStatus is null)
        {
            return;
        }
        windowTitleStatus.Text = windowTitleEnabled.IsOn
            ? "正在启用窗口标题记录；标题可能包含敏感信息..."
            : "正在关闭窗口标题记录...";
        try
        {
            var result = await new ControlPipeClient().UpdateWindowTitlePreferenceAsync(
                windowTitleEnabled.IsOn,
                CancellationToken.None);
            windowTitleEnabled.IsOn = result.WindowTitleEnabled ?? windowTitleEnabled.IsOn;
            windowTitleStatus.Text = windowTitleEnabled.IsOn
                ? "窗口标题记录已开启，标题可能包含文件名、网页标题和其他敏感信息。"
                : "窗口标题记录已关闭。";
        }
        catch
        {
            windowTitleStatus.Text = "无法保存窗口标题设置。";
        }
    }

    private async Task SaveHighRiskShortcutsPreferenceAsync()
    {
        if (highRiskShortcutsEnabled is null || highRiskShortcutsStatus is null)
        {
            return;
        }
        highRiskShortcutsStatus.Text = highRiskShortcutsEnabled.IsOn
            ? "正在启用高风险组合键类别记录..."
            : "正在关闭高风险组合键类别记录...";
        try
        {
            var result = await new ControlPipeClient().UpdateHighRiskShortcutsPreferenceAsync(
                highRiskShortcutsEnabled.IsOn,
                CancellationToken.None);
            highRiskShortcutsEnabled.IsOn = result.HighRiskShortcutsEnabled ?? highRiskShortcutsEnabled.IsOn;
            highRiskShortcutsStatus.Text = highRiskShortcutsEnabled.IsOn
                ? "高风险组合键类别记录已开启；只保存 Alt+Tab、Win+R、Win+L、任务管理器四类及次数。"
                : "高风险组合键类别记录已关闭。";
        }
        catch
        {
            highRiskShortcutsStatus.Text = "无法保存高风险组合键设置。";
        }
    }

    private void ApplyMonitoringPolicy(MonitoringPolicyInfo? policy)
    {
        policy = (policy ?? MonitoringPolicyInfo.CreateDefault()).Resolved();
        currentMonitoringPolicy = policy;
        if (fileActivityPolicyEnabled is not null)
        {
            fileActivityPolicyEnabled.IsOn = policy.FileActivityEnabled;
        }
        if (processAndSoftwarePolicyEnabled is not null)
        {
            processAndSoftwarePolicyEnabled.IsOn = policy.ProcessAndSoftwareEnabled;
        }
        if (systemAndNetworkPolicyEnabled is not null)
        {
            systemAndNetworkPolicyEnabled.IsOn = policy.SystemAndNetworkEnabled;
        }
        if (externalDevicesPolicyEnabled is not null)
        {
            externalDevicesPolicyEnabled.IsOn = policy.ExternalDevicesEnabled;
        }
        if (userSessionActivityPolicyEnabled is not null)
        {
            userSessionActivityPolicyEnabled.IsOn = policy.UserSessionActivityEnabled;
        }
        if (strictReadAuditPolicyEnabled is not null)
        {
            strictReadAuditPolicyEnabled.IsOn = policy.StrictReadAuditEnabled;
            strictReadAuditPolicyEnabled.IsEnabled = policy.FileActivityEnabled;
        }
        if (monitoringPolicyStatus is not null)
        {
            monitoringPolicyStatus.Text = $"正在编辑“{MonitoringModeName(editingMonitoringMode)}”模式；保存后用于下一次选择该模式的新会话。";
        }
        UpdateMonitoringPolicySummary();
        UpdateTemporaryInputUnlockHints();
    }

    private MonitoringPolicyInfo MonitoringPolicyFromControls()
    {
        return currentMonitoringPolicy with
        {
            Mode = editingMonitoringMode,
            FileActivityEnabled = fileActivityPolicyEnabled?.IsOn ?? currentMonitoringPolicy.FileActivityEnabled,
            ProcessAndSoftwareEnabled = processAndSoftwarePolicyEnabled?.IsOn ?? currentMonitoringPolicy.ProcessAndSoftwareEnabled,
            SystemAndNetworkEnabled = systemAndNetworkPolicyEnabled?.IsOn ?? currentMonitoringPolicy.SystemAndNetworkEnabled,
            ExternalDevicesEnabled = externalDevicesPolicyEnabled?.IsOn ?? currentMonitoringPolicy.ExternalDevicesEnabled,
            UserSessionActivityEnabled = userSessionActivityPolicyEnabled?.IsOn ?? currentMonitoringPolicy.UserSessionActivityEnabled,
            InputShieldEnabled = false,
            StrictReadAuditEnabled = strictReadAuditPolicyEnabled?.IsOn ?? currentMonitoringPolicy.StrictReadAuditEnabled,
        };
    }

    private void UpdateMonitoringPolicySummary()
    {
        if (monitoringPolicySummary is null || fileActivityPolicyEnabled is null ||
            processAndSoftwarePolicyEnabled is null || systemAndNetworkPolicyEnabled is null ||
            externalDevicesPolicyEnabled is null || userSessionActivityPolicyEnabled is null ||
            strictReadAuditPolicyEnabled is null)
        {
            return;
        }

        var enabledCount = new[]
        {
            fileActivityPolicyEnabled.IsOn,
            processAndSoftwarePolicyEnabled.IsOn,
            systemAndNetworkPolicyEnabled.IsOn,
            externalDevicesPolicyEnabled.IsOn,
            userSessionActivityPolicyEnabled.IsOn,
        }.Count(enabled => enabled);
        monitoringPolicySummary.Text = $"{MonitoringModeName(editingMonitoringMode)}模式已启用 {enabledCount}/5 项审计能力\n" +
            $"严格读取审计：{(strictReadAuditPolicyEnabled.IsOn ? "已启用" : "未启用")}";

        var policy = currentMonitoringPolicy.Resolved();
        var riskRules = policy.RiskRules!;
        var file = policy.File!;
        var fileEvents = new[] { file.RecordCreate, file.RecordModify, file.RecordDelete, file.RecordRename }.Count(value => value);
        if (filePolicyDetailSummary is not null)
        {
            filePolicyDetailSummary.Text = $"事件类型 {fileEvents}/4；内容哈希{EnabledText(file.CaptureContentHash)}；文件风险规则{EnabledText(riskRules.FileActivity)}";
        }
        var process = policy.ProcessAndSoftware!;
        if (processPolicyDetailSummary is not null)
        {
            processPolicyDetailSummary.Text = $"相关进程与软件清单快照；间隔 {process.SnapshotIntervalSeconds} 秒；异常进程规则{EnabledText(riskRules.SensitiveProcess)}；软件变化规则{EnabledText(riskRules.SoftwareChange)}";
        }
        var system = policy.SystemAndNetwork!;
        if (systemPolicyDetailSummary is not null)
        {
            var enabled = new[]
            {
                system.MonitorAccounts, system.MonitorNetwork, system.MonitorProxy, system.MonitorFirewall,
                system.MonitorRemoteDesktop, system.MonitorAuditPolicy, system.MonitorSecurityLog, system.MonitorClock,
                system.MonitorServices, system.MonitorDrivers, system.MonitorScheduledTasks, system.MonitorStartupItems,
            }.Count(value => value);
            systemPolicyDetailSummary.Text = $"监控项 {enabled}/12；资产快照 {system.SnapshotIntervalSeconds} 秒；安全状态规则{EnabledText(riskRules.SecurityState)}";
        }
        var device = policy.ExternalDevices!;
        if (devicePolicyDetailSummary is not null)
        {
            devicePolicyDetailSummary.Text = $"接入记录{EnabledText(device.RecordConnect)}；移除记录{EnabledText(device.RecordDisconnect)}；扫描 {device.SnapshotIntervalSeconds} 秒；设备风险规则{EnabledText(riskRules.DeviceConnection)}";
        }
        var user = policy.UserSession!;
        if (userSessionPolicyDetailSummary is not null)
        {
            var enabled = new[]
            {
                user.RecordForegroundApplication, user.RecordWindowTitle, user.RecordKeyboardActivity,
                user.RecordMouseClicks, user.RecordMouseWheel, user.RecordHighRiskShortcuts,
            }.Count(value => value);
            userSessionPolicyDetailSummary.Text = $"记录项 {enabled}/6；采样 {user.SampleIntervalSeconds} 秒；汇总 {user.ReportIntervalSeconds} 秒";
        }
    }

    private static string EnabledText(bool enabled) => enabled ? "开启" : "关闭";

    private static string MonitoringModeName(string mode) => mode switch
    {
        "relaxed" => "宽松",
        "strict" => "严格",
        "custom" => "自定义",
        _ => "标准",
    };

    private MonitoringPolicyInfo PolicyForMode(string mode)
        => monitoringDrafts.Read(mode, SavedPolicyForMode(mode));

    private MonitoringPolicyInfo SavedPolicyForMode(string mode)
    {
        if (currentMonitoringProfiles is null)
        {
            return currentMonitoringPolicy with { Mode = mode };
        }
        return mode switch
        {
            "relaxed" => currentMonitoringProfiles.Relaxed,
            "strict" => currentMonitoringProfiles.Strict,
            "custom" => currentMonitoringProfiles.Custom,
            _ => currentMonitoringProfiles.Standard,
        };
    }

    private void ApplyMonitoringProfiles(MonitoringProfilesInfo? profiles, bool preserveDraft = true)
    {
        var editingPolicy = monitoringPolicyLoaded ? MonitoringPolicyFromControls() : null;
        if (preserveDraft && editingPolicy is not null)
            monitoringDrafts.Keep(editingMonitoringMode, editingPolicy, SavedPolicyForMode(editingMonitoringMode));
        else if (!preserveDraft) monitoringDrafts.Remove(editingMonitoringMode);
        if (profiles is not null)
        {
            currentMonitoringProfiles = profiles;
        }
        ApplyMonitoringPolicy(PolicyForMode(editingMonitoringMode));
        monitoringPolicyLoaded = true;
    }

    private InputShieldPolicyInfo TemporaryInputPolicy() =>
        (currentMonitoringProfiles?.Custom.Resolved().InputShield ??
        currentMonitoringPolicy.InputShield ?? InputShieldPolicyInfo.CreateDefault()).ForTemporaryControl();

    private static string InputShieldUnlockKeyName(int keyCode) => keyCode switch
    {
        0x20 => "Space",
        0x09 => "Tab",
        0x0D => "Enter",
        0x1B => "Esc",
        0x25 => "左方向键",
        0x26 => "上方向键",
        0x27 => "右方向键",
        0x28 => "下方向键",
        0x2E => "Delete",
        >= 0x30 and <= 0x39 => ((char)keyCode).ToString(),
        >= 0x41 and <= 0x5A => ((char)keyCode).ToString(),
        >= 0x70 and <= 0x87 => $"F{keyCode - 0x6F}",
        _ => $"虚拟键码 {keyCode}",
    };

    private static string InputShieldUnlockInstruction(InputShieldPolicyInfo policy)
    {
        var key = InputShieldUnlockKeyName(policy.UnlockKeyCode);
        if (policy.UnlockTrigger == "tap")
        {
            return $"在 {policy.UnlockTapWindowMilliseconds} 毫秒内连续按 {key} {policy.UnlockTapCount} 次";
        }
        var keys = new List<string>();
        if (policy.UnlockRequireControl) keys.Add("Ctrl");
        if (policy.UnlockRequireAlt) keys.Add("Alt");
        if (policy.UnlockRequireShift) keys.Add("Shift");
        keys.Add(key);
        return $"按 {string.Join("+", keys)}";
    }

    private void UpdateTemporaryInputUnlockHints()
    {
        var instruction = InputShieldUnlockInstruction(TemporaryInputPolicy());
        var hint = $"可{instruction} 呼出密码窗口；验证时仅密码框接收键盘，取消后继续锁定。";
        if (inputShieldUnlockHint is not null) inputShieldUnlockHint.Text = hint;
        if (temporaryInputUnlockHint is not null)
        {
            temporaryInputUnlockHint.Text = $"{instruction} 解锁 · 输入本地密码或恢复码";
            ToolTipService.SetToolTip(temporaryInputUnlockHint, hint);
        }
    }

    private void SelectMonitoringProfile(string mode)
    {
        if (monitoringPolicyLoaded)
            monitoringDrafts.Keep(editingMonitoringMode, MonitoringPolicyFromControls(), SavedPolicyForMode(editingMonitoringMode));
        editingMonitoringMode = mode;
        ApplyMonitoringPolicy(PolicyForMode(mode));
    }

    private async Task LoadMonitoringPolicyAsync()
    {
        if (monitoringPolicyLoaded) return;
        if (monitoringPolicyStatus is null)
        {
            return;
        }
        monitoringPolicyStatus.Text = "正在读取监控与审计策略...";
        try
        {
            var result = await new ControlPipeClient().GetDirectoryMonitoringAsync(CancellationToken.None);
            if (result.MonitoringProfiles is not null)
            {
                ApplyMonitoringProfiles(result.MonitoringProfiles);
            }
            else
            {
                ApplyMonitoringPolicy(result.MonitoringPolicy);
            }
        }
        catch
        {
            monitoringPolicyStatus.Text = "无法读取监控与审计策略。";
        }
    }

    private async Task<bool> SaveMonitoringPolicyAsync()
    {
        if (fileActivityPolicyEnabled is null || processAndSoftwarePolicyEnabled is null ||
            systemAndNetworkPolicyEnabled is null || externalDevicesPolicyEnabled is null ||
            userSessionActivityPolicyEnabled is null || strictReadAuditPolicyEnabled is null ||
            monitoringPolicyStatus is null)
        {
            return false;
        }
        var policy = MonitoringPolicyFromControls();
        currentMonitoringPolicy = policy;
        monitoringPolicyStatus.Text = "正在保存监控与审计策略...";
        try
        {
            var client = new ControlPipeClient();
            var result = await client.UpdateMonitoringPolicyAsync(policy, CancellationToken.None);
            if (result.MonitoringProfiles is not null)
            {
                // Preserve edits made while the save request was in flight,
                // including edits in a different mode.
                monitoringDrafts.Keep(editingMonitoringMode, MonitoringPolicyFromControls(), SavedPolicyForMode(editingMonitoringMode));
                currentMonitoringProfiles = result.MonitoringProfiles;
                monitoringDrafts.Keep(policy.Mode, monitoringDrafts.Read(policy.Mode, policy), SavedPolicyForMode(policy.Mode));
                ApplyMonitoringPolicy(PolicyForMode(editingMonitoringMode));
            }
            else
            {
                ApplyMonitoringPolicy(result.MonitoringPolicy ?? policy);
            }
            await LoadSessionStartPreviewAsync();
            return true;
        }
        catch
        {
            monitoringPolicyStatus.Text = "无法保存策略。请检查采集类别、严格读取审计依赖和采样间隔。";
            return false;
        }
    }

    private async Task ResetMonitoringProfileAsync()
    {
        if (monitoringPolicyStatus is null)
        {
            return;
        }
        var resetMode = editingMonitoringMode;
        monitoringPolicyStatus.Text = $"正在恢复“{MonitoringModeName(resetMode)}”模式默认设置...";
        try
        {
            var result = await new ControlPipeClient().ResetMonitoringPolicyAsync(
                resetMode, CancellationToken.None);
            if (editingMonitoringMode != resetMode)
                monitoringDrafts.Keep(editingMonitoringMode, MonitoringPolicyFromControls(), SavedPolicyForMode(editingMonitoringMode));
            monitoringDrafts.Remove(resetMode);
            if (result.MonitoringProfiles is not null) currentMonitoringProfiles = result.MonitoringProfiles;
            ApplyMonitoringPolicy(PolicyForMode(editingMonitoringMode));
            await LoadSessionStartPreviewAsync();
        }
        catch
        {
            monitoringPolicyStatus.Text = "无法恢复当前模式的默认设置。";
        }
    }

    private ToggleSwitch CreateMonitoringPolicyOption(string header, bool isOn)
    {
        return new ToggleSwitch
        {
            Header = header,
            IsOn = isOn,
            OnContent = "开启",
            OffContent = "关闭",
        };
    }

    private static NumberBox CreateMonitoringPolicyInterval(string header, int value, int minimum, int maximum)
    {
        return new NumberBox
        {
            Header = header,
            Value = value,
            Minimum = minimum,
            Maximum = maximum,
            SmallChange = 1,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
        };
    }

    private async Task<bool> ShowMonitoringPolicyDialogAsync(string title, string description, params UIElement[] controls)
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null)
        {
            return false;
        }
        var content = new StackPanel { Spacing = 10, Width = 680 };
        content.Children.Add(new TextBlock
        {
            Text = description,
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        });
        foreach (var control in controls)
        {
            content.Children.Add(control);
        }
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            RequestedTheme = ElementTheme.Light,
            Title = title,
            Content = new ScrollViewer { MaxHeight = 620, Content = content },
            PrimaryButtonText = "保存配置",
            CloseButtonText = "取消",
            DefaultButton = ContentDialogButton.Primary,
        };
        return await dialog.ShowAsync() == ContentDialogResult.Primary;
    }

    private async Task ShowFilePolicyDetailsAsync()
    {
        var policy = currentMonitoringPolicy.Resolved();
        var details = policy.File!;
        var create = CreateMonitoringPolicyOption("记录文件创建", details.RecordCreate);
        var modify = CreateMonitoringPolicyOption("记录文件修改与截断", details.RecordModify);
        var delete = CreateMonitoringPolicyOption("记录文件删除", details.RecordDelete);
        var rename = CreateMonitoringPolicyOption("记录文件重命名", details.RecordRename);
        var hash = CreateMonitoringPolicyOption("采集内容哈希", details.CaptureContentHash);
        var strict = CreateMonitoringPolicyOption("记录文件读取与打开活动（严格审计）", policy.StrictReadAuditEnabled);
        var riskRule = CreateMonitoringPolicyOption("启用文件活动风险规则", policy.RiskRules!.FileActivity);
        if (!await ShowMonitoringPolicyDialogAsync(
                "文件审计详情",
                "这些选项直接控制重点目标产生的实时文件事件与哈希证据。严格读取审计依赖管理员权限及 Windows 对象访问审核配置。监控目标和排除规则继续在仪表盘维护。",
                create, modify, delete, rename, hash, strict, riskRule))
        {
            return;
        }
        currentMonitoringPolicy = policy with
        {
            File = new FileAuditPolicyInfo(create.IsOn, modify.IsOn, delete.IsOn, rename.IsOn, hash.IsOn, false),
            StrictReadAuditEnabled = strict.IsOn,
            RiskRules = policy.RiskRules with { FileActivity = riskRule.IsOn },
        };
        ApplyMonitoringPolicy(currentMonitoringPolicy);
        await SaveMonitoringPolicyAsync();
    }

    private async Task ShowProcessPolicyDetailsAsync()
    {
        var policy = currentMonitoringPolicy.Resolved();
        var details = policy.ProcessAndSoftware!;
        var interval = CreateMonitoringPolicyInterval("进程与软件快照间隔（秒，60–3600）", details.SnapshotIntervalSeconds, 60, 3600);
        var sensitiveRule = CreateMonitoringPolicyOption("启用敏感进程风险规则", policy.RiskRules!.SensitiveProcess);
        var softwareRule = CreateMonitoringPolicyOption("启用软件变化风险规则", policy.RiskRules.SoftwareChange);
        if (!await ShowMonitoringPolicyDialogAsync(
                "进程与软件详情",
                "每个周期生成一条筛选后的相关进程快照，并比较软件清单的新增、移除和版本变化。常规 Windows 组件与稳定服务会被过滤；用户目录、名称路径不一致、异常父链和同名换路径等项目会保留。签名仅用于内存筛选，不写入审计记录。",
                interval, sensitiveRule, softwareRule))
        {
            return;
        }
        currentMonitoringPolicy = policy with
        {
            ProcessAndSoftware = new ProcessSoftwarePolicyInfo(
                false, false, false, false, false,
                Math.Clamp((int)Math.Round(interval.Value), 60, 3600)),
            RiskRules = policy.RiskRules with
            {
                SensitiveProcess = sensitiveRule.IsOn,
                SoftwareChange = softwareRule.IsOn,
            },
        };
        ApplyMonitoringPolicy(currentMonitoringPolicy);
        await SaveMonitoringPolicyAsync();
    }

    private async Task ShowSystemPolicyDetailsAsync()
    {
        var policy = currentMonitoringPolicy.Resolved();
        var details = policy.SystemAndNetwork!;
        var accounts = CreateMonitoringPolicyOption("账户配置", details.MonitorAccounts);
        var network = CreateMonitoringPolicyOption("网络配置", details.MonitorNetwork);
        var proxy = CreateMonitoringPolicyOption("代理配置", details.MonitorProxy);
        var firewall = CreateMonitoringPolicyOption("防火墙配置", details.MonitorFirewall);
        var remoteDesktop = CreateMonitoringPolicyOption("远程桌面配置", details.MonitorRemoteDesktop);
        var audit = CreateMonitoringPolicyOption("系统审计策略", details.MonitorAuditPolicy);
        var securityLog = CreateMonitoringPolicyOption("Windows 安全日志", details.MonitorSecurityLog);
        var clock = CreateMonitoringPolicyOption("系统时间跳变", details.MonitorClock);
        var services = CreateMonitoringPolicyOption("系统服务", details.MonitorServices);
        var drivers = CreateMonitoringPolicyOption("驱动程序", details.MonitorDrivers);
        var tasks = CreateMonitoringPolicyOption("计划任务", details.MonitorScheduledTasks);
        var startup = CreateMonitoringPolicyOption("启动项", details.MonitorStartupItems);
        var interval = CreateMonitoringPolicyInterval("系统资产快照间隔（秒，5–3600）", details.SnapshotIntervalSeconds, 5, 3600);
        var startupRule = CreateMonitoringPolicyOption("启用启动项变化风险规则", policy.RiskRules!.StartupChange);
        var securityRule = CreateMonitoringPolicyOption("启用安全状态风险规则", policy.RiskRules.SecurityState);
        var gapRule = CreateMonitoringPolicyOption("启用采集缺口风险规则", policy.RiskRules.CollectionGap);
        if (!await ShowMonitoringPolicyDialogAsync(
                "系统与网络详情",
                "每个监控项控制对应注册表或系统资产变化是否进入审计时间线。资产快照间隔用于服务、驱动、任务、账户和网络等轮询采集。",
                accounts, network, proxy, firewall, remoteDesktop, audit, securityLog, clock,
                services, drivers, tasks, startup, interval, startupRule, securityRule, gapRule))
        {
            return;
        }
        currentMonitoringPolicy = policy with
        {
            SystemAndNetwork = new SystemNetworkPolicyInfo(
                accounts.IsOn, network.IsOn, proxy.IsOn, firewall.IsOn, remoteDesktop.IsOn, audit.IsOn,
                securityLog.IsOn, clock.IsOn, services.IsOn, drivers.IsOn, tasks.IsOn, startup.IsOn,
                Math.Clamp((int)Math.Round(interval.Value), 5, 3600)),
            RiskRules = policy.RiskRules with
            {
                StartupChange = startupRule.IsOn,
                SecurityState = securityRule.IsOn,
                CollectionGap = gapRule.IsOn,
            },
        };
        ApplyMonitoringPolicy(currentMonitoringPolicy);
        await SaveMonitoringPolicyAsync();
    }

    private async Task ShowDevicePolicyDetailsAsync()
    {
        var policy = currentMonitoringPolicy.Resolved();
        var details = policy.ExternalDevices!;
        var connect = CreateMonitoringPolicyOption("记录设备接入与重新接入", details.RecordConnect);
        var disconnect = CreateMonitoringPolicyOption("记录设备移除", details.RecordDisconnect);
        var interval = CreateMonitoringPolicyInterval("设备扫描间隔（秒，1–300）", details.SnapshotIntervalSeconds, 1, 300);
        var riskRule = CreateMonitoringPolicyOption("启用设备接入风险规则", policy.RiskRules!.DeviceConnection);
        if (!await ShowMonitoringPolicyDialogAsync(
                "外接设备详情",
                "配置会控制 USB 存储和常见即插即用设备的接入、移除事件及风险判定。扫描间隔越短，设备变化发现越及时。",
                connect, disconnect, interval, riskRule))
        {
            return;
        }
        currentMonitoringPolicy = policy with
        {
            ExternalDevices = new ExternalDevicePolicyInfo(
                connect.IsOn, disconnect.IsOn, Math.Clamp((int)Math.Round(interval.Value), 1, 300)),
            RiskRules = policy.RiskRules with { DeviceConnection = riskRule.IsOn },
        };
        ApplyMonitoringPolicy(currentMonitoringPolicy);
        await SaveMonitoringPolicyAsync();
    }

    private async Task ShowUserSessionPolicyDetailsAsync()
    {
        var policy = currentMonitoringPolicy.Resolved();
        var details = policy.UserSession!;
        var foreground = CreateMonitoringPolicyOption("记录前台应用", details.RecordForegroundApplication);
        var title = CreateMonitoringPolicyOption("记录窗口标题", details.RecordWindowTitle);
        var keyboard = CreateMonitoringPolicyOption("记录键盘活动计数", details.RecordKeyboardActivity);
        var clicks = CreateMonitoringPolicyOption("记录鼠标点击计数", details.RecordMouseClicks);
        var wheel = CreateMonitoringPolicyOption("记录鼠标滚轮计数", details.RecordMouseWheel);
        var shortcuts = CreateMonitoringPolicyOption("记录高风险组合键类别", details.RecordHighRiskShortcuts);
        var sampleInterval = CreateMonitoringPolicyInterval("活动采样间隔（秒，1–60）", details.SampleIntervalSeconds, 1, 60);
        var reportInterval = CreateMonitoringPolicyInterval("活动汇总间隔（秒，5–600）", details.ReportIntervalSeconds, 5, 600);
        if (!await ShowMonitoringPolicyDialogAsync(
                "用户会话活动详情",
                "仅记录所选活动类别。键盘、点击和滚轮保存计数，不保存输入内容；窗口标题可能包含文件名、网页标题等敏感信息。汇总间隔需要大于或等于采样间隔。",
                foreground, title, keyboard, clicks, wheel, shortcuts, sampleInterval, reportInterval))
        {
            return;
        }
        var sampleSeconds = Math.Clamp((int)Math.Round(sampleInterval.Value), 1, 60);
        var reportSeconds = Math.Clamp((int)Math.Round(reportInterval.Value), Math.Max(5, sampleSeconds), 600);
        currentMonitoringPolicy = policy with
        {
            UserSession = new UserSessionPolicyInfo(
                foreground.IsOn, title.IsOn, keyboard.IsOn, clicks.IsOn, wheel.IsOn, shortcuts.IsOn,
                sampleSeconds, reportSeconds),
        };
        ApplyMonitoringPolicy(currentMonitoringPolicy);
        await SaveMonitoringPolicyAsync();
    }

    private async Task ShowInputShieldPolicyDetailsAsync()
    {
        var previousMode = editingMonitoringMode;
        if (currentMonitoringProfiles is not null)
        {
            SelectMonitoringProfile("custom");
            if (settingsMonitoringMode is not null)
            {
                settingsMonitoringMode.SelectedItem = settingsMonitoringMode.Items.OfType<ComboBoxItem>()
                    .FirstOrDefault(item => string.Equals(item.Tag as string, "custom", StringComparison.Ordinal));
            }
            ApplyMonitoringPolicy(PolicyForMode("custom").Resolved());
        }
        var policy = currentMonitoringPolicy.Resolved();
        var details = policy.InputShield!.ForTemporaryControl();
        ComboBox Choice(string header, string selected, params (string Label, string Value)[] options)
        {
            var choice = new ComboBox { Header = header, HorizontalAlignment = HorizontalAlignment.Stretch };
            foreach (var option in options)
            {
                choice.Items.Add(new ComboBoxItem { Content = option.Label, Tag = option.Value });
            }
            choice.SelectedItem = choice.Items.OfType<ComboBoxItem>()
                .FirstOrDefault(item => string.Equals(item.Tag as string, selected, StringComparison.Ordinal))
                ?? choice.Items[0];
            return choice;
        }
        static string SelectedTag(ComboBox choice, string fallback) =>
            choice.SelectedItem is ComboBoxItem { Tag: string value } ? value : fallback;

        var overlay = CreateMonitoringPolicyOption("显示多显示器输入阻断告警", details.ShowWarningOverlay);
        var warningDuration = CreateMonitoringPolicyInterval("告警显示时间（秒，1–60）", details.WarningDurationSeconds, 1, 60);
        var warningMessage = new TextBox
        {
            Header = "告警提示内容（最多 120 个字符）",
            Text = details.WarningMessage,
            MaxLength = 120,
        };
        void UpdateOverlayControls()
        {
            warningDuration.IsEnabled = overlay.IsOn;
            warningMessage.IsEnabled = overlay.IsOn;
        }
        overlay.Toggled += (_, _) => UpdateOverlayControls();
        UpdateOverlayControls();

        var trackDevices = CreateMonitoringPolicyOption("识别当前活跃键盘与鼠标", details.TrackActiveDevices);
        var deviceArrival = CreateMonitoringPolicyOption("新键鼠设备接入时告警并记录", details.WarnOnDeviceArrival);
        var deviceRemoval = CreateMonitoringPolicyOption("记录键鼠设备移除", details.RecordDeviceRemoval);
        void UpdateDeviceControls()
        {
            deviceArrival.IsEnabled = trackDevices.IsOn;
            deviceRemoval.IsEnabled = trackDevices.IsOn;
            if (!trackDevices.IsOn)
            {
                deviceArrival.IsOn = false;
                deviceRemoval.IsOn = false;
            }
        }
        trackDevices.Toggled += (_, _) => UpdateDeviceControls();
        UpdateDeviceControls();
        var trigger = Choice("本地解锁触发方式", details.UnlockTrigger,
            ("组合键", "combination"), ("单键连续点击", "tap"));
        var keyCode = CreateMonitoringPolicyInterval("解锁键 Windows 虚拟键码（U=85，空格=32）", details.UnlockKeyCode, 1, 255);
        var requireControl = CreateMonitoringPolicyOption("组合键包含 Ctrl", details.UnlockRequireControl);
        var requireAlt = CreateMonitoringPolicyOption("组合键包含 Alt", details.UnlockRequireAlt);
        var requireShift = CreateMonitoringPolicyOption("组合键包含 Shift", details.UnlockRequireShift);
        var tapCount = CreateMonitoringPolicyInterval("连续点击次数（3–12）", details.UnlockTapCount, 3, 12);
        var tapWindow = CreateMonitoringPolicyInterval("连续点击时间窗口（毫秒，500–10000）", details.UnlockTapWindowMilliseconds, 500, 10000);
        void UpdateTriggerControls()
        {
            var combination = SelectedTag(trigger, "combination") == "combination";
            requireControl.IsEnabled = combination;
            requireAlt.IsEnabled = combination;
            requireShift.IsEnabled = combination;
            tapCount.IsEnabled = !combination;
            tapWindow.IsEnabled = !combination;
        }
        trigger.SelectionChanged += (_, _) => UpdateTriggerControls();
        UpdateTriggerControls();

        var attempts = CreateMonitoringPolicyInterval("连续失败锁定阈值（1–10）", details.MaxFailedUnlockAttempts, 1, 10);
        var lockout = CreateMonitoringPolicyInterval("失败锁定时间（秒，10–3600）", details.UnlockLockoutSeconds, 10, 3600);

        var recordCategory = CreateMonitoringPolicyOption("记录被阻断输入的类别和次数", details.RecordBlockedInputCategory);
        var recordKeys = CreateMonitoringPolicyOption("记录具体按键名称", details.RecordKeyNames);
        var recordCoordinates = CreateMonitoringPolicyOption("记录鼠标点击坐标", details.RecordPointerCoordinates);
        void UpdateAuditControls()
        {
            recordKeys.IsEnabled = recordCategory.IsOn;
            recordCoordinates.IsEnabled = recordCategory.IsOn;
            if (!recordKeys.IsEnabled)
            {
                recordKeys.IsOn = false;
            }
            if (!recordCoordinates.IsEnabled)
            {
                recordCoordinates.IsOn = false;
            }
        }
        recordCategory.Toggled += (_, _) => UpdateAuditControls();
        UpdateAuditControls();
        var heartbeat = CreateMonitoringPolicyInterval("钩子健康心跳（秒，1–30）", details.HookHeartbeatSeconds, 1, 30);
        var privacyNotice = new TextBlock
        {
            Text = "具体按键和鼠标坐标属于敏感审计数据。开启后只写入加密审计链。兼容模式只能识别注入标志，无法确认输入来自某个指定远程控制软件。",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        if (!await ShowMonitoringPolicyDialogAsync(
                "临时输入控制设置",
                "统一锁定本地键盘和鼠标，允许远程注入输入。默认按 Ctrl+Alt+Space 呼出密码窗口；输入已保存的本地密码或恢复码解锁，取消后继续锁定。Ctrl+Alt+Delete 与 Windows 安全桌面由操作系统控制。服务或系统重启会释放锁定。",
                overlay, warningDuration, warningMessage,
                trackDevices, deviceArrival, deviceRemoval,
                trigger, keyCode, requireControl, requireAlt, requireShift, tapCount, tapWindow,
                attempts, lockout,
                recordCategory, recordKeys, recordCoordinates, heartbeat, privacyNotice))
        {
            if (settingsMonitoringMode is not null)
            {
                settingsMonitoringMode.SelectedItem = settingsMonitoringMode.Items.OfType<ComboBoxItem>()
                    .FirstOrDefault(item => string.Equals(item.Tag as string, previousMode, StringComparison.Ordinal));
            }
            SelectMonitoringProfile(previousMode);
            return;
        }
        var triggerMode = SelectedTag(trigger, "combination");
        var control = requireControl.IsOn;
        var alt = requireAlt.IsOn;
        var shift = requireShift.IsOn;
        if (triggerMode == "combination" && !control && !alt && !shift)
        {
            control = true;
        }
        var defaultWarning = InputShieldPolicyInfo.CreateDefault().WarningMessage;
        currentMonitoringPolicy = policy with
        {
            InputShield = new InputShieldPolicyInfo(
                true, true, true,
                "compatible", overlay.IsOn,
                Math.Clamp((int)Math.Round(warningDuration.Value), 1, 60),
                string.IsNullOrWhiteSpace(warningMessage.Text) ? defaultWarning : warningMessage.Text.Trim(),
                trackDevices.IsOn, deviceArrival.IsOn, deviceRemoval.IsOn,
                triggerMode, Math.Clamp((int)Math.Round(keyCode.Value), 1, 255),
                control, alt, shift,
                Math.Clamp((int)Math.Round(tapCount.Value), 3, 12),
                Math.Clamp((int)Math.Round(tapWindow.Value), 500, 10000),
                "local", true,
                "suspend",
                Math.Clamp((int)Math.Round(attempts.Value), 1, 10),
                Math.Clamp((int)Math.Round(lockout.Value), 10, 3600),
                recordCategory.IsOn, recordKeys.IsOn, recordCoordinates.IsOn,
                false, Math.Clamp((int)Math.Round(heartbeat.Value), 1, 30)),
        };
        ApplyMonitoringPolicy(currentMonitoringPolicy);
        if (!await SaveMonitoringPolicyAsync())
        {
            ApplyMonitoringPolicy(policy);
            monitoringPolicyStatus!.Text = "控制规则未保存；临时输入控制仍使用上次保存的规则。";
            await ShowInputShieldCredentialNoticeAsync(
                "控制规则未保存", "临时输入控制仍使用上次保存的规则。请检查后台服务后重试。");
        }
    }

    private async Task LoadInputShieldManagementAsync()
    {
        if (inputShieldRuntimeStatus is null || inputShieldCredentialStatus is null)
        {
            return;
        }
        inputShieldRuntimeStatus.Text = "正在读取输入防护运行状态...";
        inputShieldCredentialStatus.Text = "正在读取本地凭据状态...";
        var client = new ControlPipeClient();
        var runtimeTask = client.GetInputShieldStatusAsync(CancellationToken.None);
        var credentialTask = client.GetInputShieldCredentialStatusAsync(CancellationToken.None);
        try
        {
            var runtime = await runtimeTask;
            inputShieldDevices = runtime.Devices ?? Array.Empty<InputShieldDeviceInfo>();
            inputShieldDevicesAvailable = true;
            var state = runtime.State switch
            {
                "starting" => "正在启动",
                "protecting" => "防护中",
                "verifying" => "正在验证身份",
                "suspended" => "已暂停输入阻断",
                "degraded" => "运行异常",
                _ => "未运行",
            };
            var heartbeat = runtime.ObservedUtc is null || runtime.ObservedUtc.Value <= DateTimeOffset.UnixEpoch
                ? "暂无运行记录"
                : $"最近心跳 {runtime.ObservedUtc.Value.ToLocalTime():yyyy-MM-dd HH:mm:ss}";
            inputShieldRuntimeStatus.Text = $"状态：{state}；钩子{(runtime.HookRunning ? "已运行" : "未运行")}；{heartbeat}；输入设备 {inputShieldDevices.Count} 个；丢弃事件 {runtime.DroppedEvents}";
        }
        catch
        {
            inputShieldDevices = Array.Empty<InputShieldDeviceInfo>();
            inputShieldDevicesAvailable = false;
            inputShieldRuntimeStatus.Text = "无法读取输入防护运行状态。";
        }
        try
        {
            var credential = await credentialTask;
            inputShieldCredentialStatus.Text = credential.Configured
                ? $"独立本地密码已设置；一次性恢复码{(credential.RecoveryCodeEnabled ? "可用" : "未启用或已使用")}。"
                : "尚未设置本地密码。首次启动锁定时需先设置密码。";
        }
        catch
        {
            inputShieldCredentialStatus.Text = "无法读取本地凭据状态。";
        }
    }

    private async Task LoadTemporaryInputControlAsync()
    {
        if (temporaryInputControlStatus is null)
        {
            return;
        }
        try
        {
            var result = await new ControlPipeClient().GetInputControlAsync(CancellationToken.None);
            ApplyTemporaryInputControl(result);
        }
        catch
        {
            temporaryInputControlStatus.Text = "无法读取临时输入控制状态。";
            if (startTemporaryInputControlButton is not null)
            {
                startTemporaryInputControlButton.IsEnabled = false;
            }
        }
    }

    private void ApplyTemporaryInputControl(InputControlResult result)
    {
        if (temporaryInputControlStatus is null)
        {
            return;
        }
        var temporary = string.Equals(result.Source, "temporary", StringComparison.Ordinal);
        if (temporary && string.Equals(result.State, "stopping", StringComparison.Ordinal))
        {
            temporaryInputControlStatus.Text = "正在等待交互式代理确认键鼠钩子已停止...";
        }
        else if (!result.Enabled)
        {
            temporaryInputControlStatus.Text = "未锁定 · 启动后锁定本地键盘和鼠标";
        }
        else if (temporary)
        {
            var scope = result.Policy switch
            {
                { BlockPhysicalKeyboard: true, BlockPhysicalMouse: true } => "键盘和鼠标",
                { BlockPhysicalKeyboard: true } => "键盘",
                _ => "鼠标",
            };
            var runtime = result.State == "protecting" && result.HookRunning ? "运行中" : result.State;
            var duration = result.Indefinite
                ? $"可{InputShieldUnlockInstruction(result.Policy ?? TemporaryInputPolicy())} 呼出密码窗口；验证成功后释放"
                : $"到期时间 {result.ExpiresUtc?.ToLocalTime():yyyy-MM-dd HH:mm}";
            temporaryInputControlStatus.Text = $"临时控制{runtime}：{scope}；{duration}。";
        }
        else
        {
            temporaryInputControlStatus.Text = "当前输入控制来自正在运行的保护模式。可启动临时任务暂时覆盖其键鼠范围。";
        }
        if (startTemporaryInputControlButton is not null)
        {
            var stopping = temporary && string.Equals(result.State, "stopping", StringComparison.Ordinal);
            startTemporaryInputControlButton.IsEnabled = !stopping && !temporary;
            startTemporaryInputControlButton.Visibility = temporary && result.Enabled
                ? Visibility.Collapsed : Visibility.Visible;
            startTemporaryInputControlButton.Content = stopping ? "正在解除…" : temporary ? "锁定中" : "启动锁定";
        }
        if (stopTemporaryInputControlButton is not null)
        {
            stopTemporaryInputControlButton.IsEnabled = temporary && result.Enabled;
            stopTemporaryInputControlButton.Visibility = temporary && result.Enabled ? Visibility.Visible : Visibility.Collapsed;
        }
        if (temporary && result.State is ("starting" or "degraded" or "stopping"))
        {
            EnsureInteractiveAgentStarted();
        }
    }

    private async Task StartTemporaryInputControlAsync()
    {
        if (temporaryInputDuration is null || temporaryInputDurationMode is null || temporaryInputControlStatus is null)
        {
            return;
        }
        var indefinite = temporaryInputDurationMode.SelectedItem is ComboBoxItem { Tag: string mode } && mode == "until_unlock";
        var duration = indefinite
            ? 0
            : double.IsNaN(temporaryInputDuration.Value) ? 15 : (int)Math.Round(temporaryInputDuration.Value);
        var policy = TemporaryInputPolicy();
        if (string.Equals(policy.CredentialMode, "local", StringComparison.Ordinal))
        {
            try
            {
                var credential = await new ControlPipeClient().GetInputShieldCredentialStatusAsync(CancellationToken.None);
                if (!credential.Configured)
                {
                    await ConfigureInputShieldCredentialsAsync();
                    credential = await new ControlPipeClient().GetInputShieldCredentialStatusAsync(CancellationToken.None);
                    if (!credential.Configured)
                    {
                        temporaryInputControlStatus.Text = "尚未设置密码，未启动锁定。";
                        return;
                    }
                }
            }
            catch
            {
                temporaryInputControlStatus.Text = "无法确认本地密码状态。请确认服务正在运行后重试。";
                return;
            }
        }
        temporaryInputControlStatus.Text = "正在启动临时输入控制...";
        if (startTemporaryInputControlButton is not null)
        {
            startTemporaryInputControlButton.IsEnabled = false;
        }
        try
        {
            var result = await new ControlPipeClient().StartInputControlAsync(
                policy, duration, indefinite, CancellationToken.None);
            ApplyTemporaryInputControl(result);
        }
        catch
        {
            temporaryInputControlStatus.Text = "临时输入控制启动失败。请检查交互式代理和解锁凭据设置。";
            if (startTemporaryInputControlButton is not null)
            {
                startTemporaryInputControlButton.IsEnabled = true;
            }
        }
    }

    private async Task StopTemporaryInputControlAsync()
    {
        if (temporaryInputControlStatus is null)
        {
            return;
        }
        temporaryInputControlStatus.Text = "正在打开解锁密码窗口...";
        try
        {
            var result = await new ControlPipeClient().RequestInputControlUnlockAsync(CancellationToken.None);
            ApplyTemporaryInputControl(result);
        }
        catch
        {
            temporaryInputControlStatus.Text = "无法打开密码窗口，请使用解锁快捷键。";
        }
    }

    private async Task ShowInputShieldDevicesAsync()
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null)
        {
            return;
        }
        await LoadInputShieldManagementAsync();
        var deviceList = new StackPanel { Spacing = 10 };
        if (!inputShieldDevicesAvailable || inputShieldDevices.Count == 0)
        {
            deviceList.Children.Add(new TextBlock
            {
                Text = inputShieldDevicesAvailable
                    ? "当前没有代理上报的键盘或鼠标设备。请确认交互式代理正在运行，并等待设备清单上报。"
                    : "无法读取输入设备清单。请检查后台服务与交互式代理后重试。",
                TextWrapping = TextWrapping.Wrap,
                Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            });
        }
        foreach (var device in inputShieldDevices)
        {
            var kind = device.Kind == "keyboard" ? "键盘" : device.Kind == "mouse" ? "鼠标" : device.Kind;
            var active = device.Active ? "当前活动" : "已识别";
            var lastActive = device.LastActiveUtc is null
                ? "尚无活动记录"
                : $"最后活动 {device.LastActiveUtc.Value.ToLocalTime():yyyy-MM-dd HH:mm:ss}";
            var hardware = string.IsNullOrWhiteSpace(device.VendorId) && string.IsNullOrWhiteSpace(device.ProductId)
                ? "硬件标识不可用"
                : $"VID {device.VendorId ?? "----"} / PID {device.ProductId ?? "----"}";
            deviceList.Children.Add(CreateSectionCard(
                $"{kind} · {active}",
                new TextBlock { Text = $"{hardware}；{lastActive}", TextWrapping = TextWrapping.Wrap },
                new TextBlock
                {
                    Text = string.IsNullOrWhiteSpace(device.InstanceId) ? "实例标识不可用" : device.InstanceId,
                    TextWrapping = TextWrapping.Wrap,
                    IsTextSelectionEnabled = true,
                    Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                },
                new TextBlock
                {
                    Text = string.IsNullOrWhiteSpace(device.InterfacePath) ? "接口路径不可用" : device.InterfacePath,
                    TextWrapping = TextWrapping.Wrap,
                    IsTextSelectionEnabled = true,
                    Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                }));
        }
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            RequestedTheme = ElementTheme.Light,
            Title = $"输入设备清单（{inputShieldDevices.Count}）",
            Content = new ScrollViewer
            {
                Width = 680,
                MaxHeight = 560,
                VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
                Content = deviceList,
            },
            CloseButtonText = "关闭",
        };
        await dialog.ShowAsync();
    }

    private async Task ConfigureInputShieldCredentialsAsync()
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null || inputShieldCredentialStatus is null)
        {
            return;
        }
        var password = new PasswordBox { Header = "本地防护密码（8–128 个字符）" };
        var confirmation = new PasswordBox { Header = "再次输入密码" };
        var recovery = CreateMonitoringPolicyOption("生成一次性恢复码", true);
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            RequestedTheme = ElementTheme.Light,
            Title = "设置本地输入防护凭据",
            Content = new StackPanel
            {
                Width = 520,
                Spacing = 12,
                Children =
                {
                    new TextBlock
                    {
                        Text = "密码只保存慢哈希，并由服务端加密存储。恢复码只显示一次，验证成功后立即失效。",
                        TextWrapping = TextWrapping.Wrap,
                        Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                    },
                    password,
                    confirmation,
                    recovery,
                },
            },
            PrimaryButtonText = "保存凭据",
            CloseButtonText = "取消",
            DefaultButton = ContentDialogButton.Primary,
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            password.Password = string.Empty;
            confirmation.Password = string.Empty;
            return;
        }
        var passwordText = password.Password;
        var confirmationText = confirmation.Password;
        password.Password = string.Empty;
        confirmation.Password = string.Empty;
        if (passwordText.EnumerateRunes().Count() is < 8 or > 128 || !string.Equals(passwordText, confirmationText, StringComparison.Ordinal))
        {
            inputShieldCredentialStatus.Text = "密码长度需要为 8–128 个字符，并且两次输入保持一致。";
            await ShowInputShieldCredentialNoticeAsync("未保存本地密码", inputShieldCredentialStatus.Text);
            return;
        }
        var passwordBytes = Encoding.UTF8.GetBytes(passwordText);
        passwordText = string.Empty;
        confirmationText = string.Empty;
        try
        {
            inputShieldCredentialStatus.Text = "正在保存本地输入防护凭据...";
            var result = await new ControlPipeClient().UpdateInputShieldCredentialsAsync(
                passwordBytes, recovery.IsOn, CancellationToken.None);
            inputShieldCredentialStatus.Text = result.Configured
                ? "独立本地防护密码已设置。"
                : "本地防护凭据未保存。";
            if (!string.IsNullOrWhiteSpace(result.RecoveryCode))
            {
                await ShowInputShieldRecoveryCodeAsync(result.RecoveryCode);
            }
            else if (result.Configured)
            {
                await ShowInputShieldCredentialNoticeAsync(
                    "本地密码已保存",
                    "本地密码已保存，可直接用于临时键鼠控制的解锁验证。");
            }
            else
            {
                await ShowInputShieldCredentialNoticeAsync("未保存本地密码", "服务没有确认保存本地防护凭据。");
            }
            await LoadInputShieldManagementAsync();
        }
        catch (Exception exception)
        {
            var message = InputShieldCredentialFailureMessage(exception);
            inputShieldCredentialStatus.Text = message;
            await ShowInputShieldCredentialNoticeAsync("未保存本地密码", message);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(passwordBytes);
        }
    }

    private static string InputShieldCredentialFailureMessage(Exception exception)
    {
        if (exception is OperationCanceledException)
        {
            return "保存本地防护密码超时。请确认服务正在运行后重试。";
        }
        if (exception.Message.Contains("must contain 8 to 128", StringComparison.OrdinalIgnoreCase))
        {
            return "密码长度需要为 8–128 个字符。";
        }
        if (exception.Message.Contains("credentials could not be saved", StringComparison.OrdinalIgnoreCase))
        {
            return "服务无法保存本地防护密码。请确认服务状态正常且数据目录可写。";
        }
        return "无法保存本地防护密码。请确认服务正在运行后重试。";
    }

    private async Task ShowInputShieldCredentialNoticeAsync(string title, string message)
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null)
        {
            return;
        }
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            RequestedTheme = ElementTheme.Light,
            Title = title,
            Content = new TextBlock { Text = message, TextWrapping = TextWrapping.Wrap },
            CloseButtonText = "关闭",
        };
        await dialog.ShowAsync();
    }

    private async Task ShowInputShieldRecoveryCodeAsync(string recoveryCode)
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null)
        {
            return;
        }
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            RequestedTheme = ElementTheme.Light,
            Title = "请保存一次性恢复码",
            Content = new StackPanel
            {
                Width = 480,
                Spacing = 12,
                Children =
                {
                    new TextBlock
                    {
                        Text = "该恢复码关闭窗口后无法再次查看，成功使用一次后立即失效。请保存到受控位置。",
                        TextWrapping = TextWrapping.Wrap,
                        Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                    },
                    new TextBlock
                    {
                        Text = recoveryCode,
                        FontFamily = new FontFamily("Consolas"),
                        FontSize = 20,
                        FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                        IsTextSelectionEnabled = true,
                        TextWrapping = TextWrapping.Wrap,
                        Foreground = ThemeBrush("DgpPrimaryTextBrush"),
                    },
                },
            },
            CloseButtonText = "我已保存",
        };
        await dialog.ShowAsync();
    }

    private async Task DeleteInputShieldCredentialsAsync()
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null || inputShieldCredentialStatus is null)
        {
            return;
        }
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            RequestedTheme = ElementTheme.Light,
            Title = "删除本地防护凭据",
            Content = "正在运行或等待停止确认的本地密码任务需要先结束。删除后，使用本地密码的规则需要重新设置凭据才能启动。",
            PrimaryButtonText = "删除",
            CloseButtonText = "取消",
            DefaultButton = ContentDialogButton.Close,
        };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }
        try
        {
            await new ControlPipeClient().DeleteInputShieldCredentialsAsync(CancellationToken.None);
            await LoadInputShieldManagementAsync();
            await ShowInputShieldCredentialNoticeAsync("本地密码已删除", "本地防护密码及一次性恢复码已删除。");
        }
        catch (Exception exception)
        {
            var message = exception.Message.Contains("stop local-credential input control", StringComparison.OrdinalIgnoreCase)
                ? "当前临时输入控制仍依赖本地密码。请先停止任务并等待停止确认。"
                : "无法删除本地输入防护凭据。请检查后台服务后重试。";
            inputShieldCredentialStatus.Text = message;
            await ShowInputShieldCredentialNoticeAsync("本地密码未删除", message);
        }
    }

    private async Task SaveDirectoriesAsync()
    {
        if (directoryInputs is null || directoryStatus is null)
        {
            return;
        }
        var targets = directoryInputs.Children
            .OfType<Grid>()
            .Select(row => new MonitoringTargetInfo(
                row.Children.OfType<TextBox>().FirstOrDefault()?.Text.Trim() ?? string.Empty,
                row.Children.OfType<ComboBox>().FirstOrDefault()?.SelectedItem is ComboBoxItem { Tag: string kind }
                    ? kind
                    : "directory",
                row.Children.OfType<ToggleSwitch>().FirstOrDefault()?.IsOn ?? true))
            .Where(target => !string.IsNullOrWhiteSpace(target.Path))
            .GroupBy(target => target.Path, StringComparer.OrdinalIgnoreCase)
            .Select(group => group.First())
            .ToArray();
        directoryStatus.Text = "正在保存重点目录...";
        try
        {
            var result = await new ControlPipeClient().UpdateDirectoryMonitoringAsync(targets, CancellationToken.None);
			var savedTargets = (result.Targets ?? result.Directories.Select(directory =>
				new MonitoringTargetInfo(directory, "directory", true)).ToArray()).ToArray();
			var resolvedCount = targets.Zip(savedTargets, (requested, saved) =>
				!string.Equals(requested.Path, saved.Path, StringComparison.OrdinalIgnoreCase)).Count(changed => changed);
            ReplaceDirectoryInputs(savedTargets);
            directoryStatus.Text = $"已保存 {savedTargets.Length} 条监控规则。" +
				(resolvedCount > 0 ? $" 已识别并解析 {resolvedCount} 个重解析点，输入框已显示最终路径。" : " 未发现需要解析的重解析点。");
            await LoadSessionStartPreviewAsync();
        }
		catch (Exception exception)
        {
			directoryStatus.Text = $"无法保存重点目录：{exception.Message}";
        }
    }

    private async Task SaveExclusionsAsync()
    {
        if (exclusionInputs is null || exclusionStatus is null)
        {
            return;
        }
        var exclusions = exclusionInputs.Children
            .OfType<Grid>()
            .Select(row => new MonitoringExclusionInfo(
                row.Children.OfType<ComboBox>().FirstOrDefault()?.SelectedItem is ComboBoxItem { Tag: string kind }
                    ? kind
                    : "path",
                row.Children.OfType<TextBox>().FirstOrDefault()?.Text.Trim() ?? string.Empty))
            .Where(exclusion => !string.IsNullOrWhiteSpace(exclusion.Pattern))
            .GroupBy(exclusion => $"{exclusion.Kind}|{exclusion.Pattern}", StringComparer.OrdinalIgnoreCase)
            .Select(group => group.First())
            .ToArray();
        exclusionStatus.Text = "正在保存排除规则...";
        try
        {
            var result = await new ControlPipeClient().UpdateMonitoringExclusionsAsync(exclusions, CancellationToken.None);
            ReplaceExclusionInputs(result.Exclusions ?? Array.Empty<MonitoringExclusionInfo>());
            exclusionStatus.Text = $"已保存 {result.Exclusions?.Count ?? 0} 条排除规则。";
        }
        catch (Exception exception)
        {
            exclusionStatus.Text = $"无法保存排除规则：{exception.Message}";
        }
    }

    private async Task LoadSessionStartPreviewAsync()
    {
        if (sessionStartPreviewStatus is null)
        {
            return;
        }
        var mode = SelectedMonitoringMode();
        sessionStartPreviewStatus.Text = "正在生成启动预览...";
        try
        {
            var preview = await new ControlPipeClient().GetSessionStartPreviewAsync(mode, CancellationToken.None);
            if (!string.Equals(preview.MonitoringMode, SelectedMonitoringMode(), StringComparison.Ordinal))
            {
                return;
            }
            ApplySessionStartPreview(preview);
        }
        catch
        {
            sessionStartPreviewStatus.Text = "无法生成启动预览。请确认后台服务与重点目录配置可用。";
        }
    }

    private string SelectedMonitoringMode()
    {
        return monitoringMode?.SelectedItem is ComboBoxItem { Tag: string mode }
	        ? mode
	        : "standard";
    }

    private void ApplySessionStartPreview(SessionStartPreviewResult preview)
    {
        if (sessionStartPreviewStatus is null)
        {
            return;
        }
        var mode = FormatMonitoringMode(preview.MonitoringMode);
        if (dashboardModeSummary is not null)
        {
            dashboardModeSummary.Text = preview.MonitoringMode switch
            {
                "relaxed" => "轻量记录文件与设备变化",
                "strict" => "增加文件读取审计与密集采样",
                "custom" => "按已保存的自定义规则采集",
                _ => "记录文件、进程、系统与设备变化",
            };
        }
        if (dashboardTargetSummary is not null)
        {
            dashboardTargetSummary.Text = $"{preview.MonitoredTargetCount} 个目标 · 管理";
        }
        var administrator = preview.Impact.RequiresAdministrator ? "需要管理员权限" : "不需要管理员权限";
        var strictAuditNotice = preview.MonitoringLevel == "strict"
            ? "\n严格读取审计会记录文件读取与打开活动，需启用 Windows“文件系统”对象访问审核，并为重点目录配置相应审核项；日志量可能明显增加。"
            : string.Empty;
        var capabilities = FormatModeCapabilities(preview.MonitoringPolicy);
        sessionStartPreviewStatus.Text =
			$"监控目标    {preview.MonitoredTargetCount} 个\n" +
            $"启用模块    {capabilities}\n" +
			$"权限要求    {administrator}\n" +
            $"资源影响    事件量{FormatMonitoringImpact(preview.Impact.ExpectedEventVolume)}、" +
            $"性能{FormatMonitoringImpact(preview.Impact.PerformanceImpact)}、" +
            $"存储{FormatMonitoringImpact(preview.Impact.StorageImpact)}。{strictAuditNotice}";
    }

    private async Task<bool> ConfirmSessionStartAsync(SessionStartPreviewResult preview)
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null)
        {
            return false;
        }
        var mode = FormatMonitoringMode(preview.MonitoringMode);
        var administrator = preview.Impact.RequiresAdministrator
            ? "\n严格读取审计需要管理员权限。请确认 Windows“文件系统”对象访问审核与重点目录审核项已启用；该模式可能显著增加日志量与性能开销。"
            : string.Empty;
        var capabilities = FormatModeCapabilities(preview.MonitoringPolicy);
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            Title = "确认开启保护",
            Content = new TextBlock
            {
                Text =
                    $"监控范围：{preview.MonitoredTargetCount} 个监控目标\n{FormatMonitoringTargets(preview.Targets)}\n" +
                    $"保护模式：{mode}\n" +
                    $"启用功能：{capabilities}\n" +
                    $"预计影响：事件量{FormatMonitoringImpact(preview.Impact.ExpectedEventVolume)}、" +
                    $"性能{FormatMonitoringImpact(preview.Impact.PerformanceImpact)}、" +
                    $"存储{FormatMonitoringImpact(preview.Impact.StorageImpact)}。{administrator}",
                TextWrapping = TextWrapping.Wrap,
            },
            PrimaryButtonText = "开始保护",
            CloseButtonText = "取消",
            DefaultButton = ContentDialogButton.Primary,
        };
        return await dialog.ShowAsync() == ContentDialogResult.Primary;
    }

    private static string FormatMonitoringMode(string mode)
    {
        return mode switch
        {
            "relaxed" => "宽松",
            "strict" => "严格",
            "custom" => "自定义",
            _ => "标准",
        };
    }

    private static string FormatModeCapabilities(MonitoringPolicyInfo? policy)
    {
        if (policy is null)
        {
            return "等待后台返回功能清单";
        }
        var capabilities = new List<string>();
        if (policy.FileActivityEnabled)
        {
            capabilities.Add(policy.StrictReadAuditEnabled ? "文件变化与严格读取" : "文件与目录变化");
        }
        if (policy.ProcessAndSoftwareEnabled)
        {
            capabilities.Add("进程与软件");
        }
        if (policy.SystemAndNetworkEnabled)
        {
            capabilities.Add("系统与网络状态");
        }
        if (policy.ExternalDevicesEnabled)
        {
            capabilities.Add("USB与外接设备");
        }
        if (policy.UserSessionActivityEnabled)
        {
            capabilities.Add("键鼠与前台活动");
        }
        if (policy.InputShieldEnabled)
        {
            capabilities.Add("本地输入防护（实验室）");
        }
        return capabilities.Count == 0 ? "未选择功能" : string.Join("、", capabilities);
    }

	private static string FormatMonitoringTargets(IReadOnlyList<MonitoringTargetInfo>? targets)
	{
		if (targets is null || targets.Count == 0)
		{
			return "  未配置监控目标";
		}
		return string.Join("\n", targets.Select(target =>
		{
			var kind = target.Kind switch
			{
				"file" => "文件",
				"removable_volume" => "可移动卷",
				_ => "目录",
			};
			var recursion = target.Kind == "directory" ? (target.Recursive ? "，递归" : "，仅当前层") : string.Empty;
			return $"  {kind}：{target.Path}{recursion}";
		}));
	}

    private async Task<string?> ConfirmBaselineReviewAsync(SessionBaselineReviewResult review)
    {
        if (mainWindow?.Content is not FrameworkElement root || root.XamlRoot is null)
        {
            return null;
        }
        var failures = review.Decision.Failures.Count == 0
            ? "未提供失败项详情。"
            : string.Join("\n", review.Decision.Failures.Select(failure => $"{failure.Item}：{failure.Reason}"));
        var dialog = new ContentDialog
        {
            XamlRoot = root.XamlRoot,
            Title = "处理基线失败项",
            Content = new ScrollViewer
            {
                MaxHeight = 360,
                Content = new TextBlock
                {
                    Text = $"以下基线项目未能完成采集：\n{failures}\n\n继续保护会保留失败项记录；取消将结束本次保护会话。",
                    TextWrapping = TextWrapping.Wrap,
                },
            },
            PrimaryButtonText = review.Decision.CanContinue ? "继续保护" : "继续不可用",
            CloseButtonText = review.Decision.CanCancel ? "取消保护" : "关闭",
            IsPrimaryButtonEnabled = review.Decision.CanContinue,
            DefaultButton = review.Decision.CanContinue ? ContentDialogButton.Primary : ContentDialogButton.Close,
        };
        var result = await dialog.ShowAsync();
        if (result == ContentDialogResult.Primary)
        {
            return "continue";
        }
        return review.Decision.CanCancel ? "cancel" : null;
    }

    private static string FormatMonitoringImpact(string impact)
    {
        return impact switch
        {
            "low" => "低",
            "medium" => "中",
            "high" => "高",
            _ => impact,
        };
    }

    private static string FormatMonitoringLevel(string level)
    {
        return level == "strict" ? "严格读取审计" : "标准监控";
    }

    private static string FormatSessionMode(SessionInfo session)
    {
        var mode = session.MonitoringPolicy?.Mode;
        return string.IsNullOrWhiteSpace(mode)
            ? FormatMonitoringLevel(session.MonitoringLevel)
            : $"{FormatMonitoringMode(mode)}模式";
    }

    private void ReplaceDirectoryInputs(IEnumerable<MonitoringTargetInfo> targets)
    {
        if (directoryInputs is null)
        {
            return;
        }
        directoryInputs.Children.Clear();
        foreach (var target in targets)
        {
            AddDirectoryInput(
                target.ConfiguredPath ?? target.Path,
                target.Recursive,
                target.Kind,
                target.ResolutionStatus,
                target.ResolutionDetail);
        }
        if (directoryInputs.Children.Count == 0)
        {
            AddDirectoryInput();
        }
    }

    private void AddDirectoryInput(
        string value = "",
        bool recursive = true,
        string kind = "directory",
        string? resolutionStatus = null,
        string? resolutionDetail = null)
    {
        if (directoryInputs is null)
        {
            return;
        }
        var input = new TextBox
        {
            Text = value,
            PlaceholderText = "输入本地目录或单文件路径",
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var removeButton = new Button
        {
            Content = "删除",
            MinWidth = 64,
            HorizontalAlignment = HorizontalAlignment.Right,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var recursiveSwitch = new ToggleSwitch
        {
            Header = "监控子目录",
            IsOn = recursive,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var targetKind = new ComboBox
        {
            MinWidth = 92,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var resolution = new TextBlock
        {
            Text = resolutionStatus switch
            {
                "reparse_resolved" => "重解析已处理",
                "available" => "路径可用",
                _ => "等待保存验证",
            },
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            FontSize = 12,
            VerticalAlignment = VerticalAlignment.Center,
        };
        if (!string.IsNullOrWhiteSpace(resolutionDetail))
        {
            ToolTipService.SetToolTip(resolution, resolutionDetail);
        }
        targetKind.Items.Add(new ComboBoxItem { Content = "目录", Tag = "directory" });
        targetKind.Items.Add(new ComboBoxItem { Content = "单文件", Tag = "file" });
        targetKind.Items.Add(new ComboBoxItem { Content = "可移动卷", Tag = "removable_volume" });
        targetKind.SelectedIndex = kind switch
        {
            "file" => 1,
            "removable_volume" => 2,
            _ => 0,
        };
        recursiveSwitch.IsEnabled = kind == "directory";
        targetKind.SelectionChanged += (_, _) =>
        {
            var isDirectory = targetKind.SelectedItem is ComboBoxItem { Tag: "directory" };
            recursiveSwitch.IsEnabled = isDirectory;
            if (!isDirectory)
            {
                recursiveSwitch.IsOn = false;
            }
        };
        var row = new Grid
        {
            ColumnSpacing = 8,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = GridLength.Auto },
            },
        };
        removeButton.Click += (_, _) =>
        {
            directoryInputs.Children.Remove(row);
            if (directoryInputs.Children.Count == 0)
            {
                AddDirectoryInput();
            }
        };
        Grid.SetColumn(input, 0);
        Grid.SetColumn(resolution, 1);
        Grid.SetColumn(targetKind, 2);
        Grid.SetColumn(recursiveSwitch, 3);
        Grid.SetColumn(removeButton, 4);
        row.Children.Add(input);
        row.Children.Add(resolution);
        row.Children.Add(targetKind);
        row.Children.Add(recursiveSwitch);
        row.Children.Add(removeButton);
        directoryInputs.Children.Add(row);
    }

    private void ReplaceExclusionInputs(IEnumerable<MonitoringExclusionInfo> exclusions)
    {
        if (exclusionInputs is null)
        {
            return;
        }
        exclusionInputs.Children.Clear();
        foreach (var exclusion in exclusions)
        {
            AddExclusionInput(exclusion.Pattern, exclusion.Kind);
        }
        if (exclusionInputs.Children.Count == 0)
        {
            AddExclusionInput();
        }
    }

    private void AddExclusionInput(string pattern = "", string kind = "path")
    {
        if (exclusionInputs is null)
        {
            return;
        }
        var input = new TextBox
        {
            Text = pattern,
            PlaceholderText = "输入路径、文件名、扩展名或进程名",
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var exclusionKind = new ComboBox
        {
            MinWidth = 96,
            VerticalAlignment = VerticalAlignment.Center,
        };
        exclusionKind.Items.Add(new ComboBoxItem { Content = "路径", Tag = "path" });
        exclusionKind.Items.Add(new ComboBoxItem { Content = "文件名", Tag = "file_name" });
        exclusionKind.Items.Add(new ComboBoxItem { Content = "扩展名", Tag = "extension" });
        exclusionKind.Items.Add(new ComboBoxItem { Content = "进程", Tag = "process" });
        exclusionKind.SelectedIndex = kind switch
        {
            "file_name" => 1,
            "extension" => 2,
            "process" => 3,
            _ => 0,
        };
        var removeButton = new Button
        {
            Content = "删除",
            MinWidth = 64,
            HorizontalAlignment = HorizontalAlignment.Right,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var row = new Grid
        {
            ColumnSpacing = 8,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = GridLength.Auto },
            },
        };
        removeButton.Click += (_, _) =>
        {
            exclusionInputs.Children.Remove(row);
            if (exclusionInputs.Children.Count == 0)
            {
                AddExclusionInput();
            }
        };
        Grid.SetColumn(input, 0);
        Grid.SetColumn(exclusionKind, 1);
        Grid.SetColumn(removeButton, 2);
        row.Children.Add(input);
        row.Children.Add(exclusionKind);
        row.Children.Add(removeButton);
        exclusionInputs.Children.Add(row);
    }

    private async Task LoadHistoryAsync(bool append)
    {
        if (historyLoading || historyList is null || historyStatus is null || loadMoreHistoryButton is null)
        {
            return;
        }
        historyLoading = true;
        loadMoreHistoryButton.IsEnabled = false;
        historyStatus.Text = "正在读取历史会话...";
        try
        {
            var page = await new ControlPipeClient().ListSessionsAsync(
                append ? historyCursor : "", CancellationToken.None);
            if (!append)
            {
                historyList.Items.Clear();
            }
            foreach (var item in page.Items)
            {
                var row = new StackPanel
                {
                    Spacing = 8,
                };
                row.Children.Add(new TextBlock
                {
                    Text = item.Session.Name,
                    FontSize = 15,
                    TextWrapping = TextWrapping.Wrap,
                    Foreground = ThemeBrush("DgpDashboardTextBrush"),
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                });
                var openSession = CreateWorkspaceButton("打开会话", true);
                openSession.Click += async (_, _) =>
                {
                    SelectAnalysisSession(AnalysisSessionFrom(item.Session));
                    await NavigateAnalysisAsync("audit");
                };
                var rowActions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
                row.Children.Add(new TextBlock
                {
                    Text = $"{FormatSessionState(item.Session.State)} · 创建于 {item.CreatedUtc.LocalDateTime:yyyy-MM-dd HH:mm:ss} · " +
                        (item.RetentionLocked ? "已锁定保留" : "可按保留策略清理"),
                    Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                    TextWrapping = TextWrapping.Wrap,
                });
                if (item.Session.State is "completed" or "failed")
                {
                    var retentionButton = CreateWorkspaceButton(item.RetentionLocked ? "解除保留锁" : "锁定保留");
                    retentionButton.Click += async (_, _) =>
                        await UpdateHistoryRetentionLockAsync(item.Session.Id, !item.RetentionLocked);
                    rowActions.Children.Add(retentionButton);
                }
                rowActions.Children.Add(openSession);
                historyList.Items.Add(new ListViewItem
                {
                    Content = CreateWorkspaceToolbar(row, rowActions),
                });
            }
            historyCursor = page.NextCursor ?? "";
            historyHasMore = page.HasMore && historyCursor.Length > 0;
            historyStatus.Text = $"已显示 {historyList.Items.Count} 条会话记录。";
        }
        catch
        {
            historyStatus.Text = "无法读取历史会话。";
        }
        finally
        {
            historyLoading = false;
            loadMoreHistoryButton.IsEnabled = historyHasMore;
        }
    }

    private async Task UpdateHistoryRetentionLockAsync(string sessionId, bool locked)
    {
        if (historyStatus is null || historyLoading)
        {
            return;
        }
        historyStatus.Text = locked ? "正在锁定会话保留..." : "正在解除会话保留锁...";
        try
        {
            var result = await new ControlPipeClient().UpdateSessionRetentionLockAsync(
                sessionId, locked, CancellationToken.None);
            await LoadHistoryAsync(false);
            historyStatus.Text = result.Locked ? "已锁定会话保留。" : "已解除会话保留锁。";
        }
        catch
        {
            historyStatus.Text = "无法更新会话保留状态。";
        }
    }

    private async Task LoadTimelineAsync(bool append)
    {
        if (timelineList is null || timelineStatus is null || loadMoreTimelineButton is null) return;
        var request = ++timelineRequest;
        loadMoreTimelineButton.IsEnabled = false;
        timelineStatus.Text = "正在读取事件时间线...";
        try
        {
            var client = new ControlPipeClient();
            var session = await ResolveAnalysisSessionAsync(client);
            request = ++timelineRequest;
            if (session is null)
            {
                timelineList.Items.Clear();
                timelineStatus.Text = "还没有可查询的保护会话。请先开启保护或选择历史记录。";
                return;
            }
            var revision = analysisWorkspace.Revision;
            if (!TryReadTimelineTime(timelineFromFilter?.Text, out var fromUtc) ||
                !TryReadTimelineTime(timelineToFilter?.Text, out var toUtc) ||
                (fromUtc.HasValue && toUtc.HasValue && fromUtc > toUtc))
            {
                timelineStatus.Text = "时间筛选无效，请检查日期格式及起止顺序。";
                timelineHasMore = false;
                timelineList.Items.Clear();
                if (timelineDetail is not null) timelineDetail.Text = "请修正时间筛选后重新查询。";
                return;
            }
            var categories = timelineCategoryFilter?.SelectedItem is ComboBoxItem { Tag: string category } && category.Length > 0
                ? new[] { category } : Array.Empty<string>();
            var severities = timelineSeverityFilter?.SelectedItem is ComboBoxItem { Tag: string severity } && severity.Length > 0
                ? new[] { severity } : Array.Empty<string>();
            var user = timelineUserFilter?.Text.Trim() ?? "";
            var process = timelineProcessFilter?.Text.Trim() ?? "";
            var path = timelinePathFilter?.Text.Trim() ?? "";
            var queryKey = JsonSerializer.Serialize(new { session.Id, categories, severities, user, process, path, fromUtc, toUtc });
            var cursor = timelinePaging.Begin(queryKey, append);
            if (cursor.Length == 0)
            {
                timelineList.Items.Clear();
                if (timelineDetail is not null) timelineDetail.Text = "选择一条事件以查看详情。";
            }
            var evidenceId = pendingEvidenceId;
            do
            {
                var page = await client.QueryTimelineAsync(session.Id, cursor, categories, severities,
                    user, process, path, fromUtc, toUtc, CancellationToken.None);
                if (request != timelineRequest || !analysisWorkspace.IsCurrent(session.Id, revision)) return;
                foreach (var record in page.Records)
                {
                    var eventInfo = record.Event;
                    var hashStatus = ReadContentHashStatus(record.Payload);
                    var row = new ListViewItem
                    {
                        Tag = (eventInfo, hashStatus, record.Payload, record.PreviewTruncated),
                        Content = new StackPanel
                        {
                            Spacing = 4, Margin = new Thickness(0, 4, 0, 4),
                            Children =
                            {
                                new TextBlock
                                {
                                    Text = $"#{eventInfo.Sequence} · {FormatTimelineCategory(eventInfo.Category)} · {FormatTimelineAction(eventInfo.Action)}",
                                    FontSize = 14,
                                    TextWrapping = TextWrapping.Wrap,
                                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                                },
                                new TextBlock
                                {
                                    Text = $"{eventInfo.ObservedUtc.LocalDateTime:yyyy-MM-dd HH:mm:ss} · {eventInfo.ObjectKey ?? "未提供对象"}" +
                                        $" · 进程 {eventInfo.ProcessKey ?? "未归因"} · {eventInfo.Source}",
                                    Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                                    TextWrapping = TextWrapping.Wrap,
                                },
                            },
                        },
                    };
                    timelineList.Items.Add(row);
                    if (evidenceId == eventInfo.EventId)
                    {
                        timelineList.SelectedItem = row;
                        timelineList.ScrollIntoView(row);
                        pendingEvidenceId = null;
                        evidenceId = null;
                    }
                }
                timelineCursor = page.NextCursor ?? "";
                timelinePaging.Complete(timelineCursor);
                timelineHasMore = page.HasMore && timelineCursor.Length > 0;
                var coverageGaps = page.Records.Count(record => IsCoverageGapAction(record.Event.Action));
                var pendingHashes = page.Records.Count(record => ReadContentHashStatus(record.Payload) == "waiting");
                timelineStatus.Text = page.IntegrityVerified
                    ? $"已显示 {timelineList.Items.Count} 条事件，本页完整性已验证。{FormatTimelineHealthStatus(coverageGaps, pendingHashes)}"
                    : $"已显示 {timelineList.Items.Count} 条事件，完整性等待验证。{FormatTimelineHealthStatus(coverageGaps, pendingHashes)}";
                cursor = timelineCursor;
            } while (evidenceId is not null && timelineHasMore);
            if (evidenceId is not null)
            {
                timelineStatus.Text = "未找到这条证据事件，记录可能已清理；可重置筛选后重试。";
                pendingEvidenceId = null;
            }
        }
        catch
        {
            if (request != timelineRequest) return;
            timelineList.Items.Clear();
            if (timelineDetail is not null) timelineDetail.Text = "事件详情暂不可用。";
            timelineHasMore = false;
            timelineStatus.Text = "无法读取所选会话的事件时间线，请检查服务后重试。";
        }
        finally
        {
            if (request == timelineRequest) loadMoreTimelineButton.IsEnabled = timelineHasMore;
        }
    }
    private static bool TryReadTimelineTime(string? value, out DateTimeOffset? utc)
    {
        utc = null;
        if (string.IsNullOrWhiteSpace(value))
        {
            return true;
        }
        if (!DateTimeOffset.TryParse(value.Trim(), out var parsed))
        {
            return false;
        }
        utc = parsed.ToUniversalTime();
        return true;
    }

    private async Task LoadRiskAsync()
    {
        if (riskList is null || riskStatus is null)
        {
            return;
        }
        riskStatus.Text = "正在评估会话风险...";
        riskList.Items.Clear();
        var request = ++riskRequest;
        try
        {
            var client = new ControlPipeClient();
            var session = await ResolveAnalysisSessionAsync(client);
            request = ++riskRequest;
            if (session is null)
            {
                riskStatus.Text = "当前没有可评估的保护会话。";
                return;
            }
            var revision = analysisWorkspace.Revision;
            var evaluation = await client.EvaluateRiskAsync(session.Id, CancellationToken.None);
            if (request != riskRequest || !analysisWorkspace.IsCurrent(session.Id, revision)) return;
            riskList.Items.Clear();
            foreach (var finding in evaluation.Findings)
            {
                var actions = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
                var evidenceLinks = new StackPanel { Spacing = 6 };
                foreach (var evidence in finding.Evidence)
                {
                    var link = CreateWorkspaceButton($"查看事件 #{evidence.Sequence}");
                    link.Click += async (_, _) => await OpenRiskEvidenceAsync(session, evidence);
                    evidenceLinks.Children.Add(link);
                }
                foreach (var choice in new[]
                {
                    (Label: "已知", Status: "known"),
                    (Label: "待确认", Status: "pending_review"),
                    (Label: "需处理", Status: "action_needed"),
                })
                {
                    var button = CreateWorkspaceButton(choice.Label);
                    button.IsEnabled = finding.Status != choice.Status;
                    button.Click += async (_, _) => await UpdateRiskFindingStatusAsync(
                        evaluation.SessionId, finding.Id, choice.Status);
                    actions.Children.Add(button);
                }
				var evidenceLines = finding.Evidence.Select(evidence =>
					$"#{evidence.Sequence}  {evidence.ObservedUtc.ToLocalTime():yyyy-MM-dd HH:mm:ss}\n" +
					$"事件 {evidence.EventId}\n类别 {FormatTimelineCategory(evidence.Category)}  动作 {FormatTimelineAction(evidence.Action)}\n" +
					$"对象 {evidence.ObjectKey ?? "（无）"}");
                var evidenceDetails = CreateDashboardExpander($"关联事件（{finding.Evidence.Count}）",
                    new StackPanel
                    {
                        Spacing = 12,
                        Children =
                        {
                            new TextBlock
					{
						Text = $"触发规则：{finding.RuleId}\n" +
							$"标签：{string.Join("、", finding.Tags ?? Array.Empty<string>())}\n" +
							$"观测区间：{finding.FirstObservedUtc.ToLocalTime():yyyy-MM-dd HH:mm:ss} 至 {finding.LastObservedUtc.ToLocalTime():yyyy-MM-dd HH:mm:ss}\n\n" +
							string.Join("\n\n", evidenceLines),
						TextWrapping = TextWrapping.Wrap,
						IsTextSelectionEnabled = true,
						Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                            },
                            evidenceLinks,
                        },
                    });
                riskList.Items.Add(new ListViewItem
                {
                    Content = new StackPanel
                    {
                        Spacing = 12,
                        Margin = new Thickness(0, 4, 0, 4),
                        Children =
                        {
                            new TextBlock
                            {
                                Text = $"{FormatRiskLevel(finding.Level)}风险 · {finding.Title}",
                                FontSize = 15,
                                TextWrapping = TextWrapping.Wrap,
                                FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                            },
                            new TextBlock
                            {
                                Text = finding.Summary,
                                TextWrapping = TextWrapping.Wrap,
                            },
                            new TextBlock
                            {
                                Text = $"状态 {FormatRiskStatus(finding.Status)} · 评分 {finding.Score} · 置信度 {finding.Confidence:P0} · 证据 {finding.Evidence.Count} · 规则 {finding.RuleId}",
                                Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                                TextWrapping = TextWrapping.Wrap,
                            },
							evidenceDetails,
                            actions,
                        },
                    },
                });
            }
            riskStatus.Text = $"会话事件 {evaluation.EventCount} 条，风险发现 {evaluation.Findings.Count} 项，采集缺口 {evaluation.CoverageGapCount} 个，规则故障 {evaluation.Failures.Count} 项。";
        }
        catch
        {
            if (request == riskRequest) riskStatus.Text = "无法评估所选会话风险，请检查服务后重试。";
        }
    }

    private async Task UpdateRiskFindingStatusAsync(string sessionId, string findingId, string status)
    {
        if (riskStatus is null)
        {
            return;
        }
        riskStatus.Text = "正在保存风险处置状态...";
        try
        {
            await new ControlPipeClient().UpdateRiskFindingStatusAsync(sessionId, findingId, status, CancellationToken.None);
            if (analysisWorkspace.Session?.Id == sessionId) await LoadRiskAsync();
        }
        catch
        {
            riskStatus.Text = "无法保存风险处置状态。";
        }
    }

    private async Task LoadAssetDifferencesAsync()
    {
        if (assetList is null || assetStatus is null)
        {
            return;
        }
        assetStatus.Text = "正在读取资产变化...";
        assetList.Items.Clear();
        if (assetDetail is not null) assetDetail.Text = "选择一项资产变化以查看详情。";
        var request = ++assetRequest;
        try
        {
            var client = new ControlPipeClient();
            var session = await ResolveAnalysisSessionAsync(client);
            request = ++assetRequest;
            if (session is null)
            {
                assetStatus.Text = "当前没有可查询的保护会话。";
                return;
            }
            var revision = analysisWorkspace.Revision;
            var result = await client.QueryAssetDifferencesAsync(
                session.Id, assetCategories.ToArray(), CancellationToken.None);
            if (request != assetRequest || !analysisWorkspace.IsCurrent(session.Id, revision)) return;
            assetList.Items.Clear();
            if (!result.BaselineAvailable)
            {
                assetStatus.Text = session.AssetMonitoringEnabled == false
                    ? "这次会话未启用软件、系统或设备资产采集，没有资产差异可比较。"
                    : session.State is "active" or "paused" or "preparing" or "degraded" or "finalizing"
                    ? "保护结束并完成收尾后生成资产差异。"
                    : "这次会话缺少完整的开始/结束资产快照；可能未启用资产采集或采集未完成。可在审计中查看采集健康事件。";
                return;
            }
            foreach (var difference in result.Differences)
            {
                var asset = AssetFor(difference);
                if (asset is null)
                {
                    continue;
                }
                assetList.Items.Add(new ListViewItem
                {
                    Tag = difference,
                    Content = new StackPanel
                    {
                        Spacing = 4,
                        Margin = new Thickness(0, 4, 0, 4),
                        Children =
                        {
                            new TextBlock
                            {
                                Text = $"{FormatAssetDifferenceKind(difference.Kind)} · {FormatAssetCategory(asset.Category)} · {asset.DisplayName}",
                                FontSize = 14,
                                TextWrapping = TextWrapping.Wrap,
                                FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                            },
                            new TextBlock
                            {
                                Text = asset.Identifier,
                                Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                                TextWrapping = TextWrapping.Wrap,
                            },
                        },
                    },
                });
            }
            assetStatus.Text = $"已显示 {assetList.Items.Count} 项资产变化。";
        }
        catch
        {
            if (request == assetRequest) assetStatus.Text = "无法读取所选会话资产变化，请检查服务后重试。";
        }
    }

    private async Task ExportReportAsync()
    {
        if (reportExporting || reportStatus is null || reportFormat?.SelectedItem is not ComboBoxItem formatItem ||
            reportObjectDetails?.SelectedItem is not ComboBoxItem detailItem)
        {
            return;
        }
        var format = formatItem.Tag as string ?? "html";
        var objectDetails = detailItem.Tag as string ?? "basename";
        var includeProcessKey = reportIncludeProcessKey?.IsChecked == true;
        var includePayload = reportIncludePayload?.IsChecked == true;
        var includeUsernames = reportIncludeUsernames?.IsChecked == true;
        var includeWindowTitles = reportIncludeWindowTitles?.IsChecked == true;
		var sensitive = objectDetails == "full" || includeProcessKey || includePayload || includeUsernames || includeWindowTitles;
        if (sensitive && reportSensitiveAcknowledgement?.IsChecked != true)
        {
            reportStatus.Text = "当前设置会包含敏感审计信息，请先确认后再导出。";
            return;
        }
        if (!ReportSequenceRange.TryParse(reportFromSequence?.Text ?? "", reportToSequence?.Text ?? "",
                out var fromSequence, out var toSequence, out var rangeError))
        {
            reportStatus.Text = rangeError;
            return;
        }
        reportExporting = true;
        reportStatus.Text = "正在生成并校验报告...";
        try
        {
            var client = new ControlPipeClient();
            var session = await ResolveAnalysisSessionAsync(client);
            if (session is null)
            {
                reportStatus.Text = "当前没有可导出的保护会话。";
                return;
            }
            var result = await client.ExportReportAsync(
                session.Id, format,
                new { objectDetails, includeProcessKey, includePayload, includeUsernames, includeWindowTitles, maximumTextLength = 512 },
                fromSequence, toSequence, CancellationToken.None);
            if (mainWindow is null)
            {
                return;
            }
            var extension = format == "json" ? ".json" : format == "markdown" ? ".md" : ".html";
            var picker = new FileSavePicker
            {
                SuggestedFileName = $"{SanitizeFileName(session.Name)}-{result.GeneratedUtc:yyyyMMdd-HHmmss}",
                SuggestedStartLocation = PickerLocationId.DocumentsLibrary,
            };
            picker.FileTypeChoices.Add(format.ToUpperInvariant() + " 报告", new List<string> { extension });
            WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(mainWindow));
            var reportFile = await picker.PickSaveFileAsync();
            if (reportFile is null)
            {
                reportStatus.Text = "已取消保存报告。";
                return;
            }
            await FileIO.WriteTextAsync(reportFile, result.Content!);
            var manifestPath = Path.Combine(
                Path.GetDirectoryName(reportFile.Path)!,
                $"{Path.GetFileNameWithoutExtension(reportFile.Path)}.verification.json");
            await File.WriteAllTextAsync(
                manifestPath,
                JsonSerializer.Serialize(result.Verification, new JsonSerializerOptions { WriteIndented = true }),
                CancellationToken.None);
            reportStatus.Text = $"会话“{session.Name}”的报告和验证清单已保存到 {Path.GetDirectoryName(reportFile.Path)}。";
        }
        catch (Exception exception)
        {
            reportStatus.Text = exception is InvalidOperationException
                ? $"报告未生成：{exception.Message}"
                : "无法生成或保存报告，请检查服务与保存目录权限；大报告可填写较小序号范围重试。";
        }
        finally
        {
            reportExporting = false;
        }
    }

    private static string SanitizeFileName(string value)
    {
        var invalid = Path.GetInvalidFileNameChars();
        var sanitized = new string(value.Select(character => invalid.Contains(character) ? '-' : character).ToArray()).Trim();
        return sanitized.Length == 0 ? "保护会话" : sanitized.Length > 64 ? sanitized[..64] : sanitized;
    }

    private static AssetInfo? AssetFor(AssetDifference difference)
    {
        return difference.After ?? difference.Before;
    }

    private static string FormatAssetCategory(string category)
    {
        return category switch
        {
            "software" => "软件",
            "device" => "设备",
            "network" => "网络",
            "account" => "账户",
            "system" => "系统",
            _ => category,
        };
    }

    private static string FormatAssetDifferenceKind(string kind)
    {
        return kind switch
        {
            "added" => "新增",
            "removed" => "移除",
            "changed" => "变更",
            _ => kind,
        };
    }

    private static string FormatDevicePresence(string? status)
    {
        return status switch
        {
            "newly_discovered" => "新发现设备",
            "missing_at_session_end" => "会话结束仍缺失",
            "temporarily_removed" => "临时移除",
            _ => string.Empty,
        };
    }

    private static string FormatRiskLevel(string level)
    {
        return level switch
        {
            "critical" => "严重",
            "high" => "高",
            "medium" => "中",
            "low" => "低",
            "informational" => "提示",
            _ => level,
        };
    }

    private static string FormatRiskStatus(string status)
    {
        return status switch
        {
            "known" => "已知",
            "pending_review" => "待确认",
            "action_needed" => "需处理",
            _ => "待确认",
        };
    }

    private static string FormatTimelineCategory(string category)
    {
        return category switch
        {
            "file" => "文件",
            "process" => "进程",
            "software" => "软件",
            "system" => "系统",
            "device" => "设备",
            "health" => "采集健康",
            _ => category,
        };
    }

    private static string FormatTimelineAction(string action)
    {
        return action switch
        {
            "file_hash_completed" => "文件哈希完成",
            "process_started" => "程序曾运行",
            "process_stopped" => "程序已退出",
            "agent_activity_summary" => "应用曾处于前台",
            "file_hash_queue_overflow" => "文件哈希队列溢出",
            "observation_queue_overflow" => "事件队列溢出",
            "directory_snapshot_required" => "需要目录重扫",
            "usn_journal_gap_detected" => "USN 日志采集缺口",
            "strict_read_audit_unavailable" => "严格读取审计不可用",
            "system_asset_snapshot_unavailable" => "系统资产快照不可用",
            "security_log_monitor_unavailable" => "安全日志监控不可用",
			"process_snapshot_unavailable" => "进程快照不可用",
			"process_snapshot_reconciled" => "进程快照已校正",
			"software_repair_monitor_unavailable" => "软件修复日志监控不可用",
            "windows_security_log_cleared" => "Windows 安全日志已清理",
			"system_clock_jump_detected" => "检测到系统时间跳变",
			"time_configuration_changed" => "时间或同步配置已变化",
			"audit_policy_changed" => "关键审计策略已变化",
			"firewall_configuration_changed" => "防火墙配置已变化",
			"remote_desktop_configuration_changed" => "远程桌面配置已变化",
			"security_center_status_changed" => "Windows 安全中心状态已变化",
			"proxy_configuration_changed" => "系统代理配置已变化",
			"account_configuration_changed" => "本地用户或管理员组已变化",
			"network_configuration_changed" => "网络配置已变化",
			"service_added" => "服务已新增",
			"service_removed" => "服务已移除",
			"service_changed" => "服务配置已变化",
			"driver_added" => "驱动已新增",
			"driver_removed" => "驱动已移除",
			"driver_changed" => "驱动配置已变化",
			"scheduled_task_added" => "计划任务已新增",
			"scheduled_task_removed" => "计划任务已移除",
			"scheduled_task_changed" => "计划任务已变化",
			"startup_item_added" => "开机启动项已新增",
			"startup_item_removed" => "开机启动项已移除",
			"startup_item_changed" => "开机启动项已变化",
            "device_connected" => "外接设备首次接入",
            "device_temporarily_removed" => "外接设备临时移除",
            "device_reconnected" => "外接设备重新接入",
			"removable_volume_unavailable" => "可移动卷已移除或不可用",
            "software_installed" => "软件已安装",
            "software_uninstalled" => "软件已卸载",
            "software_upgraded" => "软件已升级",
			"software_repaired" => "软件已修复",
            "software_inventory_modified" => "软件清单已修改",
            "software_installer_started" => "安装器已启动",
            "portable_program_executed" => "便携程序已运行",
            "service_recovered_after_interruption" => "服务中断后恢复",
			"system_sleep_started" => "系统开始睡眠",
			"service_recovered_after_sleep" => "系统睡眠后恢复",
            _ => action.Replace('_', ' '),
        };
    }

    private static string FormatHashStatus(string status)
    {
        return status switch
        {
            "waiting" => "等待后台限速哈希",
            "available" => "SHA-256 已完成",
            "unavailable" => "哈希不可用",
            "canceled" => "哈希已取消",
            _ => status,
        };
    }

    private static bool IsCoverageGapAction(string action)
    {
        return action is "observation_queue_overflow" or "directory_snapshot_required" or
            "service_recovered_after_interruption" or "usn_journal_gap_detected" or
			"service_recovered_after_sleep" or
            "file_hash_queue_overflow" or "strict_read_audit_unavailable" or
            "system_asset_snapshot_unavailable" or "security_log_monitor_unavailable" or
            "process_snapshot_unavailable" or "software_repair_monitor_unavailable";
    }

    private static string FormatTimelineHealthStatus(int coverageGaps, int pendingHashes)
    {
        var details = new List<string>();
        if (coverageGaps > 0)
        {
            details.Add($"本页发现 {coverageGaps} 个采集缺口");
        }
        if (pendingHashes > 0)
        {
            details.Add($"{pendingHashes} 个大文件等待哈希");
        }
        return details.Count == 0 ? string.Empty : $" {string.Join("；", details)}。";
    }

    private static string? ReadContentHashStatus(string? encodedPayload)
    {
        if (string.IsNullOrWhiteSpace(encodedPayload))
        {
            return null;
        }
        try
        {
            using var document = JsonDocument.Parse(Encoding.UTF8.GetString(Convert.FromBase64String(encodedPayload)));
            return document.RootElement.TryGetProperty("contentHashStatus", out var status) && status.ValueKind == JsonValueKind.String
                ? status.GetString()
                : null;
        }
        catch (FormatException)
        {
            return null;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static string FormatSessionState(string state)
    {
        return state switch
        {
            "draft" => "草稿",
            "preparing" => "准备中",
            "baseline_review" => "基线待处理",
            "active" => "保护中",
            "degraded" => "降级保护",
            "paused" => "已暂停",
            "finalizing" => "正在结束",
            "completed" => "已完成",
            "failed" => "失败",
            _ => state,
        };
    }

    private FrameworkElement CreateShell(
        out TextBlock serviceStatus,
        out StackPanel directoryInputs,
        out TextBlock directoryStatus,
        out StackPanel exclusionInputs,
        out TextBlock exclusionStatus,
        out ComboBox monitoringMode,
        out TextBlock sessionStartPreviewStatus,
        out TextBox sessionName,
        out TextBlock sessionStatus,
        out Button protectionButton,
        out ListView historyList,
        out TextBlock historyStatus,
        out Button loadMoreHistoryButton,
        out ListView timelineList,
        out TextBlock timelineStatus,
        out TextBlock timelineDetail,
        out Button loadMoreTimelineButton,
        out ListView riskList,
        out TextBlock riskStatus,
        out ListView assetList,
        out TextBlock assetStatus,
        out TextBlock assetDetail,
        out ComboBox reportFormat,
        out ComboBox reportObjectDetails,
        out CheckBox reportIncludeProcessKey,
        out CheckBox reportIncludePayload,
        out CheckBox reportSensitiveAcknowledgement,
        out TextBlock reportStatus,
        Func<Task> refreshHealthAsync,
        Func<Task> saveDirectoriesAsync,
        Func<Task> performProtectionActionAsync,
        Func<bool, Task> loadHistoryAsync,
        Func<bool, Task> loadTimelineAsync,
        Func<Task> loadRiskAsync,
        Func<Task> loadAssetDifferencesAsync,
        Func<Task> exportReportAsync)
    {
        serviceStatus = new TextBlock
        {
            Text = "正在连接后台服务...",
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
        };
        var refreshButton = new Button
        {
            Content = "刷新服务状态",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        refreshButton.Click += async (_, _) => await refreshHealthAsync();
        directoryInputs = new StackPanel
        {
            Spacing = 8,
        };
        AddDirectoryInput();
        var directoryInputScrollViewer = new ScrollViewer
        {
            Content = directoryInputs,
            MinHeight = 80,
            MaxHeight = 240,
            VerticalScrollMode = ScrollMode.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        directoryStatus = new TextBlock
        {
            Text = "正在读取重点目录...",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        var saveDirectoriesButton = new Button
        {
            Content = "保存目录",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        saveDirectoriesButton.Click += async (_, _) => await saveDirectoriesAsync();
        var addDirectoryButton = new Button
        {
            Content = "新增目录",
        };
        addDirectoryButton.Click += (_, _) => AddDirectoryInput();
        var reloadDirectoriesButton = new Button
        {
            Content = "重新读取目录",
        };
        reloadDirectoriesButton.Click += async (_, _) => await LoadDirectoriesAsync();
        var directoryActions = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Children =
            {
                addDirectoryButton,
                reloadDirectoriesButton,
                saveDirectoriesButton,
            },
        };
        exclusionInputs = new StackPanel
        {
            Spacing = 8,
        };
        AddExclusionInput();
        var exclusionInputScrollViewer = new ScrollViewer
        {
            Content = exclusionInputs,
            MinHeight = 80,
            MaxHeight = 200,
            VerticalScrollMode = ScrollMode.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
        };
        exclusionStatus = new TextBlock
        {
            Text = "正在读取排除规则...",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        var addExclusionButton = new Button
        {
            Content = "新增规则",
        };
        addExclusionButton.Click += (_, _) => AddExclusionInput();
        var saveExclusionsButton = new Button
        {
            Content = "保存规则",
        };
        saveExclusionsButton.Click += async (_, _) => await SaveExclusionsAsync();
        var exclusionActions = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Children =
            {
                addExclusionButton,
                saveExclusionsButton,
            },
        };
        inputActivityEnabled = new ToggleSwitch
        {
            Header = "输入活动统计",
            OnContent = "已开启",
            OffContent = "已关闭",
            IsOn = true,
        };
        inputActivityStatus = new TextBlock
        {
            Text = "只汇总按键次数、鼠标点击和滚轮活动，不保存按键内容。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        var saveInputActivityButton = new Button
        {
            Content = "保存输入设置",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        saveInputActivityButton.Click += async (_, _) => await SaveInputActivityPreferenceAsync();
        highRiskShortcutsEnabled = new ToggleSwitch
        {
            Header = "高风险组合键类别",
            OnContent = "已授权",
            OffContent = "未授权",
            IsOn = false,
        };
        highRiskShortcutsStatus = new TextBlock
        {
            Text = "默认关闭。开启后只记录四类组合键类别与次数，不保存完整按键文字。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        var saveHighRiskShortcutsButton = new Button
        {
            Content = "保存组合键设置",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        saveHighRiskShortcutsButton.Click += async (_, _) => await SaveHighRiskShortcutsPreferenceAsync();
        windowTitleEnabled = new ToggleSwitch
        {
            Header = "窗口标题记录",
            OnContent = "已开启",
            OffContent = "已关闭",
            IsOn = false,
        };
        windowTitleStatus = new TextBlock
        {
            Text = "窗口标题记录已关闭。开启后可能采集文件名、网页标题和其他敏感信息。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        var saveWindowTitleButton = new Button
        {
            Content = "保存标题设置",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        saveWindowTitleButton.Click += async (_, _) => await SaveWindowTitlePreferenceAsync();
        monitoringMode = new ComboBox
        {
            HorizontalAlignment = HorizontalAlignment.Left,
            SelectedIndex = 1,
        };
        monitoringMode.MaxWidth = 280;
        monitoringMode.MinWidth = 160;
        monitoringMode.HorizontalAlignment = HorizontalAlignment.Stretch;
        monitoringMode.MinHeight = 40;
        monitoringMode.Items.Add(new ComboBoxItem { Content = "宽松", Tag = "relaxed" });
        monitoringMode.Items.Add(new ComboBoxItem { Content = "标准", Tag = "standard" });
        monitoringMode.Items.Add(new ComboBoxItem { Content = "严格", Tag = "strict" });
        monitoringMode.Items.Add(new ComboBoxItem { Content = "自定义", Tag = "custom" });
        monitoringMode.SelectionChanged += async (_, _) => await LoadSessionStartPreviewAsync();
        sessionStartPreviewStatus = new TextBlock
        {
            Text = "正在生成启动预览...",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        sessionName = new TextBox
        {
            Header = "会话名称（可选）",
            PlaceholderText = "留空时自动命名",
            HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        sessionName.MaxWidth = 420;
        sessionName.MinWidth = 0;
        sessionStatus = new TextBlock
        {
            Text = "正在读取保护会话...",
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            TextWrapping = TextWrapping.Wrap,
        };
        dashboardModeSummary = new TextBlock
        {
            Text = "记录文件、进程、系统与设备变化",
            FontSize = 13,
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpDashboardMutedBrush"),
        };
        dashboardTargetSummary = new TextBlock
        {
            Text = "正在读取...",
            FontSize = 14,
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            Foreground = ThemeBrush("DgpPrimaryTextBrush"),
        };
        protectionButton = new Button
        {
            Content = "开启保护",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        protectionButton.Background = ThemeBrush("DgpBrandBrush");
        protectionButton.Foreground = ThemeBrush("DgpTitleBarTextBrush");
        protectionButton.Padding = new Thickness(16, 8, 16, 8);
        protectionButton.MinHeight = 42;
        protectionButton.MinWidth = 160;
        protectionButton.CornerRadius = new CornerRadius(8);
        protectionButton.Click += async (_, _) => await performProtectionActionAsync();
        pauseProtectionButton = new Button
        {
            Content = "暂停保护",
            HorizontalAlignment = HorizontalAlignment.Left,
            Visibility = Visibility.Collapsed,
        };
        pauseProtectionButton.Click += async (_, _) => await ToggleProtectionPauseAsync();
        temporaryInputDuration = new NumberBox
        {
            Header = "持续时间（分钟）",
            Value = 15,
            Minimum = 1,
            Maximum = 480,
            SmallChange = 5,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact,
        };
        temporaryInputDurationMode = new ComboBox
        {
            Header = "结束方式",
            HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        temporaryInputDurationMode.Items.Add(new ComboBoxItem { Content = "到时自动释放", Tag = "timed" });
        temporaryInputDurationMode.Items.Add(new ComboBoxItem { Content = "持续到验证解锁", Tag = "until_unlock" });
        temporaryInputDurationMode.SelectedIndex = 0;
        temporaryInputDurationMode.SelectionChanged += (_, _) =>
        {
            if (temporaryInputDuration is not null)
            {
                temporaryInputDuration.IsEnabled = temporaryInputDurationMode.SelectedItem is not ComboBoxItem { Tag: "until_unlock" };
            }
        };
        temporaryInputControlStatus = new TextBlock
        {
            Text = "正在读取临时输入控制状态...",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        startTemporaryInputControlButton = new Button { Content = "启动锁定", MinHeight = 42, MinWidth = 124, CornerRadius = new CornerRadius(8) };
        startTemporaryInputControlButton.Background = ThemeBrush("DgpTealBrush");
        startTemporaryInputControlButton.Foreground = ThemeBrush("DgpTitleBarTextBrush");
        startTemporaryInputControlButton.Padding = new Thickness(14, 8, 14, 8);
        startTemporaryInputControlButton.Click += async (_, _) => await StartTemporaryInputControlAsync();
        stopTemporaryInputControlButton = new Button { Content = "解除锁定", IsEnabled = false, Visibility = Visibility.Collapsed };
        stopTemporaryInputControlButton.MinHeight = 42;
        stopTemporaryInputControlButton.MinWidth = 124;
        stopTemporaryInputControlButton.CornerRadius = new CornerRadius(8);
        stopTemporaryInputControlButton.Background = ThemeBrush("DgpTealBrush");
        stopTemporaryInputControlButton.Foreground = ThemeBrush("DgpTitleBarTextBrush");
        stopTemporaryInputControlButton.Click += async (_, _) => await StopTemporaryInputControlAsync();
        inputShieldRuntimeStatus = new TextBlock
        {
            Text = "正在读取输入控制运行状态...",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpPrimaryTextBrush"),
        };
        inputShieldCredentialStatus = new TextBlock
        {
            Text = "正在读取本地凭据状态...",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        var configureTemporaryInputButton = new Button { Content = "控制规则设置" };
        configureTemporaryInputButton.Click += async (_, _) => await ShowInputShieldPolicyDetailsAsync();
        var refreshInputShieldButton = new Button { Content = "刷新状态" };
        refreshInputShieldButton.Click += async (_, _) => await LoadInputShieldManagementAsync();
        var showInputShieldDevicesButton = new Button { Content = "查看输入设备" };
        showInputShieldDevicesButton.Click += async (_, _) => await ShowInputShieldDevicesAsync();
        var configureInputShieldCredentialsButton = new Button { Content = "设置本地密码" };
        configureInputShieldCredentialsButton.Click += async (_, _) => await ConfigureInputShieldCredentialsAsync();
        var deleteInputShieldCredentialsButton = new Button { Content = "删除本地密码" };
        deleteInputShieldCredentialsButton.Click += async (_, _) => await DeleteInputShieldCredentialsAsync();
        foreach (var actionButton in new[]
                 {
                     configureTemporaryInputButton,
                     refreshInputShieldButton,
                     showInputShieldDevicesButton,
                     configureInputShieldCredentialsButton,
                     deleteInputShieldCredentialsButton,
                 })
        {
            actionButton.HorizontalAlignment = HorizontalAlignment.Stretch;
            actionButton.MinHeight = 42;
            actionButton.Padding = new Thickness(16, 9, 16, 9);
        }
        var openModeSettingsButton = new Button
        {
            Content = "查看模式配置",
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        var temporaryInputTiming = new Grid
        {
            ColumnSpacing = 12,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
            },
        };
        Grid.SetColumn(temporaryInputDurationMode, 0);
        Grid.SetColumn(temporaryInputDuration, 1);
        temporaryInputTiming.Children.Add(temporaryInputDurationMode);
        temporaryInputTiming.Children.Add(temporaryInputDuration);
        var inputManagementActions = new Grid
        {
            ColumnSpacing = 10,
            RowSpacing = 10,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
            },
            RowDefinitions =
            {
                new RowDefinition { Height = GridLength.Auto },
                new RowDefinition { Height = GridLength.Auto },
            },
        };
        Grid.SetColumnSpan(configureTemporaryInputButton, 2);
        Grid.SetRow(showInputShieldDevicesButton, 1);
        Grid.SetRow(refreshInputShieldButton, 1);
        Grid.SetColumn(refreshInputShieldButton, 1);
        inputManagementActions.Children.Add(configureTemporaryInputButton);
        inputManagementActions.Children.Add(showInputShieldDevicesButton);
        inputManagementActions.Children.Add(refreshInputShieldButton);

        var inputCredentialActions = new Grid
        {
            ColumnSpacing = 10,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
            },
        };
        Grid.SetColumn(deleteInputShieldCredentialsButton, 1);
        inputCredentialActions.Children.Add(configureInputShieldCredentialsButton);
        inputCredentialActions.Children.Add(deleteInputShieldCredentialsButton);

        inputShieldUnlockHint = new TextBlock
        {
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        var inputManagementContent = new StackPanel
        {
            Width = 400,
            Spacing = 12,
            Children =
            {
                new TextBlock
                {
                    Text = "高级设置与设备",
                    FontSize = 20,
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                    Foreground = ThemeBrush("DgpPrimaryTextBrush"),
                },
                inputShieldUnlockHint,
                inputShieldRuntimeStatus,
                inputShieldCredentialStatus,
                new TextBlock
                {
                    Text = "控制与设备",
                    Margin = new Thickness(0, 4, 0, 0),
                    FontSize = 14,
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                    Foreground = ThemeBrush("DgpPrimaryTextBrush"),
                },
                inputManagementActions,
                new TextBlock
                {
                    Text = "本地凭据",
                    Margin = new Thickness(0, 4, 0, 0),
                    FontSize = 14,
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                    Foreground = ThemeBrush("DgpPrimaryTextBrush"),
                },
                inputCredentialActions,
            },
        };
        var inputManagementFlyout = new Flyout
        {
            Content = new ScrollViewer
            {
                HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
                HorizontalScrollMode = ScrollMode.Disabled,
                VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
                VerticalScrollMode = ScrollMode.Auto,
                Content = inputManagementContent,
            },
        };
        var inputManagementButton = new Button
        {
            Content = "高级设置与设备",
            HorizontalAlignment = HorizontalAlignment.Left,
            Flyout = inputManagementFlyout,
        };
        temporaryInputUnlockHint = new TextBlock
        {
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        UpdateTemporaryInputUnlockHints();
        var dashboardHeader = CreatePageHeader("仪表盘");

        sessionStatus.FontSize = 15;
        sessionStatus.Foreground = ThemeBrush("DgpDashboardTextBrush");
        dashboardProtectionBadge = new Border
        {
            Background = ThemeBrush("DgpInformationBrush"),
            CornerRadius = new CornerRadius(8),
            Padding = new Thickness(12, 10, 12, 10),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            Child = sessionStatus,
        };
        var manageTargetsButton = new Button
        {
            Content = dashboardTargetSummary,
            Background = ThemeBrush("DgpInformationBrush"),
            BorderThickness = new Thickness(0),
            CornerRadius = new CornerRadius(8),
            Padding = new Thickness(12, 8, 12, 8),
            MinHeight = 40,
            HorizontalAlignment = HorizontalAlignment.Left,
        };
        manageTargetsButton.Click += (_, _) => RevealDashboardScope();

        var startOptions = CreateDashboardExpander("启动选项与采集详情",
            new StackPanel
            {
                Spacing = 12,
                Children = { sessionName, sessionStartPreviewStatus, openModeSettingsButton },
            });
        var protectionBody = new StackPanel
        {
            Spacing = 16,
            Children =
            {
                dashboardProtectionBadge,
                CreateBalancedDashboardColumns(
                    new StackPanel
                    {
                        Spacing = 8,
                        Children =
                        {
                            new TextBlock { Text = "保护模式", Foreground = ThemeBrush("DgpDashboardMutedBrush") },
                            monitoringMode,
                        },
                    },
                    new StackPanel
                    {
                        Spacing = 8,
                        Children =
                        {
                            new TextBlock { Text = "监控目标", Foreground = ThemeBrush("DgpDashboardMutedBrush") },
                            manageTargetsButton,
                        },
                    }, 400),
                dashboardModeSummary,
                startOptions,
            },
        };
        var protectionActions = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 12,
            HorizontalAlignment = HorizontalAlignment.Right,
            Children = { pauseProtectionButton, protectionButton },
        };
        var inputBody = new StackPanel
        {
            Spacing = 16,
            Children =
            {
                new Border
                {
                    Background = ThemeBrush("DgpTealSoftBrush"),
                    CornerRadius = new CornerRadius(8),
                    Padding = new Thickness(12, 10, 12, 10),
                    Child = temporaryInputControlStatus,
                },
                new TextBlock
                {
                    Text = "锁定本地键盘和鼠标，远程注入输入保持可用。",
                    TextWrapping = TextWrapping.Wrap,
                    Foreground = ThemeBrush("DgpSecondaryTextBrush"),
                },
                temporaryInputTiming,
                temporaryInputUnlockHint,
            },
        };
        var inputActions = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Children = { startTemporaryInputControlButton, stopTemporaryInputControlButton },
        };
        inputManagementButton.Background = null;
        inputManagementButton.BorderThickness = new Thickness(0);
        inputManagementButton.Padding = new Thickness(0, 4, 0, 4);
        inputManagementButton.Foreground = ThemeBrush("DgpTealBrush");
        var inputFooter = new Grid
        {
            ColumnSpacing = 12,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = GridLength.Auto },
            },
        };
        inputManagementButton.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(inputActions, 1);
        inputFooter.Children.Add(inputManagementButton);
        inputFooter.Children.Add(inputActions);
        var primaryTasks = CreateBalancedDashboardColumns(
            CreateDashboardTask("电脑保护", "记录重要变化，结束后可回看和导出", "\uE83D",
                protectionBody, protectionActions, false),
            CreateDashboardTask("临时锁定键鼠", "暂时停用输入，可与电脑保护同时使用", "\uE72E",
                inputBody, inputFooter, true), 820, 1.25);

        dashboardScopeExpander = CreateDashboardExpander("监控范围与排除规则",
            new StackPanel
            {
                Spacing = 20,
                Children =
                {
                    new TextBlock { Text = "监控目标", FontSize = 17, FontWeight = Microsoft.UI.Text.FontWeights.SemiBold },
                    new TextBlock { Text = "开始保护后范围固定；修改目标或排除规则前请先结束保护。", TextWrapping = TextWrapping.Wrap },
                    directoryInputScrollViewer,
                    directoryActions,
                    directoryStatus,
                    new Border { Height = 1, Background = ThemeBrush("DgpDashboardLineBrush") },
                    new TextBlock { Text = "排除规则", FontSize = 17, FontWeight = Microsoft.UI.Text.FontWeights.SemiBold },
                    exclusionInputScrollViewer,
                    exclusionActions,
                    exclusionStatus,
                },
            });
        dashboardScopeExpander.Background = ThemeBrush("DgpSurfaceBrush");
        var historyShortcut = CreateDashboardShortcut("历史记录", "回看已完成的保护", Symbol.Calendar);
        var auditShortcut = CreateDashboardShortcut("审计结果", "查看当前会话事件", Symbol.Document);
        var reportShortcut = CreateDashboardShortcut("导出报告", "保存当前会话结果", Symbol.Save);
        var serviceDetails = CreateDashboardExpander("服务详情",
            new StackPanel { Spacing = 8, Children = { serviceStatus, refreshButton } });
        serviceDetails.Background = null;
        serviceDetails.BorderThickness = new Thickness(0);
        var overview = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                dashboardHeader,
                primaryTasks,
                dashboardScopeExpander,
                new TextBlock
                {
                    Text = "记录与报告",
                    FontSize = 17,
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                    Margin = new Thickness(0, 4, 0, 0),
                    Foreground = ThemeBrush("DgpDashboardTextBrush"),
                },
                CreateBalancedDashboardTriplet(historyShortcut, auditShortcut, reportShortcut, 720),
                serviceDetails,
            },
        };
        historyList = new ListView
        {
            SelectionMode = ListViewSelectionMode.None,
            MinHeight = 360,
        };
        historyStatus = new TextBlock
        {
            Text = "打开此页面后读取历史会话。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        var refreshHistoryButton = CreateWorkspaceButton("刷新列表");
        refreshHistoryButton.Click += async (_, _) => await loadHistoryAsync(false);
        loadMoreHistoryButton = new Button
        {
            Content = "加载更早会话",
            HorizontalAlignment = HorizontalAlignment.Left,
            IsEnabled = false,
        };
        loadMoreHistoryButton.Click += async (_, _) => await loadHistoryAsync(true);
        var history = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                CreatePageHeader("历史会话"),
                CreateWorkspaceCard("会话记录", Symbol.Calendar,
                    new StackPanel
                    {
                        Spacing = 16,
                        Children =
                        {
                            CreateWorkspaceToolbar(historyStatus, refreshHistoryButton),
                            historyList,
                            loadMoreHistoryButton,
                        },
                    }),
            },
        };
        timelineList = new ListView
        {
            SelectionMode = ListViewSelectionMode.None,
            MinHeight = 360,
        };
        timelineStatus = new TextBlock
        {
            Text = "打开此页面后读取当前保护会话的事件。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        timelineDetail = new TextBlock
        {
            Text = "选择一条事件以查看详情。",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        timelineCategoryFilter = new ComboBox { Header = "类别", SelectedIndex = 0, HorizontalAlignment = HorizontalAlignment.Stretch };
        foreach (var option in new[]
        {
            ("全部类别", ""), ("文件", "file"), ("进程", "process"), ("软件", "software"),
            ("系统", "system"), ("设备", "device"), ("采集健康", "health"),
        })
        {
            timelineCategoryFilter.Items.Add(new ComboBoxItem { Content = option.Item1, Tag = option.Item2 });
        }
        timelineSeverityFilter = new ComboBox { Header = "风险级别", SelectedIndex = 0, HorizontalAlignment = HorizontalAlignment.Stretch };
        foreach (var option in new[] { ("全部级别", ""), ("高", "high"), ("中", "medium"), ("低", "low") })
        {
            timelineSeverityFilter.Items.Add(new ComboBoxItem { Content = option.Item1, Tag = option.Item2 });
        }
		timelineUserFilter = new TextBox { Header = "用户", PlaceholderText = "用户名、域账号、SID 或 64 位 SID 哈希" };
        timelineProcessFilter = new TextBox { Header = "进程", PlaceholderText = "输入进程标识或路径片段" };
        timelinePathFilter = new TextBox { Header = "路径", PlaceholderText = "输入对象路径片段" };
        timelineFromFilter = new TextBox { Header = "开始时间", PlaceholderText = "例如 2026-09-13 08:00 +08:00" };
        timelineToFilter = new TextBox { Header = "结束时间", PlaceholderText = "例如 2026-09-13 18:00 +08:00" };
        var timelineDetailView = timelineDetail;
        timelineList.SelectionMode = ListViewSelectionMode.Single;
        timelineList.SelectionChanged += (_, args) =>
        {
            if (args.AddedItems.FirstOrDefault() is ListViewItem { Tag: ValueTuple<AuditEventInfo, string?, string?, bool> selected })
            {
                var eventInfo = selected.Item1;
                var hashStatus = selected.Item2;
				var rawPayload = selected.Item3;
				var payloadTruncated = selected.Item4;
                timelineDetailView.Text =
                    $"事件 #{eventInfo.Sequence}\n" +
                    $"观测时间：{eventInfo.ObservedUtc.LocalDateTime:yyyy-MM-dd HH:mm:ss}\n" +
                    $"类别：{FormatTimelineCategory(eventInfo.Category)}\n" +
                    $"动作：{FormatTimelineAction(eventInfo.Action)}\n" +
                    $"对象：{eventInfo.ObjectKey ?? "未提供"}\n" +
                    $"来源：{eventInfo.Source}\n" +
                    $"风险级别：{eventInfo.Severity}\n" +
                    $"归因可信度：{eventInfo.Confidence}\n" +
                    $"进程关联：{eventInfo.ProcessKey ?? "未归因"}" +
                    (hashStatus is null ? "" : $"\n哈希状态：{FormatHashStatus(hashStatus)}") +
					(string.IsNullOrWhiteSpace(rawPayload) ? "\n原始负载：未提供" :
						$"\n原始负载{(payloadTruncated ? "（已截断）" : "")}：\n{rawPayload}");
            }
        };
        var refreshTimelineButton = CreateWorkspaceButton("刷新时间线");
        refreshTimelineButton.Click += async (_, _) => await loadTimelineAsync(false);
        var exportEventButton = CreateWorkspaceButton("导出所选事件");
        exportEventButton.Click += async (_, _) => await ExportSelectedEventAsync();
        var resetTimelineFilters = CreateWorkspaceButton("重置筛选");
        var applyTimelineFilters = CreateWorkspaceButton("应用筛选", true);
        applyTimelineFilters.Click += async (_, _) => await loadTimelineAsync(false);
        resetTimelineFilters.Click += async (_, _) =>
        {
            timelineCategoryFilter.SelectedIndex = 0;
            timelineSeverityFilter.SelectedIndex = 0;
            timelineUserFilter.Text = timelineProcessFilter.Text = timelinePathFilter.Text = "";
            timelineFromFilter.Text = timelineToFilter.Text = "";
            pendingEvidenceId = null;
            await loadTimelineAsync(false);
        };
        loadMoreTimelineButton = new Button
        {
            Content = "加载更多事件",
            HorizontalAlignment = HorizontalAlignment.Left,
            IsEnabled = false,
        };
        loadMoreTimelineButton.Click += async (_, _) => await loadTimelineAsync(true);
        var audit = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                CreatePageHeader("审计结果"),
                CreateAnalysisSessionBar("audit"),
                CreateDashboardExpander("筛选事件",
                    new StackPanel
                    {
                        Spacing = 16,
                        Children =
                        {
                            CreateWorkspaceForm(timelineCategoryFilter, timelineSeverityFilter, timelineUserFilter,
                                timelineProcessFilter, timelinePathFilter, timelineFromFilter, timelineToFilter),
                            CreateWorkspaceToolbar(resetTimelineFilters, applyTimelineFilters),
                        },
                    }),
                CreateBalancedDashboardColumns(
                    CreateWorkspaceCard("事件时间线", Symbol.Document,
                        new StackPanel
                        {
                            Spacing = 16,
                            Children =
                            {
                                CreateWorkspaceToolbar(timelineStatus, refreshTimelineButton),
                                timelineList, loadMoreTimelineButton,
                            },
                        }),
                    CreateWorkspaceCard("事件详情", Symbol.View,
                        new StackPanel
                        {
                            Spacing = 16,
                            Children = { CreateWorkspaceDetail(timelineDetail), exportEventButton },
                        }), 880, 1.6),
            },
        };
        riskList = new ListView
        {
            SelectionMode = ListViewSelectionMode.None,
            MinHeight = 360,
        };
        riskStatus = new TextBlock
        {
            Text = "打开此页面后评估当前保护会话。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        var refreshRiskButton = CreateWorkspaceButton("重新评估", true);
        refreshRiskButton.Click += async (_, _) => await loadRiskAsync();
        var risk = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                CreatePageHeader("风险分析"),
                CreateAnalysisSessionBar("risk"),
                CreateWorkspaceCard("风险发现", Symbol.ReportHacked,
                    new StackPanel
                    {
                        Spacing = 16,
                        Children =
                        {
                            CreateWorkspaceToolbar(riskStatus, refreshRiskButton),
                            riskList,
                        },
                    }),
            },
        };
        assetList = new ListView
        {
            SelectionMode = ListViewSelectionMode.Single,
            MinHeight = 300,
        };
        assetStatus = new TextBlock
        {
            Text = "打开此页面后读取当前保护会话的资产变化。",
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        };
        assetDetail = new TextBlock
        {
            Text = "选择一项变化以查看资产标识、属性和变更字段。",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        var assetDetailView = assetDetail;
        assetList.SelectionChanged += (_, args) =>
        {
            if (args.AddedItems.FirstOrDefault() is not ListViewItem { Tag: AssetDifference difference })
            {
                return;
            }
            var asset = AssetFor(difference);
            if (asset is null)
            {
                return;
            }
            var attributes = asset.Attributes is { Count: > 0 }
                ? string.Join("；", asset.Attributes.Select(attribute => $"{attribute.Key}：{attribute.Value}"))
                : "未提供";
            assetDetailView.Text =
                $"变化类型：{FormatAssetDifferenceKind(difference.Kind)}\n" +
                $"设备状态：{(string.IsNullOrEmpty(FormatDevicePresence(difference.PresenceStatus)) ? "不适用" : FormatDevicePresence(difference.PresenceStatus))}\n" +
                $"资产类别：{FormatAssetCategory(asset.Category)}\n" +
                $"显示名称：{asset.DisplayName}\n" +
                $"标识：{asset.Identifier}\n" +
                $"变更字段：{(difference.ChangedAttributes.Count == 0 ? "无" : string.Join("、", difference.ChangedAttributes))}\n" +
                $"当前属性：{attributes}";
        };
        CheckBox CreateAssetCategoryFilter(string label, string category)
        {
            var filter = new CheckBox { Content = label, Tag = category };
            filter.Checked += async (_, _) =>
            {
                assetCategories.Add(category);
                await loadAssetDifferencesAsync();
            };
            filter.Unchecked += async (_, _) =>
            {
                assetCategories.Remove(category);
                await loadAssetDifferencesAsync();
            };
            return filter;
        }
        var assetFilters = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 16,
            Children =
            {
                CreateAssetCategoryFilter("软件", "software"),
                CreateAssetCategoryFilter("设备", "device"),
                CreateAssetCategoryFilter("网络", "network"),
                CreateAssetCategoryFilter("账户", "account"),
                CreateAssetCategoryFilter("系统", "system"),
            },
        };
        var refreshAssetsButton = CreateWorkspaceButton("刷新资产变化");
        refreshAssetsButton.Click += async (_, _) => await loadAssetDifferencesAsync();
        var assets = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                CreatePageHeader("资产差异"),
                CreateAnalysisSessionBar("assets"),
                CreateBalancedDashboardColumns(
                    CreateWorkspaceCard("资产变化", Symbol.Library,
                        new StackPanel
                        {
                            Spacing = 16,
                            Children =
                            {
                                CreateWorkspaceToolbar(assetStatus, refreshAssetsButton),
                                assetFilters, assetList,
                            },
                        }),
                    CreateWorkspaceCard("资产详情", Symbol.View, CreateWorkspaceDetail(assetDetail)), 880, 1.6),
            },
        };
        assets.SizeChanged += (_, args) =>
        {
            var compact = args.NewSize.Width < CompactLayoutWidth;
            assetFilters.Orientation = compact ? Orientation.Vertical : Orientation.Horizontal;
            assetFilters.Spacing = compact ? 5 : 13;
        };
        reportFormat = new ComboBox
        {
            Header = "报告格式",
            SelectedIndex = 0,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            MaxWidth = 320,
        };
        reportFormat.Items.Add(new ComboBoxItem { Content = "HTML 报告", Tag = "html" });
        reportFormat.Items.Add(new ComboBoxItem { Content = "Markdown 报告", Tag = "markdown" });
        reportFormat.Items.Add(new ComboBoxItem { Content = "JSON 报告", Tag = "json" });
        reportFormat.SelectedItem = reportFormat.Items.OfType<ComboBoxItem>()
            .First(item => string.Equals(item.Tag as string, uiPreferences.DefaultReportFormat, StringComparison.Ordinal));
        reportObjectDetails = new ComboBox
        {
            Header = "对象细节",
            SelectedIndex = 1,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            MaxWidth = 320,
        };
        reportObjectDetails.Items.Add(new ComboBoxItem { Content = "隐藏对象信息", Tag = "omit" });
        reportObjectDetails.Items.Add(new ComboBoxItem { Content = "仅保留文件名", Tag = "basename" });
        reportObjectDetails.Items.Add(new ComboBoxItem { Content = "包含完整对象路径", Tag = "full" });
        reportIncludeProcessKey = new CheckBox { Content = "包含进程关联键" };
        reportIncludePayload = new CheckBox { Content = "包含原始事件负载" };
        reportIncludeUsernames = new CheckBox { Content = "原始负载中保留用户名" };
        reportIncludeWindowTitles = new CheckBox { Content = "原始负载中保留窗口标题" };
        reportSensitiveAcknowledgement = new CheckBox { Content = "我已确认本次导出包含敏感信息" };
        reportFromSequence = new TextBox { Header = "起始事件序号", PlaceholderText = "留空导出整个会话，例如 1" };
        reportToSequence = new TextBox { Header = "结束事件序号", PlaceholderText = "例如 100000" };
        reportStatus = new TextBlock
        {
            Text = "默认导出保留完成审计所需的必要细节。",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        var exportReportButton = CreateWorkspaceButton("生成并保存报告", true);
        exportReportButton.Click += async (_, _) => await exportReportAsync();
        var reports = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                CreatePageHeader("报告导出"),
                CreateAnalysisSessionBar("reports"),
                CreateBalancedDashboardColumns(
                    CreateWorkspaceCard("报告内容", Symbol.Document,
                        new StackPanel
                        {
                            Spacing = 16,
                            Children =
                            {
                                reportFormat,
                                CreateWorkspaceForm(reportFromSequence, reportToSequence),
                                CreateWorkspaceNote("序号留空时导出整个会话，每份最多 100,000 条事件。资产差异始终比较整个会话。"),
                            },
                        }),
                    CreateWorkspaceCard("隐私与细节", Symbol.Permissions,
                        new StackPanel
                        {
                            Spacing = 16,
                            Children =
                            {
                                reportObjectDetails,
                                CreateDashboardExpander("更多导出字段",
                                    new StackPanel
                                    {
                                        Spacing = 10,
                                        Children = { reportIncludeProcessKey, reportIncludePayload,
                                            reportIncludeUsernames, reportIncludeWindowTitles },
                                    }),
                                reportSensitiveAcknowledgement,
                            },
                        }), 800),
                CreateWorkspaceNotice(CreateWorkspaceToolbar(reportStatus, exportReportButton), true),
            },
        };
        fileActivityPolicyEnabled = new ToggleSwitch { Header = "模块总开关", OnContent = "开启", OffContent = "关闭", IsOn = true };
        processAndSoftwarePolicyEnabled = new ToggleSwitch { Header = "模块总开关", OnContent = "开启", OffContent = "关闭", IsOn = true };
        systemAndNetworkPolicyEnabled = new ToggleSwitch { Header = "模块总开关", OnContent = "开启", OffContent = "关闭", IsOn = true };
        externalDevicesPolicyEnabled = new ToggleSwitch { Header = "模块总开关", OnContent = "开启", OffContent = "关闭", IsOn = true };
        userSessionActivityPolicyEnabled = new ToggleSwitch { Header = "模块总开关", OnContent = "开启", OffContent = "关闭", IsOn = false };
        strictReadAuditPolicyEnabled = new ToggleSwitch { Header = "严格读取审计", OnContent = "开启", OffContent = "关闭", IsOn = false };
        fileActivityPolicyEnabled.Toggled += (_, _) =>
        {
            if (strictReadAuditPolicyEnabled is not null && !fileActivityPolicyEnabled.IsOn)
            {
                strictReadAuditPolicyEnabled.IsOn = false;
                strictReadAuditPolicyEnabled.IsEnabled = false;
            }
            else if (strictReadAuditPolicyEnabled is not null)
            {
                strictReadAuditPolicyEnabled.IsEnabled = true;
            }
            UpdateMonitoringPolicySummary();
        };
        foreach (var policyToggle in new[]
        {
            processAndSoftwarePolicyEnabled,
            systemAndNetworkPolicyEnabled,
            externalDevicesPolicyEnabled,
            userSessionActivityPolicyEnabled,
            strictReadAuditPolicyEnabled,
        })
        {
            policyToggle.Toggled += (_, _) => UpdateMonitoringPolicySummary();
        }
        TextBlock CreatePolicyDetailSummary() => new()
        {
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpPrimaryTextBrush"),
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
        };
        filePolicyDetailSummary = CreatePolicyDetailSummary();
        processPolicyDetailSummary = CreatePolicyDetailSummary();
        systemPolicyDetailSummary = CreatePolicyDetailSummary();
        devicePolicyDetailSummary = CreatePolicyDetailSummary();
        userSessionPolicyDetailSummary = CreatePolicyDetailSummary();
        monitoringPolicySummary = new TextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        UpdateMonitoringPolicySummary();
        monitoringPolicyStatus = new TextBlock
        {
            Text = "正在读取监控与审计策略...",
            TextWrapping = TextWrapping.Wrap,
            Foreground = ThemeBrush("DgpSecondaryTextBrush"),
        };
        var saveMonitoringPolicyButton = CreateWorkspaceButton("保存当前模式", true);
        saveMonitoringPolicyButton.Click += async (_, _) => await SaveMonitoringPolicyAsync();
        var resetMonitoringPolicyButton = CreateWorkspaceButton("恢复默认值");
        resetMonitoringPolicyButton.Click += async (_, _) => await ResetMonitoringProfileAsync();
        settingsMonitoringMode = new ComboBox
        {
            Header = "正在配置的模式",
            HorizontalAlignment = HorizontalAlignment.Left,
            Width = 240,
            SelectedIndex = 3,
        };
        settingsMonitoringMode.Items.Add(new ComboBoxItem { Content = "宽松", Tag = "relaxed" });
        settingsMonitoringMode.Items.Add(new ComboBoxItem { Content = "标准", Tag = "standard" });
        settingsMonitoringMode.Items.Add(new ComboBoxItem { Content = "严格", Tag = "strict" });
        settingsMonitoringMode.Items.Add(new ComboBoxItem { Content = "自定义", Tag = "custom" });
        settingsMonitoringMode.SelectionChanged += (_, _) =>
        {
            if (settingsMonitoringMode.SelectedItem is ComboBoxItem item && item.Tag is string mode)
            {
                SelectMonitoringProfile(mode);
            }
        };

        Border CreatePolicyCapability(
            string title,
            Symbol icon,
            ToggleSwitch toggle,
            TextBlock detailSummary,
            string scope,
            Func<Task> showDetails)
        {
            var detailButton = CreateWorkspaceButton("配置详情");
            detailButton.Click += async (_, _) => await showDetails();
            var controls = CreateWorkspaceToolbar(toggle, detailButton);
            return CreateWorkspaceCard(title, icon,
                new StackPanel
                {
                    Spacing = 12,
                    Children = { detailSummary, controls },
                }, scope);
        }

        var capabilityCards = new StackPanel
        {
            Spacing = 16,
            Children =
            {
                CreateBalancedDashboardColumns(
                CreatePolicyCapability(
                    "文件审计", Symbol.Document,
                    fileActivityPolicyEnabled,
                    filePolicyDetailSummary,
                    "文件变化、内容哈希与读取审计",
                    ShowFilePolicyDetailsAsync),
                CreatePolicyCapability(
                    "进程与软件", Symbol.AllApps,
                    processAndSoftwarePolicyEnabled,
                    processPolicyDetailSummary,
                    "进程活动、软件清单与变化规则",
                    ShowProcessPolicyDetailsAsync), 800),
                CreateBalancedDashboardColumns(
                CreatePolicyCapability(
                    "系统与网络", Symbol.Setting,
                    systemAndNetworkPolicyEnabled,
                    systemPolicyDetailSummary,
                    "系统配置、网络状态与安全日志",
                    ShowSystemPolicyDetailsAsync),
                CreatePolicyCapability(
                    "外接设备", Symbol.Link,
                    externalDevicesPolicyEnabled,
                    devicePolicyDetailSummary,
                    "设备接入、移除与扫描频率",
                    ShowDevicePolicyDetailsAsync), 800),
                CreatePolicyCapability(
                    "用户会话活动", Symbol.Contact,
                    userSessionActivityPolicyEnabled,
                    userSessionPolicyDetailSummary,
                    "前台应用、输入计数与活动汇总",
                    ShowUserSessionPolicyDetailsAsync),
            },
        };
        UpdateMonitoringPolicySummary();

        var monitoringPolicy = new StackPanel
        {
            Spacing = 20,
            Margin = new Thickness(24),
            Children =
            {
                CreatePageHeader("系统设置"),
                CreateWorkspaceCard("模式配置", Symbol.Setting,
                    new StackPanel
                    {
                        Spacing = 16,
                        Children =
                        {
                            CreateWorkspaceToolbar(settingsMonitoringMode,
                                new StackPanel
                                {
                                    Orientation = Orientation.Horizontal, Spacing = 8,
                                    Children = { resetMonitoringPolicyButton, saveMonitoringPolicyButton },
                                }),
                            CreateWorkspaceNote("修改用于该模式的新会话；切换模式保留草稿，关闭软件前请保存。"),
                            CreateWorkspaceNotice(monitoringPolicyStatus),
                        },
                    }),
                capabilityCards,
                CreateDashboardExpander("模式摘要与运行说明",
                    new StackPanel
                    {
                        Spacing = 12,
                        Children =
                        {
                            monitoringPolicySummary,
                            CreateWorkspaceNote("各模块可组合运行。配置详情中可调整事件类型、对象、采样间隔与风险规则。"),
                            CreateWorkspaceNote("临时键鼠控制在仪表盘独立运行，期间暂停输入计数；前台应用与窗口标题可继续采集。"),
                        },
                    }),
            },
        };
        foreach (var page in new[] { history, audit, risk, assets, reports, monitoringPolicy })
            ApplyWorkspaceTypography(page);
        foreach (var list in new[] { historyList, timelineList, riskList, assetList })
            StyleWorkspaceList(list);
        var overviewPage = CreatePageSurface(overview);
        var historyPage = CreatePageSurface(history);
        var auditPage = CreatePageSurface(audit);
        var riskPage = CreatePageSurface(risk);
        var assetsPage = CreatePageSurface(assets);
        var reportsPage = CreatePageSurface(reports);
        var monitoringPolicyPage = CreatePageSurface(monitoringPolicy);
        var navigation = new NavigationView
        {
            PaneTitle = string.Empty,
            PaneDisplayMode = NavigationViewPaneDisplayMode.Auto,
            ExpandedModeThresholdWidth = 1000,
            CompactModeThresholdWidth = 640,
            OpenPaneLength = 164,
            CompactPaneLength = 56,
            IsPaneOpen = true,
            IsBackButtonVisible = NavigationViewBackButtonVisible.Collapsed,
            IsSettingsVisible = false,
            AlwaysShowHeader = false,
            Background = ThemeBrush("DgpCanvasBrush"),
            Foreground = ThemeBrush("DgpPrimaryTextBrush"),
            FontFamily = ThemeFont(),
            FontSize = 15,
            Content = overviewPage,
        };
        shellNavigation = navigation;
        navigation.CornerRadius = new CornerRadius(0);
        navigation.Resources["NavigationViewDefaultPaneBackground"] = ThemeBrush("DgpSidebarBrush");
        navigation.Resources["NavigationViewExpandedPaneBackground"] = ThemeBrush("DgpSidebarBrush");
        navigation.Resources["NavigationViewContentBackground"] = ThemeBrush("DgpCanvasBrush");
        navigation.Resources["NavigationViewContentMargin"] = new Thickness(0);
        navigation.Resources["NavigationViewPaneContentGridMargin"] = new Thickness(0);
        navigation.Resources["NavigationViewBorderThickness"] = new Thickness(0);
        navigation.Resources["NavigationViewContentGridBorderThickness"] = new Thickness(0);
        navigation.Resources["OverlayCornerRadius"] = new CornerRadius(0);
        navigation.Resources["NavigationViewItemForeground"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewItemForegroundPointerOver"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewItemForegroundPressed"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewItemForegroundSelected"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewItemForegroundSelectedPointerOver"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewItemForegroundSelectedPressed"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewItemBackgroundPointerOver"] = ThemeBrush("DgpNavigationHoverBrush");
        navigation.Resources["NavigationViewItemBackgroundPressed"] = ThemeBrush("DgpBrandBrush");
        navigation.Resources["NavigationViewItemBackgroundSelected"] = ThemeBrush("DgpNavigationSelectedBrush");
        navigation.Resources["NavigationViewItemBackgroundSelectedPointerOver"] = ThemeBrush("DgpNavigationHoverBrush");
        navigation.Resources["NavigationViewItemBackgroundSelectedPressed"] = ThemeBrush("DgpBrandBrush");
        navigation.Resources["NavigationViewSelectionIndicatorForeground"] = ThemeBrush("DgpNavigationSelectedBrush");
        navigation.Resources["NavigationViewButtonForegroundPointerOver"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Resources["NavigationViewButtonForegroundPressed"] = ThemeBrush("DgpTitleBarTextBrush");
        navigation.Loaded += (_, _) =>
        {
            if (FindDescendantByName<SplitView>(navigation, "RootSplitView") is { } splitView)
            {
                splitView.CornerRadius = new CornerRadius(0);
            }
        };
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "仪表盘",
            Icon = new SymbolIcon(Symbol.Home),
            IsSelected = true,
            Tag = "overview",
        });
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "历史会话",
            Icon = new SymbolIcon(Symbol.Calendar),
            Tag = "history",
        });
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "审计结果",
            Icon = new SymbolIcon(Symbol.Document),
            Tag = "audit",
        });
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "风险分析",
            Icon = new SymbolIcon(Symbol.ReportHacked),
            Tag = "risk",
        });
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "资产差异",
            Icon = new SymbolIcon(Symbol.Library),
            Tag = "assets",
        });
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "报告导出",
            Icon = new SymbolIcon(Symbol.Save),
            Tag = "reports",
        });
        navigation.MenuItems.Add(new NavigationViewItem
        {
            Content = "系统设置",
            Icon = new SymbolIcon(Symbol.Setting),
            Tag = "monitoring-policy",
        });
        foreach (var (shortcut, tag) in new[]
                 { (historyShortcut, "history"), (auditShortcut, "audit"), (reportShortcut, "reports") })
        {
            shortcut.Click += async (_, _) =>
            {
                if (tag is "audit" or "reports")
                {
                    try
                    {
                        var session = (await new ControlPipeClient().GetHealthAsync(CancellationToken.None)).Session;
                        if (session is not null) SelectAnalysisSession(AnalysisSessionFrom(session));
                    }
                    catch { }
                }
                await NavigateAnalysisAsync(tag);
            };
        }
        openModeSettingsButton.Click += (_, _) =>
        {
            navigation.SelectedItem = navigation.MenuItems
                .OfType<NavigationViewItem>()
                .First(item => string.Equals(item.Tag as string, "monitoring-policy", StringComparison.Ordinal));
        };
        navigation.SelectionChanged += async (_, args) =>
        {
            if (args.SelectedItem is not NavigationViewItem item)
            {
                return;
            }
            switch (item.Tag)
            {
                case "history":
                    navigation.Content = historyPage;
                    await loadHistoryAsync(false);
                    break;
                case "audit":
                    navigation.Content = auditPage;
                    await loadTimelineAsync(false);
                    break;
                case "risk":
                    navigation.Content = riskPage;
                    await loadRiskAsync();
                    break;
                case "assets":
                    navigation.Content = assetsPage;
                    await loadAssetDifferencesAsync();
                    break;
                case "reports":
                    navigation.Content = reportsPage;
                    await ReloadAnalysisPageAsync();
                    break;
                case "monitoring-policy":
                    navigation.Content = monitoringPolicyPage;
                    await LoadMonitoringPolicyAsync();
                    await LoadInputShieldManagementAsync();
                    break;
                default:
                    navigation.Content = overviewPage;
                    break;
            }
        };
        var menuBar = CreateMenuBar(
            navigation,
            directoryInputs,
            refreshHealthAsync,
            loadHistoryAsync,
            loadTimelineAsync,
            loadRiskAsync,
            loadAssetDifferencesAsync);
        var titleBar = CreateTitleBar(menuBar, navigation);
        var root = new Grid
        {
            RequestedTheme = ElementTheme.Light,
            Background = ThemeBrush("DgpCanvasBrush"),
            RowDefinitions =
            {
                new RowDefinition { Height = new GridLength(80) },
                new RowDefinition { Height = new GridLength(1, GridUnitType.Star) },
            },
        };
        Grid.SetRow(titleBar, 0);
        Grid.SetRow(navigation, 1);
        root.Children.Add(titleBar);
        root.Children.Add(navigation);
        ApplyCompactDensity(root);
        ApplyCompactDensity(historyPage);
        ApplyCompactDensity(auditPage);
        ApplyCompactDensity(riskPage);
        ApplyCompactDensity(assetsPage);
        ApplyCompactDensity(reportsPage);
        ApplyCompactDensity(monitoringPolicyPage);
        return root;
    }

    private Border CreatePageSurface(FrameworkElement content)
    {
        var scrollViewer = new ScrollViewer
        {
            Content = content,
            VerticalScrollMode = ScrollMode.Auto,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            ZoomMode = ZoomMode.Disabled,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            VerticalContentAlignment = VerticalAlignment.Stretch,
        };
        var surface = new Border
        {
            Margin = new Thickness(0),
            Background = ThemeBrush("DgpCanvasBrush"),
            BorderBrush = ThemeBrush("DgpBorderBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(0),
            Child = scrollViewer,
        };
        surface.SizeChanged += (_, args) =>
        {
            var padding = args.NewSize.Width < CompactLayoutWidth
                ? 6
                : args.NewSize.Width < ExpandedLayoutWidth ? 10 : 13;
            content.Margin = new Thickness(padding);
        };
        return surface;
    }

    private Border CreatePageHeader(string title)
    {
        return new Border
        {
            Padding = new Thickness(0, 4, 0, 8),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            Child = new TextBlock
            {
                Text = title,
                FontSize = 24,
                FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                Foreground = ThemeBrush("DgpPrimaryTextBrush"),
            },
        };
    }

    private Button CreateWorkspaceButton(string label, bool primary = false)
    {
        return new Button
        {
            Content = label,
            FontSize = 13,
            MinHeight = 40,
            Padding = new Thickness(16, 8, 16, 8),
            CornerRadius = new CornerRadius(8),
            Background = ThemeBrush(primary ? "DgpBrandBrush" : "DgpInformationBrush"),
            Foreground = ThemeBrush(primary ? "DgpTitleBarTextBrush" : "DgpBrandBrush"),
            BorderThickness = new Thickness(0),
            HorizontalAlignment = HorizontalAlignment.Left,
            VerticalAlignment = VerticalAlignment.Center,
        };
    }

    private TextBlock CreateWorkspaceNote(string text) => new()
    {
        Text = text, FontSize = 13,
        Foreground = ThemeBrush("DgpDashboardMutedBrush"),
        TextWrapping = TextWrapping.Wrap,
    };

    private Border CreateWorkspaceNotice(FrameworkElement content, bool success = false) => new()
    {
        Background = ThemeBrush(success ? "DgpTealSurfaceBrush" : "DgpInformationBrush"),
        BorderBrush = ThemeBrush("DgpDashboardLineBrush"),
        BorderThickness = new Thickness(1),
        CornerRadius = new CornerRadius(12),
        Padding = new Thickness(16),
        Child = content,
    };

    private Border CreateWorkspaceCard(string title, Symbol icon, FrameworkElement body, string? description = null)
    {
        var heading = new StackPanel
        {
            Orientation = Orientation.Horizontal, Spacing = 12,
            Children =
            {
                new Border
                {
                    Width = 36, Height = 36, CornerRadius = new CornerRadius(10),
                    Background = ThemeBrush("DgpInformationBrush"),
                    Child = new SymbolIcon(icon) { Foreground = ThemeBrush("DgpBrandBrush") },
                },
                new TextBlock
                {
                    Text = title, FontSize = 18,
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                    Foreground = ThemeBrush("DgpDashboardTextBrush"),
                    VerticalAlignment = VerticalAlignment.Center,
                },
            },
        };
        var content = new StackPanel { Spacing = 16 };
        content.Children.Add(heading);
        if (description is not null) content.Children.Add(CreateWorkspaceNote(description));
        content.Children.Add(body);
        return new Border
        {
            Background = ThemeBrush("DgpSurfaceBrush"),
            BorderBrush = ThemeBrush("DgpDashboardLineBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(18),
            Padding = new Thickness(20),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
            Child = content,
        };
    }

    private Grid CreateWorkspaceToolbar(FrameworkElement content, FrameworkElement actions)
    {
        var grid = new Grid { ColumnSpacing = 16, RowSpacing = 12 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        grid.RowDefinitions.Add(new RowDefinition { Height = new GridLength(0) });
        content.VerticalAlignment = VerticalAlignment.Center;
        actions.VerticalAlignment = VerticalAlignment.Center;
        grid.Children.Add(content);
        grid.Children.Add(actions);
        void Arrange(double width)
        {
            var wide = width >= 520;
            grid.ColumnDefinitions[1].Width = wide ? GridLength.Auto : new GridLength(0);
            grid.RowDefinitions[1].Height = wide ? new GridLength(0) : GridLength.Auto;
            Grid.SetColumn(actions, wide ? 1 : 0);
            Grid.SetRow(actions, wide ? 0 : 1);
            actions.HorizontalAlignment = wide ? HorizontalAlignment.Right : HorizontalAlignment.Left;
        }
        Arrange(800);
        grid.SizeChanged += (_, args) => Arrange(args.NewSize.Width);
        return grid;
    }

    private Grid CreateWorkspaceForm(params FrameworkElement[] fields)
    {
        var grid = new Grid { ColumnSpacing = 16, RowSpacing = 16 };
        foreach (var field in fields) grid.Children.Add(field);
        var previousColumns = 0;
        void Arrange(double width)
        {
            var columns = Math.Clamp((int)(width / 240), 1, Math.Min(3, fields.Length));
            if (columns == previousColumns) return;
            previousColumns = columns;
            grid.ColumnDefinitions.Clear();
            grid.RowDefinitions.Clear();
            for (var i = 0; i < columns; i++)
                grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            for (var i = 0; i < (fields.Length + columns - 1) / columns; i++)
                grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            for (var i = 0; i < fields.Length; i++)
            {
                Grid.SetColumn(fields[i], i % columns);
                Grid.SetRow(fields[i], i / columns);
            }
        }
        Arrange(720);
        grid.SizeChanged += (_, args) => Arrange(args.NewSize.Width);
        return grid;
    }

    private ScrollViewer CreateWorkspaceDetail(TextBlock text)
    {
        text.IsTextSelectionEnabled = true;
        text.FontSize = 13;
        return new ScrollViewer
        {
            Content = text, MaxHeight = 480,
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
        };
    }

    private void ApplyWorkspaceTypography(FrameworkElement element)
    {
        if (element is TextBlock text)
        {
            text.TextWrapping = TextWrapping.Wrap;
            if (text.ReadLocalValue(TextBlock.FontSizeProperty) == DependencyProperty.UnsetValue) text.FontSize = 13;
        }
        if (element is Control control && element is not ListView)
        {
            control.FontSize = 13;
            control.Foreground = control is Button ? control.Foreground : ThemeBrush("DgpDashboardTextBrush");
            if (control is TextBox or ComboBox or Button)
            {
                control.MinHeight = 40;
                control.CornerRadius = new CornerRadius(8);
                control.Padding = new Thickness(12, 8, 12, 8);
            }
        }
        switch (element)
        {
            case Panel panel:
                foreach (var child in panel.Children.OfType<FrameworkElement>()) ApplyWorkspaceTypography(child);
                break;
            case Border { Child: FrameworkElement child }: ApplyWorkspaceTypography(child); break;
            case ContentControl { Content: FrameworkElement child }: ApplyWorkspaceTypography(child); break;
        }
    }

    private void StyleWorkspaceList(ListView list)
    {
        list.MinHeight = 240;
        list.MaxHeight = 520;
        list.FontSize = 13;
        list.Foreground = ThemeBrush("DgpDashboardTextBrush");
        list.HorizontalContentAlignment = HorizontalAlignment.Stretch;
        var style = new Style(typeof(ListViewItem));
        style.Setters.Add(new Setter(Control.HorizontalContentAlignmentProperty, HorizontalAlignment.Stretch));
        style.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(12)));
        style.Setters.Add(new Setter(FrameworkElement.MarginProperty, new Thickness(0, 0, 0, 8)));
        style.Setters.Add(new Setter(Control.CornerRadiusProperty, new CornerRadius(10)));
        list.ItemContainerStyle = style;
        list.Resources["ListViewItemBackground"] = ThemeBrush("DgpCanvasBrush");
        list.Resources["ListViewItemBackgroundSelected"] = ThemeBrush("DgpInformationBrush");
        list.Resources["ListViewItemBackgroundPointerOver"] = ThemeBrush("DgpInformationBrush");
    }

    private Border CreateDashboardTask(
        string title, string purpose, string glyph,
        FrameworkElement body, FrameworkElement footer, bool inputTask)
    {
        var heading = new Grid
        {
            ColumnSpacing = 14,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
            },
        };
        heading.Children.Add(new Border
        {
            Width = 48, Height = 48,
            CornerRadius = new CornerRadius(14),
            Background = ThemeBrush(inputTask ? "DgpTealSoftBrush" : "DgpInformationBrush"),
            Child = new FontIcon
            {
                Glyph = glyph, FontSize = 24,
                Foreground = ThemeBrush(inputTask ? "DgpTealBrush" : "DgpBrandBrush"),
            },
        });
        var headingText = new StackPanel
        {
            Spacing = 5,
            VerticalAlignment = VerticalAlignment.Center,
            Children =
            {
                new TextBlock
                {
                    Text = title, FontSize = 23,
                    FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
                    Foreground = ThemeBrush("DgpDashboardTextBrush"),
                },
                new TextBlock
                {
                    Text = purpose, FontSize = 13,
                    Foreground = ThemeBrush("DgpDashboardMutedBrush"),
                    TextWrapping = TextWrapping.Wrap,
                },
            },
        };
        Grid.SetColumn(headingText, 1);
        heading.Children.Add(headingText);
        var content = new Grid
        {
            RowSpacing = 24,
            RowDefinitions =
            {
                new RowDefinition { Height = GridLength.Auto },
                new RowDefinition { Height = new GridLength(1, GridUnitType.Star) },
                new RowDefinition { Height = GridLength.Auto },
            },
        };
        body.VerticalAlignment = VerticalAlignment.Top;
        Grid.SetRow(body, 1);
        Grid.SetRow(footer, 2);
        content.Children.Add(heading);
        content.Children.Add(body);
        content.Children.Add(footer);
        return new Border
        {
            Background = ThemeBrush(inputTask ? "DgpTealSurfaceBrush" : "DgpSurfaceBrush"),
            BorderBrush = ThemeBrush("DgpDashboardLineBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(18),
            Padding = new Thickness(24),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
            Child = content,
        };
    }

    private Expander CreateDashboardExpander(string title, FrameworkElement content)
    {
        return new Expander
        {
            Header = title,
            Content = content,
            IsExpanded = false,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Background = ThemeBrush("DgpSurfaceBrush"),
            BorderBrush = ThemeBrush("DgpDashboardLineBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(10),
            Foreground = ThemeBrush("DgpDashboardTextBrush"),
        };
    }

    private void RevealDashboardScope()
    {
        if (dashboardScopeExpander is null) return;
        dashboardScopeExpander.IsExpanded = true;
        dashboardScopeExpander.DispatcherQueue.TryEnqueue(() =>
            dashboardScopeExpander.StartBringIntoView(new BringIntoViewOptions { AnimationDesired = true }));
    }

    private Button CreateDashboardShortcut(string title, string description, Symbol icon)
    {
        var content = new Grid
        {
            ColumnSpacing = 14,
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = GridLength.Auto },
            },
        };
        content.Children.Add(new SymbolIcon(icon)
        {
            Foreground = ThemeBrush("DgpMutedTextBrush"),
            VerticalAlignment = VerticalAlignment.Center,
        });
        var text = new StackPanel
        {
            Spacing = 4,
            Children =
            {
                new TextBlock { Text = title, FontSize = 16, FontWeight = Microsoft.UI.Text.FontWeights.SemiBold },
                new TextBlock
                {
                    Text = description, FontSize = 12, TextWrapping = TextWrapping.Wrap,
                    Foreground = ThemeBrush("DgpDashboardMutedBrush"),
                },
            },
        };
        Grid.SetColumn(text, 1);
        content.Children.Add(text);
        var arrow = new FontIcon { Glyph = "\uE76C", FontSize = 12, Foreground = ThemeBrush("DgpDashboardMutedBrush") };
        Grid.SetColumn(arrow, 2);
        content.Children.Add(arrow);
        var button = new Button
        {
            Content = content,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            Background = ThemeBrush("DgpSurfaceBrush"),
            BorderBrush = ThemeBrush("DgpDashboardLineBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(10),
            Padding = new Thickness(18),
        };
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(button, title);
        return button;
    }

    private Grid CreateBalancedDashboardColumns(
        FrameworkElement primary,
        FrameworkElement secondary,
        double breakpoint = 760,
        double primaryWeight = 1)
    {
        var primaryColumn = new ColumnDefinition { Width = new GridLength(primaryWeight, GridUnitType.Star) };
        var secondaryColumn = new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) };
        var secondRow = new RowDefinition { Height = new GridLength(0) };
        var grid = new Grid
        {
            ColumnSpacing = 12,
            RowSpacing = 12,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
            ColumnDefinitions =
            {
                primaryColumn,
                secondaryColumn,
            },
            RowDefinitions =
            {
                new RowDefinition { Height = GridLength.Auto },
                secondRow,
            },
        };
        primary.HorizontalAlignment = HorizontalAlignment.Stretch;
        primary.VerticalAlignment = VerticalAlignment.Stretch;
        secondary.HorizontalAlignment = HorizontalAlignment.Stretch;
        secondary.VerticalAlignment = VerticalAlignment.Stretch;
        grid.Children.Add(primary);
        grid.Children.Add(secondary);

        void ApplyLayout(bool useColumns)
        {
            primaryColumn.Width = new GridLength(useColumns ? primaryWeight : 1, GridUnitType.Star);
            secondaryColumn.Width = useColumns
                ? new GridLength(1, GridUnitType.Star)
                : new GridLength(0);
            secondRow.Height = useColumns ? new GridLength(0) : GridLength.Auto;
            Grid.SetColumn(primary, 0);
            Grid.SetRow(primary, 0);
            Grid.SetColumn(secondary, useColumns ? 1 : 0);
            Grid.SetRow(secondary, useColumns ? 0 : 1);
        }

        ApplyLayout(true);
        grid.SizeChanged += (_, args) => ApplyLayout(args.NewSize.Width >= breakpoint);
        return grid;
    }

    private Grid CreateBalancedDashboardTriplet(
        FrameworkElement first,
        FrameworkElement second,
        FrameworkElement third,
        double breakpoint = 900)
    {
        var firstColumn = new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) };
        var secondColumn = new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) };
        var thirdColumn = new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) };
        var secondRow = new RowDefinition { Height = new GridLength(0) };
        var thirdRow = new RowDefinition { Height = new GridLength(0) };
        var grid = new Grid
        {
            ColumnSpacing = 12,
            RowSpacing = 12,
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Stretch,
            ColumnDefinitions = { firstColumn, secondColumn, thirdColumn },
            RowDefinitions =
            {
                new RowDefinition { Height = GridLength.Auto },
                secondRow,
                thirdRow,
            },
        };
        foreach (var element in new[] { first, second, third })
        {
            element.HorizontalAlignment = HorizontalAlignment.Stretch;
            element.VerticalAlignment = VerticalAlignment.Stretch;
            grid.Children.Add(element);
        }

        void ApplyLayout(bool useColumns)
        {
            firstColumn.Width = new GridLength(1, GridUnitType.Star);
            secondColumn.Width = useColumns ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
            thirdColumn.Width = useColumns ? new GridLength(1, GridUnitType.Star) : new GridLength(0);
            secondRow.Height = useColumns ? new GridLength(0) : GridLength.Auto;
            thirdRow.Height = useColumns ? new GridLength(0) : GridLength.Auto;
            Grid.SetColumn(first, 0);
            Grid.SetRow(first, 0);
            Grid.SetColumn(second, useColumns ? 1 : 0);
            Grid.SetRow(second, useColumns ? 0 : 1);
            Grid.SetColumn(third, useColumns ? 2 : 0);
            Grid.SetRow(third, useColumns ? 0 : 2);
        }

        ApplyLayout(true);
        grid.SizeChanged += (_, args) => ApplyLayout(args.NewSize.Width >= breakpoint);
        return grid;
    }

    private Border CreateSectionCard(string title, params UIElement[] children)
    {
        var body = new StackPanel
        {
            Spacing = 10,
        };
        foreach (var child in children)
        {
            body.Children.Add(child);
        }
        var content = new StackPanel
        {
            Spacing = 12,
        };
        content.Children.Add(new TextBlock
        {
            Text = title,
            FontSize = 18,
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            Foreground = ThemeBrush("DgpPrimaryTextBrush"),
            TextWrapping = TextWrapping.Wrap,
        });
        content.Children.Add(new Border
        {
            Background = ThemeBrush("DgpInformationBrush"),
            BorderBrush = ThemeBrush("DgpFineBorderBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(10),
            Padding = new Thickness(14),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            Child = body,
        });
        return new Border
        {
            Background = ThemeBrush("DgpSurfaceBrush"),
            BorderBrush = ThemeBrush("DgpBorderBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(12),
            Padding = new Thickness(16),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            VerticalAlignment = VerticalAlignment.Top,
            Child = content,
        };
    }

    private Grid CreateAdaptiveColumns(FrameworkElement primary, FrameworkElement secondary, double breakpoint = 736)
    {
        var primaryColumn = new ColumnDefinition { Width = new GridLength(2, GridUnitType.Star) };
        var secondaryColumn = new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) };
        var secondRow = new RowDefinition { Height = new GridLength(0) };
        var grid = new Grid
        {
            ColumnSpacing = 16,
            RowSpacing = 16,
            ColumnDefinitions =
            {
                primaryColumn,
                secondaryColumn,
            },
            RowDefinitions =
            {
                new RowDefinition { Height = GridLength.Auto },
                secondRow,
            },
        };
        grid.Children.Add(primary);
        grid.Children.Add(secondary);

        void ApplyLayout(bool useColumns)
        {
            secondaryColumn.Width = useColumns
                ? new GridLength(1, GridUnitType.Star)
                : new GridLength(0);
            secondRow.Height = useColumns ? new GridLength(0) : GridLength.Auto;
            Grid.SetColumn(primary, 0);
            Grid.SetRow(primary, 0);
            Grid.SetColumn(secondary, useColumns ? 1 : 0);
            Grid.SetRow(secondary, useColumns ? 0 : 1);
        }

        ApplyLayout(true);
        grid.SizeChanged += (_, args) => ApplyLayout(args.NewSize.Width >= breakpoint);
        return grid;
    }

    private static T? FindDescendantByName<T>(DependencyObject parent, string name)
        where T : FrameworkElement
    {
        var childCount = VisualTreeHelper.GetChildrenCount(parent);
        for (var index = 0; index < childCount; index++)
        {
            var child = VisualTreeHelper.GetChild(parent, index);
            if (child is T match && string.Equals(match.Name, name, StringComparison.Ordinal))
            {
                return match;
            }
            if (FindDescendantByName<T>(child, name) is { } descendant)
            {
                return descendant;
            }
        }
        return null;
    }

    private Border CreateGlobalStatusBadge(
        string initialText,
        out TextBlock statusText,
        out StatusEllipse statusIndicator)
    {
        statusIndicator = new StatusEllipse
        {
            Width = 8,
            Height = 8,
            Fill = new SolidColorBrush(Windows.UI.Color.FromArgb(255, 191, 208, 230)),
            VerticalAlignment = VerticalAlignment.Center,
        };
        statusText = new TextBlock
        {
            Text = initialText,
            MinWidth = 112,
            Foreground = ThemeBrush("DgpTitleBarTextBrush"),
            FontFamily = ThemeFont(),
            FontSize = 12,
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            TextWrapping = TextWrapping.NoWrap,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var badge = new Border
        {
            Background = ThemeBrush("DgpNavigationHoverBrush"),
            BorderBrush = ThemeBrush("DgpBorderBrush"),
            BorderThickness = new Thickness(1),
            CornerRadius = new CornerRadius(8),
            Padding = new Thickness(10, 4, 10, 4),
            Child = new StackPanel
            {
                Orientation = Orientation.Horizontal,
                Spacing = 6,
                Children = { statusIndicator, statusText },
            },
        };
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(badge, initialText);
        return badge;
    }

    private Grid CreateTitleBar(MenuBar menuBar, NavigationView navigation)
    {
        var titleBar = new Grid
        {
            Height = 80,
            Background = ThemeBrush("DgpSidebarBrush"),
            RowDefinitions =
            {
                new RowDefinition { Height = new GridLength(44) },
                new RowDefinition { Height = new GridLength(36) },
            },
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = GridLength.Auto },
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = new GridLength(138) },
            },
        };
        var brandIcon = new Image
        {
            Width = 28,
            Height = 28,
            Stretch = Stretch.UniformToFill,
            Source = new BitmapImage(new Uri("ms-appx:///Assets/DeskGuardPro-icon.png")),
        };
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(brandIcon, "Desktop Guard Pro 图标");
        var brandLabel = new TextBlock
        {
            Text = "Desktop Guard Pro",
            Foreground = ThemeBrush("DgpTitleBarTextBrush"),
            FontFamily = ThemeFont(),
            FontSize = 14,
            FontWeight = Microsoft.UI.Text.FontWeights.SemiBold,
            VerticalAlignment = VerticalAlignment.Center,
        };
        var brand = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Margin = new Thickness(14, 0, 12, 0),
            VerticalAlignment = VerticalAlignment.Center,
            Children =
            {
                brandIcon,
                brandLabel,
            },
        };
        var navigationToggleButton = new Button
        {
            Width = 40,
            Height = 40,
            Padding = new Thickness(0),
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            Foreground = ThemeBrush("DgpTitleBarTextBrush"),
            Content = new SymbolIcon(Symbol.GlobalNavigationButton),
            Visibility = Visibility.Collapsed,
        };
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(navigationToggleButton, "打开导航");
        ToolTipService.SetToolTip(navigationToggleButton, "打开导航");
        navigationToggleButton.Click += (_, _) => navigation.IsPaneOpen = !navigation.IsPaneOpen;
        var leadingContent = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            VerticalAlignment = VerticalAlignment.Center,
            Children =
            {
                navigationToggleButton,
                brand,
            },
        };
        var globalServiceBadge = CreateGlobalStatusBadge(
            "服务：连接中", out globalServiceStatusText, out globalServiceStatusIndicator);
        var globalProtectionBadge = CreateGlobalStatusBadge(
            "保护：未开启", out globalProtectionStatusText, out globalProtectionStatusIndicator);
        var globalStatusPanel = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 8,
            Margin = new Thickness(8, 0, 12, 0),
            HorizontalAlignment = HorizontalAlignment.Right,
            VerticalAlignment = VerticalAlignment.Center,
            Children = { globalServiceBadge, globalProtectionBadge },
        };
        var commandRow = new Grid
        {
            ColumnDefinitions =
            {
                new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) },
                new ColumnDefinition { Width = GridLength.Auto },
            },
        };
        UpdateGlobalServiceStatus("connecting");
        UpdateGlobalProtectionStatus(null);
        var compactMenu = menuBar.Items
            .OfType<MenuBarItem>()
            .FirstOrDefault(item => string.Equals(item.Tag as string, "compact", StringComparison.Ordinal));
        var regularMenus = menuBar.Items
            .OfType<MenuBarItem>()
            .Where(item => !string.Equals(item.Tag as string, "compact", StringComparison.Ordinal))
            .ToArray();
        titleBar.SizeChanged += (_, args) =>
        {
            var compactNavigation = args.NewSize.Width < CompactLayoutWidth;
            navigationToggleButton.Visibility = compactNavigation ? Visibility.Visible : Visibility.Collapsed;
            brand.Visibility = compactNavigation ? Visibility.Collapsed : Visibility.Visible;
            brandLabel.Visibility = args.NewSize.Width < BrandLabelVisibilityWidth ? Visibility.Collapsed : Visibility.Visible;
            useCompactGlobalStatusLabels = args.NewSize.Width < ExpandedLayoutWidth;
            ApplyGlobalStatusLabels();
            foreach (var regularMenu in regularMenus)
            {
                regularMenu.Visibility = compactNavigation ? Visibility.Collapsed : Visibility.Visible;
            }
            if (compactMenu is not null)
            {
                compactMenu.Visibility = compactNavigation ? Visibility.Visible : Visibility.Collapsed;
            }
            brand.Margin = args.NewSize.Width < BrandLabelVisibilityWidth
                ? new Thickness(6, 0, 5, 0)
                : new Thickness(11, 0, 10, 0);
        };
        var dragRegion = new Grid
        {
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
        };
        titleBarDragRegion = dragRegion;
        Grid.SetColumn(leadingContent, 0);
        Grid.SetRow(leadingContent, 0);
        Grid.SetColumn(menuBar, 0);
        Grid.SetColumn(globalStatusPanel, 1);
        commandRow.Children.Add(menuBar);
        commandRow.Children.Add(globalStatusPanel);
        Grid.SetColumn(commandRow, 0);
        Grid.SetColumnSpan(commandRow, 3);
        Grid.SetRow(commandRow, 1);
        Grid.SetColumn(dragRegion, 1);
        Grid.SetRow(dragRegion, 0);
        titleBar.Children.Add(leadingContent);
        titleBar.Children.Add(dragRegion);
        titleBar.Children.Add(commandRow);
        return titleBar;
    }

    private MenuBar CreateMenuBar(
        NavigationView navigation,
        StackPanel directoryEditor,
        Func<Task> refreshHealthAsync,
        Func<bool, Task> loadHistoryAsync,
        Func<bool, Task> loadTimelineAsync,
        Func<Task> loadRiskAsync,
        Func<Task> loadAssetDifferencesAsync)
    {
        var menuBar = new MenuBar
        {
            Height = 36,
            Background = ThemeBrush("DgpSidebarBrush"),
            Foreground = ThemeBrush("DgpTitleBarTextBrush"),
            FontFamily = ThemeFont(),
            FontSize = 14,
            VerticalAlignment = VerticalAlignment.Center,
        };
        menuBar.Resources["MenuBarBackground"] = ThemeBrush("DgpSidebarBrush");
        menuBar.Resources["MenuBarItemForeground"] = ThemeBrush("DgpTitleBarTextBrush");
        menuBar.Resources["MenuBarItemBackground"] = ThemeBrush("DgpSidebarBrush");
        menuBar.Resources["MenuBarItemBackgroundPointerOver"] = ThemeBrush("DgpNavigationHoverBrush");
        menuBar.Resources["MenuBarItemBackgroundPressed"] = ThemeBrush("DgpBrandBrush");
        menuBar.Resources["MenuBarItemBackgroundSelected"] = ThemeBrush("DgpBrandBrush");

        static MenuBarItem CreateTopMenu(string title) => new()
        {
            Title = title,
            MinWidth = 40,
            Padding = new Thickness(8, 0, 8, 0),
        };

        void NavigateTo(string tag)
        {
            var target = navigation.MenuItems
                .OfType<NavigationViewItem>()
                .FirstOrDefault(item => string.Equals(item.Tag as string, tag, StringComparison.Ordinal));
            if (target is not null)
            {
                navigation.SelectedItem = target;
            }
        }

        void FocusDirectories()
        {
            NavigateTo("overview");
            RevealDashboardScope();
            var firstDirectoryInput = directoryEditor.Children
                .OfType<Grid>()
                .Select(row => row.Children.OfType<TextBox>().FirstOrDefault())
                .FirstOrDefault(input => input is not null);
            firstDirectoryInput?.Focus(FocusState.Programmatic);
        }

        async Task RefreshCurrentPageAsync()
        {
            switch ((navigation.SelectedItem as NavigationViewItem)?.Tag as string)
            {
                case "history":
                    await loadHistoryAsync(false);
                    break;
                case "audit":
                    await loadTimelineAsync(false);
                    break;
                case "risk":
                    await loadRiskAsync();
                    break;
                case "assets":
                    await loadAssetDifferencesAsync();
                    break;
                default:
                    await refreshHealthAsync();
                    break;
            }
        }

        static MenuFlyoutItem CreateItem(string text, Action action)
        {
            var item = new MenuFlyoutItem { Text = text };
            item.Click += (_, _) => action();
            return item;
        }

        static MenuFlyoutItem CreateAsyncItem(string text, Func<Task> action)
        {
            var item = new MenuFlyoutItem { Text = text };
            item.Click += async (_, _) => await action();
            return item;
        }

        static MenuFlyoutItem WithShortcut(
            MenuFlyoutItem item,
            VirtualKey key,
            VirtualKeyModifiers modifiers = VirtualKeyModifiers.Control)
        {
            item.KeyboardAccelerators.Add(new KeyboardAccelerator { Key = key, Modifiers = modifiers });
            return item;
        }

        void FocusExclusions()
        {
            NavigateTo("overview");
            RevealDashboardScope();
            var firstExclusionInput = exclusionInputs?.Children
                .OfType<Grid>()
                .Select(row => row.Children.OfType<TextBox>().FirstOrDefault())
                .FirstOrDefault(input => input is not null);
            firstExclusionInput?.Focus(FocusState.Programmatic);
        }

        async Task ShowAboutAsync()
        {
            if (navigation.XamlRoot is null)
            {
                return;
            }
            var dialog = new ContentDialog
            {
                Title = "Desktop Guard Pro",
                Content = $"版本 {typeof(App).Assembly.GetName().Version?.ToString(3)}\n" +
                    "Windows 本机保护、审计与风险分析工具。\n" +
                    "界面自启动与偏好仅对当前用户生效。所有审计数据默认保存在本机。",
                CloseButtonText = "关闭",
                XamlRoot = navigation.XamlRoot,
            };
            await dialog.ShowAsync();
        }

        var fileMenu = CreateTopMenu("文件");
        fileMenu.Items.Add(WithShortcut(CreateItem("历史会话", () => NavigateTo("history")), VirtualKey.H));
        fileMenu.Items.Add(WithShortcut(CreateItem("报告导出", () => NavigateTo("reports")), VirtualKey.E));
        fileMenu.Items.Add(new MenuFlyoutSeparator());
        fileMenu.Items.Add(CreateItem("退出", ExitMainWindow));

        var editMenu = CreateTopMenu("编辑");
        editMenu.Items.Add(WithShortcut(CreateItem("重点对象与递归设置", FocusDirectories), VirtualKey.D));
        editMenu.Items.Add(CreateItem("排除规则设置", FocusExclusions));
        editMenu.Items.Add(new MenuFlyoutSeparator());
        editMenu.Items.Add(CreateItem("系统设置", () => NavigateTo("monitoring-policy")));

        var viewMenu = CreateTopMenu("查看");
        foreach (var page in new[]
        {
            ("仪表盘", "overview", VirtualKey.Number1),
            ("历史会话", "history", VirtualKey.Number2),
            ("事件时间线", "audit", VirtualKey.Number3),
            ("风险分析", "risk", VirtualKey.Number4),
            ("资产差异", "assets", VirtualKey.Number5),
            ("报告导出", "reports", VirtualKey.Number6),
            ("系统设置", "monitoring-policy", VirtualKey.Number7),
        })
        {
            viewMenu.Items.Add(WithShortcut(CreateItem(page.Item1, () => NavigateTo(page.Item2)), page.Item3));
        }
        viewMenu.Items.Add(new MenuFlyoutSeparator());
        viewMenu.Items.Add(WithShortcut(CreateAsyncItem("刷新当前页面", RefreshCurrentPageAsync), VirtualKey.F5, VirtualKeyModifiers.None));
        viewMenu.Items.Add(WithShortcut(CreateItem("展开或收起导航栏", () => navigation.IsPaneOpen = !navigation.IsPaneOpen), VirtualKey.B));

        var settingsMenu = CreateTopMenu("设置");
        foreach (var item in CreateSettingsMenuItems(NavigateTo, FocusDirectories, FocusExclusions))
            settingsMenu.Items.Add(item);
        settingsMenu.Items.Add(new MenuFlyoutSeparator());
        settingsMenu.Items.Add(CreateAsyncItem("关于 Desktop Guard Pro", ShowAboutAsync));

        var compactMenu = CreateTopMenu("菜单");
        compactMenu.Tag = "compact";
        compactMenu.Visibility = Visibility.Collapsed;
        foreach (var page in new[]
        {
            ("仪表盘", "overview"), ("历史会话", "history"), ("事件时间线", "audit"),
            ("风险分析", "risk"), ("资产差异", "assets"), ("报告导出", "reports"),
            ("系统设置", "monitoring-policy"),
        })
        {
            compactMenu.Items.Add(CreateItem(page.Item1, () => NavigateTo(page.Item2)));
        }
        compactMenu.Items.Add(new MenuFlyoutSeparator());
        compactMenu.Items.Add(CreateItem("重点对象与递归设置", FocusDirectories));
        compactMenu.Items.Add(CreateItem("排除规则设置", FocusExclusions));
        compactMenu.Items.Add(new MenuFlyoutSeparator());
        compactMenu.Items.Add(CreateAsyncItem("刷新当前页面", RefreshCurrentPageAsync));
        compactMenu.Items.Add(CreateItem("展开或收起导航栏", () => navigation.IsPaneOpen = !navigation.IsPaneOpen));
        var compactSettings = new MenuFlyoutSubItem { Text = "设置" };
        foreach (var item in CreateSettingsMenuItems(NavigateTo, FocusDirectories, FocusExclusions))
            compactSettings.Items.Add(item);
        compactSettings.Items.Add(new MenuFlyoutSeparator());
        compactSettings.Items.Add(CreateAsyncItem("关于 Desktop Guard Pro", ShowAboutAsync));
        compactMenu.Items.Add(compactSettings);
        compactMenu.Items.Add(new MenuFlyoutSeparator());
        compactMenu.Items.Add(CreateItem("退出", ExitMainWindow));

        menuBar.Items.Add(fileMenu);
        menuBar.Items.Add(editMenu);
        menuBar.Items.Add(viewMenu);
        menuBar.Items.Add(settingsMenu);
        menuBar.Items.Add(compactMenu);
        return menuBar;
    }

    private SolidColorBrush ThemeBrush(string key) => (SolidColorBrush)Resources[key];

    private FontFamily ThemeFont() => (FontFamily)Resources["DgpContentFontFamily"];

    private static void ApplyCompactDensity(DependencyObject root)
    {
        var visited = new HashSet<DependencyObject>();
        ScaleNode(root);

        void ScaleNode(DependencyObject node)
        {
            if (!visited.Add(node))
            {
                return;
            }
            if (node is FrameworkElement element)
            {
                ScaleLocalLength(element, FrameworkElement.WidthProperty, value => element.Width = value);
                ScaleLocalLength(element, FrameworkElement.HeightProperty, value => element.Height = value);
                ScaleLocalLength(element, FrameworkElement.MinWidthProperty, value => element.MinWidth = value);
                ScaleLocalLength(element, FrameworkElement.MinHeightProperty, value => element.MinHeight = value);
                ScaleLocalLength(element, FrameworkElement.MaxWidthProperty, value => element.MaxWidth = value);
                ScaleLocalLength(element, FrameworkElement.MaxHeightProperty, value => element.MaxHeight = value);
                if (element.ReadLocalValue(FrameworkElement.MarginProperty) is Thickness margin)
                {
                    element.Margin = ScaleThickness(margin);
                }
            }
            if (node is Control control)
            {
                if (control.ReadLocalValue(Control.FontSizeProperty) is double fontSize && fontSize > 0)
                {
                    control.FontSize = Scale(fontSize);
                }
                if (control.ReadLocalValue(Control.PaddingProperty) is Thickness padding)
                {
                    control.Padding = ScaleThickness(padding);
                }
                if (control.ReadLocalValue(Control.CornerRadiusProperty) is CornerRadius cornerRadius)
                {
                    control.CornerRadius = ScaleCornerRadius(cornerRadius);
                }
            }
            if (node is TextBlock textBlock &&
                textBlock.ReadLocalValue(TextBlock.FontSizeProperty) is double textSize && textSize > 0)
            {
                textBlock.FontSize = Scale(textSize);
            }
            if (node is Border border)
            {
                border.Padding = ScaleThickness(border.Padding);
                border.CornerRadius = ScaleCornerRadius(border.CornerRadius);
            }
            if (node is StackPanel stackPanel)
            {
                stackPanel.Spacing = Scale(stackPanel.Spacing);
            }
            if (node is Grid grid)
            {
                grid.ColumnSpacing = Scale(grid.ColumnSpacing);
                grid.RowSpacing = Scale(grid.RowSpacing);
                foreach (var column in grid.ColumnDefinitions)
                {
                    if (column.Width.IsAbsolute)
                    {
                        column.Width = new GridLength(Scale(column.Width.Value));
                    }
                }
                foreach (var row in grid.RowDefinitions)
                {
                    if (row.Height.IsAbsolute)
                    {
                        row.Height = new GridLength(Scale(row.Height.Value));
                    }
                }
            }
            if (node is NavigationView navigationView)
            {
                navigationView.OpenPaneLength = Scale(navigationView.OpenPaneLength);
                navigationView.CompactPaneLength = Scale(navigationView.CompactPaneLength);
                navigationView.ExpandedModeThresholdWidth = Scale(navigationView.ExpandedModeThresholdWidth);
                navigationView.CompactModeThresholdWidth = Scale(navigationView.CompactModeThresholdWidth);
                foreach (var item in navigationView.MenuItems.OfType<DependencyObject>())
                {
                    ScaleNode(item);
                }
            }
            if (node is ContentControl contentControl && contentControl.Content is DependencyObject content)
            {
                ScaleNode(content);
            }
            if (node is ItemsControl itemsControl)
            {
                foreach (var item in itemsControl.Items.OfType<DependencyObject>())
                {
                    ScaleNode(item);
                }
            }
            var childCount = VisualTreeHelper.GetChildrenCount(node);
            for (var index = 0; index < childCount; index++)
            {
                ScaleNode(VisualTreeHelper.GetChild(node, index));
            }
        }

        static void ScaleLocalLength(FrameworkElement element, DependencyProperty property, Action<double> assign)
        {
            if (element.ReadLocalValue(property) is double value && double.IsFinite(value) && value > 0)
            {
                assign(Scale(value));
            }
        }

        static double Scale(double value) => Math.Round(value * InterfaceScale, 1);

        static Thickness ScaleThickness(Thickness value) => new(
            Scale(value.Left), Scale(value.Top), Scale(value.Right), Scale(value.Bottom));

        static CornerRadius ScaleCornerRadius(CornerRadius value) => new(
            Scale(value.TopLeft), Scale(value.TopRight), Scale(value.BottomRight), Scale(value.BottomLeft));
    }

    [UnmanagedFunctionPointer(CallingConvention.Winapi)]
    private delegate IntPtr WindowProcedure(IntPtr handle, uint message, IntPtr wParam, IntPtr lParam);

    [StructLayout(LayoutKind.Sequential)]
    private struct NativePoint
    {
        public int X;
        public int Y;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MinMaxInfo
    {
        public NativePoint Reserved;
        public NativePoint MaximumSize;
        public NativePoint MaximumPosition;
        public NativePoint MinimumTrackingSize;
        public NativePoint MaximumTrackingSize;
    }

    [DllImport("user32.dll", EntryPoint = "SetWindowLongPtrW", SetLastError = true)]
    private static extern IntPtr SetWindowLongPtr(IntPtr handle, int index, IntPtr value);

    [DllImport("user32.dll", EntryPoint = "CallWindowProcW")]
    private static extern IntPtr CallWindowProcedure(
        IntPtr previousProcedure,
        IntPtr handle,
        uint message,
        IntPtr wParam,
        IntPtr lParam);
}
