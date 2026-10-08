using DesktopGuardPro.Native;

static void Check(bool condition, string message)
{
    if (!condition) throw new Exception(message);
}

var context = new AnalysisWorkspace();
context.Select(new AnalysisSession("A", "旧会话", "completed"));
var revision = context.Revision;
Check(context.IsCurrent("A", revision), "selected history must remain queryable without health");
context.Select(new AnalysisSession("B", "新会话", "active"));
Check(!context.IsCurrent("A", revision), "late response from A must not replace B");
context.Select(new AnalysisSession("A", "旧会话", "completed"));
Check(!context.IsCurrent("A", revision), "A-B-A must reject the first A response");
var paging = new TimelinePagingState();
Check(paging.Begin("A|file", false) == "", "initial page");
paging.Complete("next");
Check(paging.Begin("A|file", true) == "next", "same query can append");
Check(paging.Begin("A|device", true) == "", "changed filter must restart pagination");
paging.Complete("next");
Check(paging.Begin("B|device", true) == "", "changed session must restart pagination");
Check(ReportSequenceRange.TryParse("1", "100000", out _, out _, out _), "maximum supported range");
Check(!ReportSequenceRange.TryParse("1", "100001", out _, out _, out _), "oversized range rejected before export");
Check(!ReportSequenceRange.TryParse("8", "7", out _, out _, out _), "reversed range rejected");
Check(!ReportSequenceRange.TryParse("abc", "9", out _, out _, out _), "invalid input rejected");
Check(ReportSequenceRange.TryParse("", "", out var from, out var to, out _) && from is null && to is null, "whole session export");
Console.WriteLine("PASS: historical selection, stale responses, pagination, report ranges");
var drafts = new ModeDraftStore<string>();
drafts.Keep("standard", "edited", "saved");
Check(drafts.Read("standard", "saved") == "edited", "mode change retains draft");
Check(drafts.Read("strict", "strict-saved") == "strict-saved", "drafts isolated by mode");
drafts.Keep("standard", "edited", "edited");
Check(drafts.Read("standard", "new-server-value") == "new-server-value", "successful save clears draft");
Console.WriteLine("PASS: per-mode drafts and successful save");

var legacyInputPolicy = InputShieldPolicyInfo.CreateDefault() with
{
    BlockPhysicalKeyboard = false, BlockPhysicalMouse = false, BlockPointerMovement = false,
    InjectedInputMode = "strict", CredentialMode = "windows", RestoreAfterRestart = true,
    UnlockKeyCode = 85, UnlockTrigger = "tap", UnlockTapCount = 6,
};
var inputPolicy = legacyInputPolicy.ForTemporaryControl();
Check(inputPolicy.CredentialMode == "local" && inputPolicy.AllowRecoveryCode,
    "saved local password and recovery must work without selecting another credential mode");
Check(inputPolicy.BlockPhysicalKeyboard && inputPolicy.BlockPhysicalMouse && inputPolicy.BlockPointerMovement &&
    inputPolicy.InjectedInputMode == "compatible", "temporary control must match DeskGuard physical/injected behavior");
Check(!inputPolicy.RestoreAfterRestart && inputPolicy.UnlockAction == "suspend", "unlock ends temporary control");
Check(inputPolicy.UnlockKeyCode == 85 && inputPolicy.UnlockTrigger == "tap" && inputPolicy.UnlockTapCount == 6,
    "configured unlock gesture must survive migration");
Console.WriteLine("PASS: DeskGuard temporary input policy and saved password selection");

var uiDefaults = new UiPreferences();
Check(uiDefaults.AutoRefresh && !uiDefaults.CloseToTray && !uiDefaults.StartInTray,
    "existing users retain visible startup, real close, and automatic health refresh");
var trayPreferences = uiDefaults with { StartInTray = true, CloseToTray = true };
Check(trayPreferences.ShouldStartInTray(new[] { "--autostart" }, true), "autostart can hide to available tray");
Check(!trayPreferences.ShouldStartInTray(Array.Empty<string>(), true), "manual launch must remain visible");
Check(!trayPreferences.ShouldStartInTray(new[] { "--autostart" }, false), "failed tray creation must keep startup visible");
Check(trayPreferences.ShouldHideOnClose(false, true), "system close can hide when configured");
Check(!trayPreferences.ShouldHideOnClose(true, true), "explicit menu exit bypasses close-to-tray");
Check(!trayPreferences.ShouldHideOnClose(false, false), "failed tray must never trap a hidden window");
Check((uiDefaults with { DefaultReportFormat = "unknown" }).Normalize().DefaultReportFormat == "html",
    "unsupported persisted report format falls back safely");

var startupStore = new FakeUiStartupStore();
var startupExecutable = Path.Combine(Path.GetFullPath("."), "应用 空格", "desktop-guard-ui.exe");
var startupRegistration = new UiStartupRegistration(startupStore, startupExecutable);
Check(!startupRegistration.IsEnabled, "absent UI startup entry is off");
startupRegistration.SetEnabled(true);
Check(startupStore.Command == $"\"{startupExecutable}\" --autostart", "startup command quotes unicode and spaces");
startupStore.Command = "\"C:\\Old install\\desktop-guard-ui.exe\" --autostart";
Check(startupRegistration.IsEnabled, "old installation startup entry can be disabled or refreshed");
startupRegistration.SetEnabled(true);
Check(startupStore.Command == UiStartupRegistration.CreateCommand(startupExecutable), "enabling refreshes relocated executable");
startupRegistration.SetEnabled(false);
Check(startupStore.Command is null && !startupRegistration.IsEnabled, "disabling removes only UI startup registration");
startupStore.RejectWrites = true;
try { startupRegistration.SetEnabled(true); throw new Exception("failed registry write was accepted"); }
catch (UnauthorizedAccessException) { }
Check(!startupRegistration.IsEnabled, "denied startup write preserves previous state");
startupStore.RejectWrites = false;
startupStore.IgnoreWrites = true;
try { startupRegistration.SetEnabled(true); throw new Exception("ineffective startup write was accepted"); }
catch (IOException) { }
foreach (var badStartupPath in new[] { "relative.exe", "", startupExecutable + "\" --injected" })
{
    try { _ = UiStartupRegistration.CreateCommand(badStartupPath); throw new Exception("unsafe startup path accepted"); }
    catch (ArgumentException) { }
}
startupStore.IgnoreWrites = false;
startupRegistration.SetEnabled(true);
startupStore.RejectDeletes = true;
try { startupRegistration.SetEnabled(false); throw new Exception("denied startup deletion was accepted"); }
catch (UnauthorizedAccessException) { }
Check(startupRegistration.IsEnabled, "denied deletion retains enabled registration");

var preferencesTestRoot = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory,
    "../../../../../dist/settings-tests", Guid.NewGuid().ToString("N")));
try
{
    var preferencesPath = Path.Combine(preferencesTestRoot, "ui-preferences.json");
    var preferencesStore = new UiPreferencesStore(preferencesPath);
    Check(preferencesStore.Load() == uiDefaults, "missing preferences use product defaults");
    var savedPreferences = trayPreferences with { AutoRefresh = false, DefaultReportFormat = "markdown" };
    preferencesStore.Save(savedPreferences);
    Check(new UiPreferencesStore(preferencesPath).Load() == savedPreferences, "preferences survive application restart");
    preferencesStore.Save(savedPreferences with { DefaultReportFormat = "json" });
    Check(preferencesStore.Load().DefaultReportFormat == "json", "existing preference file is atomically replaced");
    using (var lockedPreferences = new FileStream(preferencesPath, FileMode.Open, FileAccess.Read, FileShare.None))
    {
        try { preferencesStore.Save(uiDefaults); throw new Exception("locked preference file was overwritten"); }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException) { }
    }
    Check(preferencesStore.Load().DefaultReportFormat == "json", "failed save preserves previous preferences");
    Check(Directory.GetFiles(preferencesTestRoot).Length == 1, "preference save leaves no temporary file");
    File.WriteAllText(preferencesPath, "{}");
    Check(preferencesStore.Load() == uiDefaults, "missing fields retain defaults for older preference files");
    File.WriteAllText(preferencesPath, "{broken");
    try { _ = preferencesStore.Load(); throw new Exception("corrupt preferences were accepted"); }
    catch (System.Text.Json.JsonException) { }
}
finally
{
    if (Directory.Exists(preferencesTestRoot)) Directory.Delete(preferencesTestRoot, recursive: true);
}
Console.WriteLine("PASS: UI startup commands, persisted preferences, tray safety, and explicit exit");

var startupChecks = 0;
var startupDelays = 0;
Check(await StartupServiceConnection.TryConnectAsync(() => Task.FromResult(++startupChecks == 3),
    CancellationToken.None, delay: (interval, token) =>
    {
        Check(interval == TimeSpan.FromSeconds(5), "startup retry keeps the expected interval");
        startupDelays++;
        return Task.CompletedTask;
    }), "startup waits for a service that becomes available later");
Check(startupChecks == 3 && startupDelays == 2, "successful startup stops further retries");
startupChecks = 0;
startupDelays = 0;
Check(!await StartupServiceConnection.TryConnectAsync(() => { startupChecks++; return Task.FromResult(false); },
    CancellationToken.None, delay: (_, _) => { startupDelays++; return Task.CompletedTask; }),
    "unavailable service does not leave startup waiting forever");
Check(startupChecks == 6 && startupDelays == 5, "startup retries have a finite limit");
using (var startupCancellation = new CancellationTokenSource())
{
    startupChecks = 0;
    try
    {
        await StartupServiceConnection.TryConnectAsync(() => { startupChecks++; return Task.FromResult(false); },
            startupCancellation.Token, delay: (_, _) =>
            {
                startupCancellation.Cancel();
                return Task.CompletedTask;
            });
        throw new Exception("closed window did not cancel startup retry");
    }
    catch (OperationCanceledException) { }
    Check(startupChecks == 1, "closing the window stops pending startup work");
}
using (var cancellationDuringConnection = new CancellationTokenSource())
{
    try
    {
        await StartupServiceConnection.TryConnectAsync(() =>
        {
            cancellationDuringConnection.Cancel();
            return Task.FromResult(true);
        }, cancellationDuringConnection.Token);
        throw new Exception("closed window proceeded to load startup views");
    }
    catch (OperationCanceledException) { }
}
Console.WriteLine("PASS: boot service readiness, bounded startup retries, and cancellation");

sealed class FakeUiStartupStore : IUiStartupStore
{
    public string? Command { get; set; }
    public bool RejectWrites { get; set; }
    public bool IgnoreWrites { get; set; }
    public bool RejectDeletes { get; set; }
    public string? ReadCommand() => Command;
    public void WriteCommand(string command)
    {
        if (RejectWrites) throw new UnauthorizedAccessException("Mock registry denied.");
        if (!IgnoreWrites) Command = command;
    }
    public void DeleteCommand()
    {
        if (RejectDeletes) throw new UnauthorizedAccessException("Mock registry deletion denied.");
        Command = null;
    }
}
