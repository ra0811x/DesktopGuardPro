using Microsoft.Win32;

namespace DesktopGuardPro.Native;

internal sealed class UiStartupRegistry : IUiStartupStore
{
    internal const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    internal const string ValueName = "DesktopGuardProUI";

    public string? ReadCommand()
    {
        using var key = Registry.CurrentUser.OpenSubKey(RunKey, writable: false);
        return key?.GetValue(ValueName) as string;
    }

    public void WriteCommand(string command)
    {
        using var key = Registry.CurrentUser.CreateSubKey(RunKey, writable: true)
            ?? throw new IOException("无法打开当前用户自启动设置。");
        key.SetValue(ValueName, command, RegistryValueKind.String);
    }

    public void DeleteCommand()
    {
        using var key = Registry.CurrentUser.OpenSubKey(RunKey, writable: true);
        key?.DeleteValue(ValueName, throwOnMissingValue: false);
    }
}
