using System.Text.Json;

namespace DesktopGuardPro.Native;

public sealed record UiPreferences(
    bool StartInTray = false,
    bool CloseToTray = false,
    bool AutoRefresh = true,
    string DefaultReportFormat = "html")
{
    public UiPreferences Normalize() => this with
    {
        DefaultReportFormat = DefaultReportFormat is "html" or "markdown" or "json"
            ? DefaultReportFormat : "html",
    };

    public bool ShouldStartInTray(IEnumerable<string> arguments, bool trayAvailable) =>
        StartInTray && trayAvailable && arguments.Contains("--autostart", StringComparer.Ordinal);

    public bool ShouldHideOnClose(bool exitRequested, bool trayAvailable) =>
        CloseToTray && !exitRequested && trayAvailable;
}

public sealed class UiPreferencesStore(string path)
{
    public UiPreferences Load() => File.Exists(path)
        ? (JsonSerializer.Deserialize<UiPreferences>(File.ReadAllText(path))
            ?? throw new JsonException("界面偏好文件为空。")).Normalize()
        : new UiPreferences();

    public void Save(UiPreferences preferences)
    {
        var directory = Path.GetDirectoryName(Path.GetFullPath(path))!;
        Directory.CreateDirectory(directory);
        var temporaryPath = Path.Combine(directory, $".ui-preferences-{Guid.NewGuid():N}.tmp");
        try
        {
            File.WriteAllText(temporaryPath, JsonSerializer.Serialize(preferences.Normalize(),
                new JsonSerializerOptions { WriteIndented = true }));
            File.Move(temporaryPath, path, overwrite: true);
        }
        finally
        {
            if (File.Exists(temporaryPath)) File.Delete(temporaryPath);
        }
    }
}

public interface IUiStartupStore
{
    string? ReadCommand();
    void WriteCommand(string command);
    void DeleteCommand();
}

public static class StartupServiceConnection
{
    public static async Task<bool> TryConnectAsync(Func<Task<bool>> connect,
        CancellationToken cancellationToken, int maximumAttempts = 6,
        Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        if (maximumAttempts < 1) throw new ArgumentOutOfRangeException(nameof(maximumAttempts));
        delay ??= Task.Delay;
        for (var attempt = 0; attempt < maximumAttempts; attempt++)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var connected = await connect();
            cancellationToken.ThrowIfCancellationRequested();
            if (connected) return true;
            if (attempt + 1 < maximumAttempts)
                await delay(TimeSpan.FromSeconds(5), cancellationToken);
        }
        return false;
    }
}

public sealed class UiStartupRegistration(IUiStartupStore store, string executablePath)
{
    public static string CreateCommand(string executablePath)
    {
        if (string.IsNullOrWhiteSpace(executablePath) || executablePath.Contains('"') ||
            executablePath.Contains('\r') || executablePath.Contains('\n') ||
            !Path.IsPathFullyQualified(executablePath))
            throw new ArgumentException("自启动需要有效的程序完整路径。", nameof(executablePath));
        return $"\"{Path.GetFullPath(executablePath)}\" --autostart";
    }

    // A registration from an earlier installation still counts as enabled. Enabling it
    // again refreshes the executable path; disabling only removes our named UI value.
    public bool IsEnabled => !string.IsNullOrWhiteSpace(store.ReadCommand());

    public void SetEnabled(bool enabled)
    {
        if (enabled) store.WriteCommand(CreateCommand(executablePath));
        else store.DeleteCommand();
        if (IsEnabled != enabled)
            throw new IOException("自启动设置写入后核对失败。");
    }
}
