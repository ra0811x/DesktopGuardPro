using System.Runtime.InteropServices;

namespace DesktopGuardPro.Native;

internal sealed class NativeTrayIcon : IDisposable
{
    private const uint NIMAdd = 0x00000000;
    private const uint NIMModify = 0x00000001;
    private const uint NIMDelete = 0x00000002;
    private const uint NIFIcon = 0x00000002;
    private const uint NIFTip = 0x00000004;
    private const uint NIFMessage = 0x00000001;
    private const uint TrayCallbackMessage = 0x8001;
    private static readonly uint TaskbarCreatedMessage = RegisterWindowMessage("TaskbarCreated");
    private IntPtr icon;
    private NotifyIconData data;
    private readonly Action restoreWindow;
    private readonly Action exitApplication;
    public bool IsAvailable { get; private set; }

    private NativeTrayIcon(IntPtr icon, NotifyIconData data, Action restoreWindow, Action exitApplication)
    {
        this.icon = icon;
        this.data = data;
        this.restoreWindow = restoreWindow;
        this.exitApplication = exitApplication;
        IsAvailable = true;
    }

    public static NativeTrayIcon? Create(Microsoft.UI.Xaml.Window window, string toolTip,
        Action restoreWindow, Action exitApplication)
    {
        var executable = Environment.ProcessPath;
        if (string.IsNullOrWhiteSpace(executable))
        {
            return null;
        }
        var icon = ExtractIcon(IntPtr.Zero, executable, 0);
        if (icon == IntPtr.Zero)
        {
            return null;
        }
        var data = new NotifyIconData
        {
            Size = Marshal.SizeOf<NotifyIconData>(),
            WindowHandle = WinRT.Interop.WindowNative.GetWindowHandle(window),
            Icon = icon,
            Flags = NIFMessage | NIFIcon | NIFTip,
            CallbackMessage = TrayCallbackMessage,
            ToolTip = toolTip,
        };
        if (!ShellNotifyIcon(NIMAdd, ref data))
        {
            _ = DestroyIcon(icon);
            return null;
        }
        return new NativeTrayIcon(icon, data, restoreWindow, exitApplication);
    }

    public bool TryHandleMessage(uint message, IntPtr lParam)
    {
        if (icon == IntPtr.Zero) return false;
        if (TaskbarCreatedMessage != 0 && message == TaskbarCreatedMessage)
        {
            IsAvailable = ShellNotifyIcon(NIMAdd, ref data);
            if (!IsAvailable) restoreWindow();
            return true;
        }
        if (message != TrayCallbackMessage) return false;
        switch ((uint)lParam.ToInt64())
        {
            case 0x0202: // WM_LBUTTONUP
            case 0x0203: // WM_LBUTTONDBLCLK
            case 0x0400: // NIN_SELECT
            case 0x0401: // NIN_KEYSELECT
                restoreWindow();
                break;
            case 0x0205: // WM_RBUTTONUP
            case 0x007B: // WM_CONTEXTMENU
                ShowContextMenu();
                break;
        }
        return true;
    }

    private void ShowContextMenu()
    {
        var menu = CreatePopupMenu();
        if (menu == IntPtr.Zero || !GetCursorPos(out var point))
        {
            if (menu != IntPtr.Zero) _ = DestroyMenu(menu);
            restoreWindow();
            return;
        }
        try
        {
            _ = AppendMenu(menu, 0, 1, "打开 Desktop Guard Pro");
            _ = AppendMenu(menu, 0x0800, 0, null);
            _ = AppendMenu(menu, 0, 2, "退出界面");
            _ = SetForegroundWindow(data.WindowHandle);
            var command = TrackPopupMenu(menu, 0x0100 | 0x0002 | 0x0080,
                point.X, point.Y, 0, data.WindowHandle, IntPtr.Zero);
            _ = PostMessage(data.WindowHandle, 0, IntPtr.Zero, IntPtr.Zero);
            if (command == 1) restoreWindow();
            else if (command == 2) exitApplication();
        }
        finally { _ = DestroyMenu(menu); }
    }

    public void Dispose()
    {
        if (icon == IntPtr.Zero)
        {
            return;
        }
        _ = ShellNotifyIcon(NIMDelete, ref data);
        _ = DestroyIcon(icon);
        icon = IntPtr.Zero;
        IsAvailable = false;
    }

	public void UpdateToolTip(string toolTip)
	{
		if (icon == IntPtr.Zero)
		{
			return;
		}
		data.Flags = NIFMessage | NIFIcon | NIFTip;
		data.ToolTip = string.IsNullOrWhiteSpace(toolTip) ? "Desktop Guard Pro" : toolTip[..Math.Min(toolTip.Length, 127)];
		_ = ShellNotifyIcon(NIMModify, ref data);
	}

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NotifyIconData
    {
        public int Size;
        public IntPtr WindowHandle;
        public uint Id;
        public uint Flags;
        public uint CallbackMessage;
        public IntPtr Icon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
        public string ToolTip;
        public uint State;
        public uint StateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)]
        public string Info;
        public uint TimeoutOrVersion;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)]
        public string InfoTitle;
        public uint InfoFlags;
        public Guid GuidItem;
        public IntPtr BalloonIcon;
    }

    [DllImport("shell32.dll", EntryPoint = "Shell_NotifyIconW", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool ShellNotifyIcon(uint message, ref NotifyIconData data);

    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr ExtractIcon(IntPtr instance, string executablePath, uint index);

    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DestroyIcon(IntPtr icon);

    [StructLayout(LayoutKind.Sequential)]
    private struct NativePoint { public int X; public int Y; }

    [DllImport("user32.dll", EntryPoint = "RegisterWindowMessageW", CharSet = CharSet.Unicode)]
    private static extern uint RegisterWindowMessage(string message);
    [DllImport("user32.dll")]
    private static extern IntPtr CreatePopupMenu();
    [DllImport("user32.dll", EntryPoint = "AppendMenuW", CharSet = CharSet.Unicode)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool AppendMenu(IntPtr menu, uint flags, nuint id, string? text);
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DestroyMenu(IntPtr menu);
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetCursorPos(out NativePoint point);
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")]
    private static extern uint TrackPopupMenu(IntPtr menu, uint flags, int x, int y,
        int reserved, IntPtr window, IntPtr rectangle);
    [DllImport("user32.dll", EntryPoint = "PostMessageW")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool PostMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);
}
