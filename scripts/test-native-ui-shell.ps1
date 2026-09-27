[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$appXamlPath = Join-Path $projectRoot 'frontend\DesktopGuardPro.Native\App.xaml'
$appCodePath = Join-Path $projectRoot 'frontend\DesktopGuardPro.Native\App.xaml.cs'
$credentialPromptPath = Join-Path $projectRoot 'frontend\DesktopGuardPro.Native\WindowsCredentialPrompt.cs'
$projectPath = Join-Path $projectRoot 'frontend\DesktopGuardPro.Native\DesktopGuardPro.Native.csproj'
$manifestPath = Join-Path $projectRoot 'frontend\DesktopGuardPro.Native\app.manifest'
$brandImagePath = Join-Path $projectRoot 'assets\desktop-guard-pro.png'
$appXaml = Get-Content -LiteralPath $appXamlPath -Raw
$appCode = Get-Content -LiteralPath $appCodePath -Raw
$credentialPromptCode = Get-Content -LiteralPath $credentialPromptPath -Raw
$project = Get-Content -LiteralPath $projectPath -Raw

if (-not (Test-Path -LiteralPath $manifestPath)) {
    throw 'Native UI application manifest is missing.'
}
if (-not (Test-Path -LiteralPath $brandImagePath)) {
    throw 'Desktop Guard Pro brand image is missing.'
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw
if ($project -notmatch '<ApplicationManifest>app\.manifest</ApplicationManifest>' -or
    $manifest -notmatch '<dpiAwareness[^>]*>PerMonitorV2, PerMonitor</dpiAwareness>') {
    throw 'Native UI must embed a PerMonitorV2 application manifest.'
}
if ($project -notmatch 'assets\\desktop-guard-pro\.png' -or
    $project -notmatch 'Assets\\DeskGuardPro-icon\.png') {
    throw 'Native UI must package the approved Desktop Guard Pro image.'
}
if (-not $appCode.Contains('ms-appx:///Assets/DeskGuardPro-icon.png') -or $appCode.Contains('Text = "DG"')) {
    throw 'Native UI title bar must show the approved brand image instead of the DG letters.'
}

foreach ($resourceName in @(
    'DgpCanvasBrush',
    'DgpSidebarBrush',
    'DgpSurfaceBrush',
    'DgpInformationBrush',
    'DgpFineBorderBrush',
    'DgpPrimaryTextBrush',
    'DgpSecondaryTextBrush')) {
    if ($appXaml -notmatch ('x:Key="' + [regex]::Escape($resourceName) + '"')) {
        throw "Native UI theme resource is missing: $resourceName"
    }
}
if ($appXaml -match 'Acrylic|LinearGradientBrush|RadialGradientBrush') {
    throw 'Native UI theme must not use acrylic or gradient brushes.'
}
if ($appCode -match 'Colors\.DimGray') {
    throw 'Native UI still uses low-contrast DimGray text.'
}
foreach ($compactScaleRequirement in @(
    'private const int DefaultWindowWidth = 1734;',
    'private const int DefaultWindowHeight = 1070;',
    'private const int MinimumWindowWidth = 1280;',
    'private const int MinimumWindowHeight = 760;',
    'DisplayArea.GetFromWindowId(window.AppWindow.Id, DisplayAreaFallback.Primary)',
    'displayArea.WorkArea',
    'private const double InterfaceScale = 0.8;',
    'private const double CompactLayoutWidth = 512;',
    'private const double BrandLabelVisibilityWidth = 608;',
    'private const double ExpandedLayoutWidth = 800;',
    'double breakpoint = 736',
    'var padding = args.NewSize.Width < CompactLayoutWidth',
    'args.NewSize.Width < ExpandedLayoutWidth ? 10 : 13',
    'brandLabel.Visibility = args.NewSize.Width < BrandLabelVisibilityWidth',
    'ApplyCompactDensity(root);',
    'private static void ApplyCompactDensity(DependencyObject root)',
    'var shouldEndExistingSession = session?.State is "active" or "degraded" or "paused";',
    'if (shouldEndExistingSession && session?.State is "active" or "degraded" or "paused")')) {
    if (-not $appCode.Contains($compactScaleRequirement)) {
        throw "Native UI 80-percent density requirement is missing: $compactScaleRequirement"
    }
}
if (-not $credentialPromptCode.Contains('CredUiWinPromptFlags);') -or
    $credentialPromptCode.Contains('CredUiWinSecurePrompt')) {
    throw 'Native UI must not force the secure desktop credential prompt.'
}
foreach ($compactControlStyle in @(
    '<Style TargetType="Button">',
    '<Style TargetType="TextBox">',
    '<Style TargetType="ComboBox">',
    '<Setter Property="FontSize" Value="11.2" />',
    '<Setter Property="MinHeight" Value="28.8" />')) {
    if (-not $appXaml.Contains($compactControlStyle)) {
        throw "Native UI compact control style is missing: $compactControlStyle"
    }
}
foreach ($required in @('RequestedTheme = ElementTheme.Light', 'ExtendsContentIntoTitleBar = true', 'SetTitleBar(', 'CreateMenuBar(')) {
    if (-not $appCode.Contains($required)) {
        throw "Native UI shell requirement is missing: $required"
    }
}
$pageSurface = [regex]::Match(
    $appCode,
    'private Border CreatePageSurface\(FrameworkElement content\)[\s\S]+?^    \}',
    [Text.RegularExpressions.RegexOptions]::Multiline).Value
if (-not $pageSurface) {
    throw 'Native UI page surface factory is missing.'
}
if ($pageSurface -notmatch 'Margin\s*=\s*new Thickness\(0\)') {
    throw 'Native UI page surface must not leave outer whitespace.'
}
if ($pageSurface -notmatch 'CornerRadius\s*=\s*new CornerRadius\(0\)') {
    throw 'Native UI page surface corners must meet the navigation content boundary.'
}
if ($pageSurface -notmatch 'Background\s*=\s*ThemeBrush\("DgpCanvasBrush"\)') {
    throw 'Native UI page surface must use the light-blue canvas instead of a large white sheet.'
}
if (-not $appCode.Contains('navigation.Resources["NavigationViewContentMargin"] = new Thickness(0);')) {
    throw 'NavigationView content template must not leave a residual outer margin.'
}
foreach ($paneBoundaryRequirement in @(
    'navigation.CornerRadius = new CornerRadius(0);',
    'FindDescendantByName<SplitView>(navigation, "RootSplitView")',
    'splitView.CornerRadius = new CornerRadius(0);',
    'navigation.Resources["NavigationViewPaneContentGridMargin"] = new Thickness(0);',
    'navigation.Resources["NavigationViewBorderThickness"] = new Thickness(0);',
    'navigation.Resources["NavigationViewContentGridBorderThickness"] = new Thickness(0);',
    'navigation.Resources["OverlayCornerRadius"] = new CornerRadius(0);')) {
    if (-not $appCode.Contains($paneBoundaryRequirement)) {
        throw "Navigation pane boundary requirement is missing: $paneBoundaryRequirement"
    }
}
if ($appCode -match 'PaneHeader\s*=\s*new TextBlock[\s\S]{0,400}?Text\s*=\s*"Desktop Guard Pro"') {
    throw 'The navigation pane must not repeat the Desktop Guard Pro product name.'
}
foreach ($responsiveRequirement in @(
    'PaneDisplayMode = NavigationViewPaneDisplayMode.Auto',
    'ExpandedModeThresholdWidth = 1000',
    'CompactModeThresholdWidth = 640',
    'VerticalScrollBarVisibility = ScrollBarVisibility.Auto',
    'HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled',
    'args.NewSize.Width < CompactLayoutWidth',
    'assetFilters.Orientation =',
    'brandLabel.Visibility =')) {
    if (-not $appCode.Contains($responsiveRequirement)) {
        throw "Native UI responsive requirement is missing: $responsiveRequirement"
    }
}
foreach ($compactNavigationRequirement in @(
    'CreateTitleBar(menuBar, navigation)',
    'navigationToggleButton.Visibility =',
    'navigation.IsPaneOpen = !navigation.IsPaneOpen',
    'var compactMenu = CreateTopMenu("菜单")',
    'compactMenu.Visibility = compactNavigation ? Visibility.Visible : Visibility.Collapsed',
    'regularMenu.Visibility = compactNavigation ? Visibility.Collapsed : Visibility.Visible')) {
    if (-not $appCode.Contains($compactNavigationRequirement)) {
        throw "Compact navigation requirement is missing: $compactNavigationRequirement"
    }
}
if ($appCode -match 'reportFormat\s*=\s*new ComboBox\s*\{[^}]*Width\s*=\s*220' -or
    $appCode -match 'reportObjectDetails\s*=\s*new ComboBox\s*\{[^}]*Width\s*=\s*220') {
    throw 'Report selectors must not keep a fixed width in narrow windows.'
}
foreach ($densityRequirement in @(
    'private Border CreateSectionCard(',
    'private Grid CreateWorkspaceToolbar(',
    'private Grid CreateWorkspaceForm(',
    'args.NewSize.Width >= breakpoint')) {
    if (-not $appCode.Contains($densityRequirement)) {
        throw "Native UI content-density requirement is missing: $densityRequirement"
    }
}
if ($appCode -notmatch 'Grid\.SetRow\(secondary,\s*useColumns\s*\?\s*0\s*:\s*1\)') {
    throw 'Adaptive columns must move secondary content below the primary content in narrow windows.'
}
if (@([regex]::Matches($appCode, 'CreateWorkspaceCard\(')).Count -lt 10) {
    throw 'Analysis and settings pages must use the shared dashboard-style card.'
}
# DeskGuard locks keyboard and mouse together; their former switch pair is a single description.
if (@([regex]::Matches($appCode, 'CreateBalancedDashboardColumns\(')).Count -lt 8) {
    throw 'Dashboard, analysis and settings pages must provide balanced responsive rows.'
}
$sectionCard = [regex]::Match(
    $appCode,
    'private Border CreateSectionCard\(string title, params UIElement\[\] children\)[\s\S]+?^    \}',
    [Text.RegularExpressions.RegexOptions]::Multiline).Value
if (-not $sectionCard) {
    throw 'Native UI section card factory is missing.'
}
foreach ($cardLayerRequirement in @(
    'Background = ThemeBrush("DgpSurfaceBrush")',
    'Background = ThemeBrush("DgpInformationBrush")',
    'BorderBrush = ThemeBrush("DgpFineBorderBrush")',
    'VerticalAlignment = VerticalAlignment.Top')) {
    if (-not $sectionCard.Contains($cardLayerRequirement)) {
        throw "Native UI section cards are missing a visual hierarchy requirement: $cardLayerRequirement"
    }
}
if ($sectionCard -match 'VerticalAlignment\s*=\s*VerticalAlignment\.Stretch') {
    throw 'Native UI section cards must not stretch short content into large empty blocks.'
}
if (-not $appCode.Contains('private Border CreatePageHeader(string title)') -or
    @([regex]::Matches($appCode, 'CreatePageHeader\("')).Count -ne 7) {
    throw 'Each navigation page must use the compact, single-title page header.'
}
$workspaceCard = [regex]::Match($appCode,
    'private Border CreateWorkspaceCard\([\s\S]+?^    \}',
    [Text.RegularExpressions.RegexOptions]::Multiline).Value
foreach ($requirement in @('DgpSurfaceBrush', 'DgpDashboardLineBrush', 'new CornerRadius(18)', 'new Thickness(20)')) {
    if (-not $workspaceCard.Contains($requirement)) {
        throw "Workspace cards must match the dashboard surface and spacing: $requirement"
    }
}
$pageHeader = [regex]::Match($appCode,
    'private Border CreatePageHeader\([\s\S]+?^    \}',
    [Text.RegularExpressions.RegexOptions]::Multiline).Value
if ($pageHeader.Contains('BorderThickness') -or $pageHeader.Contains('Background =')) {
    throw 'Page headers must use the same unboxed title as the dashboard.'
}
foreach ($menuTitle in @('文件', '编辑', '查看', '设置')) {
    if (-not $appCode.Contains('CreateTopMenu("' + $menuTitle + '")')) {
        throw "Native UI menu is missing: $menuTitle"
    }
}
$standardDesktopMenuItems = @(
    '历史会话', '报告导出', '退出',
    '重点对象与递归设置', '排除规则设置', '系统设置',
    '仪表盘', '事件时间线', '风险分析', '资产差异', '刷新当前页面', '展开或收起导航栏',
    '会话保留设置', '敏感字段与报告设置', '关于 Desktop Guard Pro')
foreach ($menuItem in $standardDesktopMenuItems) {
    if (-not $appCode.Contains('"' + $menuItem + '"')) {
        throw "Native UI standard desktop menu item is missing: $menuItem"
    }
}
if (-not $appCode.Contains('KeyboardAccelerators.Add(')) {
    throw 'Native UI standard desktop menu shortcuts are missing.'
}
foreach ($globalStatusRequirement in @(
    'private TextBlock? globalServiceStatusText;',
    'private TextBlock? globalProtectionStatusText;',
    'CreateGlobalStatusBadge(',
    '"服务：连接中", out globalServiceStatusText',
    '"保护：未开启", out globalProtectionStatusText',
    'UpdateGlobalServiceStatus(',
    'UpdateGlobalProtectionStatus(',
    'TimeSpan.FromSeconds(5)',
    'MinWidth = 112',
    'TextWrapping = TextWrapping.NoWrap',
    'var commandRow = new Grid',
    'Grid.SetColumn(globalStatusPanel, 1);',
    'Grid.SetColumnSpan(commandRow, 3);',
    'Grid.SetRow(commandRow, 1);',
    'commandRow.Children.Add(menuBar);',
    'commandRow.Children.Add(globalStatusPanel);',
    'titleBar.Children.Add(commandRow);')) {
    if (-not $appCode.Contains($globalStatusRequirement)) {
        throw "Native UI global status requirement is missing: $globalStatusRequirement"
    }
}
if ($appCode.Contains('dragRegion.Children.Add(globalStatusPanel);')) {
    throw 'Native UI global status panel must not share the title-bar drag region.'
}
foreach ($monitoringResolutionRequirement in @(
    'target.ConfiguredPath ?? target.Path',
    '"重解析已处理"',
    '"路径可用"')) {
    if (-not $appCode.Contains($monitoringResolutionRequirement)) {
        throw "Native UI monitoring resolution status is missing: $monitoringResolutionRequirement"
    }
}
# Dashboard actions remain connected while configuration is progressively disclosed.
foreach ($dashboardLayoutRequirement in @(
    'CreateDashboardTask("电脑保护"',
    'CreateDashboardTask("临时锁定键鼠"',
    'CreateDashboardExpander("启动选项与采集详情"',
    'CreateDashboardExpander("监控范围与排除规则"',
    'CreateBalancedDashboardColumns(',
    'IsExpanded = false',
    'RevealDashboardScope();',
    'dashboardScopeExpander.IsExpanded = true;',
    'Grid.SetRow(footer, 2);',
    'Content = "高级设置与设备"',
    'var inputManagementFlyout = new Flyout',
    'protectionButton.Click += async (_, _) => await performProtectionActionAsync();',
    'startTemporaryInputControlButton.Click += async (_, _) => await StartTemporaryInputControlAsync();',
    'stopTemporaryInputControlButton.Click += async (_, _) => await StopTemporaryInputControlAsync();',
    'saveDirectoriesButton.Click += async (_, _) => await saveDirectoriesAsync();',
    'saveExclusionsButton.Click += async (_, _) => await SaveExclusionsAsync();',
    'Children = { sessionName, sessionStartPreviewStatus, openModeSettingsButton }',
    'Children = { startTemporaryInputControlButton, stopTemporaryInputControlButton }',
    'temporaryInputUnlockHint,',
    '(historyShortcut, "history"), (auditShortcut, "audit"), (reportShortcut, "reports")')) {
    if (-not $appCode.Contains($dashboardLayoutRequirement)) {
        throw "Native UI dashboard action or disclosure requirement is missing: $dashboardLayoutRequirement"
    }
}
foreach ($focusMethod in @('FocusDirectories', 'FocusExclusions')) {
    $focusCode = [regex]::Match($appCode, ('void ' + $focusMethod + '\(\)[\s\S]+?^        \}'),
        [Text.RegularExpressions.RegexOptions]::Multiline).Value
    if (-not $focusCode.Contains('RevealDashboardScope();')) {
        throw "Menu entry must reveal its collapsed editor: $focusMethod"
    }
}
if ($appCode.Contains('CreateDashboardCard("快速操作"') -or
    $appCode.Contains('CreateDashboardCard("运行信息"')) {
    throw 'Dashboard must not restore nested instruction cards.'
}
foreach ($dashboardBrush in @('DgpDashboardLineBrush', 'DgpTealSurfaceBrush', 'DgpTealBrush',
    'DgpAmberSurfaceBrush', 'DgpErrorSurfaceBrush')) {
    if (-not $appXaml.Contains('x:Key="' + $dashboardBrush + '"')) {
        throw "Dashboard semantic brush is missing: $dashboardBrush"
    }
}
foreach ($emptyHeartbeatRequirement in @(
    'runtime.ObservedUtc is null || runtime.ObservedUtc.Value <= DateTimeOffset.UnixEpoch',
    '"暂无运行记录"')) {
    if (-not $appCode.Contains($emptyHeartbeatRequirement)) {
        throw "Native UI empty-heartbeat handling is missing: $emptyHeartbeatRequirement"
    }
}
foreach ($monitoringPolicyRequirement in @(
    'private TextBlock? monitoringPolicySummary;',
    'private MonitoringPolicyInfo currentMonitoringPolicy = MonitoringPolicyInfo.CreateDefault();',
    'private void UpdateMonitoringPolicySummary()',
    'CreatePolicyCapability(',
    'CreateWorkspaceButton("配置详情")',
    'ShowFilePolicyDetailsAsync',
    'ShowProcessPolicyDetailsAsync',
    'ShowSystemPolicyDetailsAsync',
    'ShowDevicePolicyDetailsAsync',
    'ShowUserSessionPolicyDetailsAsync',
    'ShowInputShieldPolicyDetailsAsync',
    'LoadInputShieldManagementAsync',
    'ShowInputShieldDevicesAsync',
    'ConfigureInputShieldCredentialsAsync',
    'PrimaryButtonText = "保存配置"',
    'RequestedTheme = ElementTheme.Light',
    '记录文件创建',
    '进程与软件快照间隔',
    'Windows 安全日志',
    '记录设备接入与重新接入',
    '记录窗口标题',
    '临时输入控制设置',
    '持续到验证解锁',
    'Ctrl+Alt+Space',
    '查看输入设备',
    '设置本地密码',
    '启用采集缺口风险规则',
    '已启用 {enabledCount}/5 项审计能力',
    '配置详情中可调整事件类型',
    '修改用于该模式的新会话',
    '临时键鼠控制在仪表盘独立运行')) {
    if (-not $appCode.Contains($monitoringPolicyRequirement)) {
        throw "Native UI detailed monitoring-policy requirement is missing: $monitoringPolicyRequirement"
    }
}
$monitoringPolicyDialog = [regex]::Match(
    $appCode,
    'private async Task<bool> ShowMonitoringPolicyDialogAsync[\s\S]+?^    \}',
    [Text.RegularExpressions.RegexOptions]::Multiline).Value
if (-not $monitoringPolicyDialog -or -not $monitoringPolicyDialog.Contains('RequestedTheme = ElementTheme.Light')) {
    throw 'Monitoring policy dialogs must use the light application theme.'
}
if (-not $appCode.Contains('Padding = new Thickness(8, 0, 8, 0)') -or
    -not $appCode.Contains('MinWidth = 40')) {
    throw 'Top menu items must use compact dimensions in narrow windows.'
}

$navigationSection = [regex]::Match(
    $appCode,
    'navigation\.MenuItems\.Add[\s\S]+?navigation\.SelectionChanged',
    [Text.RegularExpressions.RegexOptions]::CultureInvariant).Value
$icons = @([regex]::Matches($navigationSection, 'Icon\s*=\s*new SymbolIcon\(Symbol\.([A-Za-z0-9_]+)\)') |
    ForEach-Object { $_.Groups[1].Value })
if ($icons.Count -ne 7 -or @($icons | Select-Object -Unique).Count -ne 7) {
    throw "Expected seven distinct navigation icons; found: $($icons -join ', ')"
}

$analysisCode = Get-Content (Join-Path $projectRoot 'frontend\DesktopGuardPro.Native\App.Analysis.cs') -Raw
foreach ($requirement in @('CreateWorkspaceButton("打开会话", true)', 'CreateAnalysisSessionBar(', 'ResolveAnalysisSessionAsync(client)', 'OpenRiskEvidenceAsync', 'ReportSequenceRange.TryParse')) {
    if (-not $appCode.Contains($requirement)) { throw "Cross-page session wiring missing: $requirement" }
}
foreach ($method in @('LoadTimelineAsync', 'LoadRiskAsync', 'LoadAssetDifferencesAsync', 'ExportReportAsync')) {
    $body = [regex]::Match($appCode, ('private async Task ' + $method + '[\s\S]+?^    \}'), [Text.RegularExpressions.RegexOptions]::Multiline).Value
    if (-not $body -or -not $body.Contains('ResolveAnalysisSessionAsync(client)') -or $body.Contains('health.Session.Id')) { throw "$method must query the selected history session" }
}
if (-not $analysisCode.Contains('analysisWorkspace.IsCurrent') -and -not $appCode.Contains('analysisWorkspace.IsCurrent')) { throw 'Missing stale response protection' }
Write-Output 'Native UI shell and cross-page session wiring checks passed.'
