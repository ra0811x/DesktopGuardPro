using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.System;

namespace DesktopGuardPro.Native;

public partial class App
{
    private readonly UiPreferencesStore uiPreferencesStore = new(Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "DesktopGuardPro", "ui-preferences.json"));
    private readonly UiStartupRegistration uiStartup = new(new UiStartupRegistry(),
        Environment.ProcessPath ?? Path.Combine(AppContext.BaseDirectory, "desktop-guard-ui.exe"));
    private readonly Dictionary<string, List<ToggleMenuFlyoutItem>> settingsToggles = new();
    private UiPreferences uiPreferences = new();
    private bool uiStartupEnabled;
    private bool exitRequested;
    private bool settingsActionBusy;
    private string? settingsLoadError;
    private readonly CancellationTokenSource startupConnectionCancellation = new();

    private async Task InitializeServiceViewsAsync()
    {
        try
        {
            if (!await StartupServiceConnection.TryConnectAsync(
                RefreshHealthAsync, startupConnectionCancellation.Token)) return;
            // Load editable configuration once after connecting. Status polling
            // must not overwrite the user's draft on later refreshes.
            await LoadDirectoriesAsync();
            await LoadInputShieldManagementAsync();
            await LoadMonitoringPolicyAsync();
        }
        catch (OperationCanceledException) { }
    }

    private void LoadUiPreferences()
    {
        try { uiPreferences = uiPreferencesStore.Load(); }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Text.Json.JsonException)
        {
            settingsLoadError = $"界面偏好未能读取，暂用默认值：{error.Message}";
        }
        try { uiStartupEnabled = uiStartup.IsEnabled; }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or System.Security.SecurityException)
        {
            settingsLoadError = $"自启动状态未能读取：{error.Message}";
        }
    }

    private void ConfigureWindowPreferences()
    {
        if (mainWindow is null) return;
        mainWindow.AppWindow.Closing += (_, args) =>
        {
            if (!uiPreferences.ShouldHideOnClose(exitRequested, trayIcon?.IsAvailable == true)) return;
            args.Cancel = true;
            mainWindow.AppWindow.Hide();
        };
        if (uiPreferences.ShouldStartInTray(Environment.GetCommandLineArgs(), trayIcon?.IsAvailable == true))
            mainWindow.AppWindow.Hide();
        ApplyAutomaticRefresh();
        if (settingsLoadError is not null)
            _ = ShowSettingsNoticeAsync("设置读取失败", settingsLoadError);
    }

    private void RestoreMainWindow()
    {
        if (mainWindow is null) return;
        if (mainWindow.AppWindow.Presenter is OverlappedPresenter presenter &&
            presenter.State == OverlappedPresenterState.Minimized) presenter.Restore();
        mainWindow.AppWindow.Show();
        mainWindow.Activate();
    }

    private void ExitMainWindow()
    {
        exitRequested = true;
        mainWindow?.Close();
    }

    private void ApplyAutomaticRefresh()
    {
        if (uiPreferences.AutoRefresh) statusRefreshTimer?.Start();
        else statusRefreshTimer?.Stop();
    }

    private async Task ShowSettingsNoticeAsync(string title, string message)
    {
        if (mainWindow?.Content is not FrameworkElement { XamlRoot: not null } root) return;
        await new ContentDialog
        {
            Title = title, Content = message, CloseButtonText = "关闭", XamlRoot = root.XamlRoot,
        }.ShowAsync();
    }

    private async Task RunSettingsActionAsync(Func<Task> action)
    {
        if (settingsActionBusy) return;
        settingsActionBusy = true;
        try { await action(); }
        catch (Exception error) { await ShowSettingsNoticeAsync("设置未完成", error.Message); }
        finally { settingsActionBusy = false; }
    }

    private async Task SetUiOptionAsync(string key, bool enabled)
    {
        try
        {
            if (key == "startup")
            {
                uiStartup.SetEnabled(enabled);
                uiStartupEnabled = uiStartup.IsEnabled;
            }
            else
            {
                var updated = key switch
                {
                    "start-in-tray" => uiPreferences with { StartInTray = enabled },
                    "close-to-tray" => uiPreferences with { CloseToTray = enabled },
                    "auto-refresh" => uiPreferences with { AutoRefresh = enabled },
                    _ => throw new ArgumentException("未知界面设置。"),
                };
                uiPreferencesStore.Save(updated);
                uiPreferences = updated;
                ApplyAutomaticRefresh();
            }
        }
        finally { SynchronizeSettingsToggles(); }
        await Task.CompletedTask;
    }

    private void SynchronizeSettingsToggles()
    {
        foreach (var (key, items) in settingsToggles)
        {
            var selected = key switch
            {
                "startup" => uiStartupEnabled,
                "start-in-tray" => uiPreferences.StartInTray,
                "close-to-tray" => uiPreferences.CloseToTray,
                "auto-refresh" => uiPreferences.AutoRefresh,
                _ => uiPreferences.DefaultReportFormat == key,
            };
            foreach (var item in items) item.IsChecked = selected;
        }
    }

    private IReadOnlyList<MenuFlyoutItemBase> CreateSettingsMenuItems(
        Action<string> navigate, Action focusDirectories, Action focusExclusions)
    {
        static MenuFlyoutItem Item(string text, Action action)
        {
            var item = new MenuFlyoutItem { Text = text };
            item.Click += (_, _) => action();
            return item;
        }
        MenuFlyoutItem AsyncItem(string text, Func<Task> action) =>
            Item(text, () => _ = RunSettingsActionAsync(action));
        void RegisterToggle(string key, ToggleMenuFlyoutItem item)
        {
            if (!settingsToggles.TryGetValue(key, out var items))
                settingsToggles[key] = items = new List<ToggleMenuFlyoutItem>();
            items.Add(item);
        }
        ToggleMenuFlyoutItem Toggle(string text, string key)
        {
            var item = new ToggleMenuFlyoutItem { Text = text };
            RegisterToggle(key, item);
            item.Click += (_, _) =>
            {
                var enabled = item.IsChecked;
                // Revert the automatic menu check until the setting is saved.
                SynchronizeSettingsToggles();
                _ = RunSettingsActionAsync(() => SetUiOptionAsync(key, enabled));
            };
            return item;
        }

        var general = new MenuFlyoutSubItem { Text = "常规与启动" };
        var startupToggle = Toggle("开机自启动（登录后）", "startup");
        ToolTipService.SetToolTip(startupToggle, "进入 Windows 桌面后启动主界面；后台服务由系统单独启动。");
        general.Items.Add(startupToggle);
        general.Items.Add(Toggle("自启动时收起到托盘", "start-in-tray"));
        general.Items.Add(Toggle("关闭窗口时收起到托盘", "close-to-tray"));
        general.Items.Add(new MenuFlyoutSeparator());
        general.Items.Add(Toggle("自动刷新服务状态（5 秒）", "auto-refresh"));
        general.Items.Add(AsyncItem("Windows 启动应用管理", async () =>
        {
            if (!await Launcher.LaunchUriAsync(new Uri("ms-settings:startupapps")))
                throw new InvalidOperationException("无法打开 Windows 启动应用管理。");
        }));

        void OpenMode(string mode)
        {
            navigate("monitoring-policy");
            if (settingsMonitoringMode is not null)
                settingsMonitoringMode.SelectedItem = settingsMonitoringMode.Items.OfType<ComboBoxItem>()
                    .FirstOrDefault(item => string.Equals(item.Tag as string, mode, StringComparison.Ordinal));
        }
        var protection = new MenuFlyoutSubItem { Text = "保护与采集" };
        protection.Items.Add(Item("系统设置", () => navigate("monitoring-policy")));
        var modes = new MenuFlyoutSubItem { Text = "模式配置" };
        foreach (var (title, mode) in new[]
                 { ("宽松", "relaxed"), ("标准", "standard"), ("严格", "strict"), ("自定义", "custom") })
            modes.Items.Add(Item(title, () => OpenMode(mode)));
        protection.Items.Add(modes);
        var modules = new MenuFlyoutSubItem { Text = "采集模块详情" };
        foreach (var (title, action) in new (string, Func<Task>)[]
        {
            ("文件审计", ShowFilePolicyDetailsAsync), ("进程与软件", ShowProcessPolicyDetailsAsync),
            ("系统与网络", ShowSystemPolicyDetailsAsync), ("外接设备", ShowDevicePolicyDetailsAsync),
            ("用户会话活动", ShowUserSessionPolicyDetailsAsync),
        })
            modules.Items.Add(AsyncItem(title, async () =>
            {
                navigate("monitoring-policy");
                await LoadMonitoringPolicyAsync();
                if (!monitoringPolicyLoaded) throw new InvalidOperationException("监控策略尚未读取成功，请检查服务后重试。");
                await action();
            }));
        protection.Items.Add(modules);
        protection.Items.Add(new MenuFlyoutSeparator());
        protection.Items.Add(Item("重点对象与递归设置", focusDirectories));
        protection.Items.Add(Item("排除规则设置", focusExclusions));

        var input = new MenuFlyoutSubItem { Text = "临时输入控制" };
        input.Items.Add(AsyncItem("控制规则设置", async () =>
        {
            await LoadMonitoringPolicyAsync();
            if (!monitoringPolicyLoaded) throw new InvalidOperationException("控制策略尚未读取成功，请检查服务后重试。");
            await ShowInputShieldPolicyDetailsAsync();
        }));
        input.Items.Add(AsyncItem("查看输入设备", ShowInputShieldDevicesAsync));
        input.Items.Add(new MenuFlyoutSeparator());
        input.Items.Add(AsyncItem("设置本地密码与恢复码", ConfigureInputShieldCredentialsAsync));
        input.Items.Add(AsyncItem("删除本地密码", DeleteInputShieldCredentialsAsync));

        var data = new MenuFlyoutSubItem { Text = "数据与报告" };
        data.Items.Add(Item("会话保留设置", () => navigate("history")));
        data.Items.Add(Item("敏感字段与报告设置", () => navigate("reports")));
        var formats = new MenuFlyoutSubItem { Text = "默认报告格式" };
        foreach (var (title, format) in new[] { ("HTML", "html"), ("Markdown", "markdown"), ("JSON", "json") })
        {
            var item = new ToggleMenuFlyoutItem { Text = title };
            RegisterToggle(format, item);
            item.Click += (_, _) =>
            {
                SynchronizeSettingsToggles();
                _ = RunSettingsActionAsync(async () =>
                {
                    var updated = uiPreferences with { DefaultReportFormat = format };
                    uiPreferencesStore.Save(updated);
                    uiPreferences = updated;
                    if (reportFormat is not null)
                        reportFormat.SelectedItem = reportFormat.Items.OfType<ComboBoxItem>()
                            .FirstOrDefault(option => string.Equals(option.Tag as string, format, StringComparison.Ordinal));
                    SynchronizeSettingsToggles();
                    await Task.CompletedTask;
                });
            };
            formats.Items.Add(item);
        }
        data.Items.Add(formats);
        SynchronizeSettingsToggles();
        return new MenuFlyoutItemBase[] { general, protection, input, data };
    }
}
