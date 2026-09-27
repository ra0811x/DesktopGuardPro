using System.Runtime.InteropServices;

namespace DesktopGuardPro.Native;

internal sealed class NativeTrayIcon : IDisposable
{
    private const uint NIMAdd = 0x00000000;
    private const uint NIMModify = 0x00000001;
    private const uint NIMDelete = 0x00000002;
    private const uint NIFIcon = 0x00000002;
    private const uint NIFTip = 0x00000004;
    private IntPtr icon;
    private NotifyIconData data;

    private NativeTrayIcon(IntPtr icon, NotifyIconData data)
    {
        this.icon = icon;
        this.data = data;
    }

    public static NativeTrayIcon? Create(Microsoft.UI.Xaml.Window window, string toolTip)
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
            Flags = NIFIcon | NIFTip,
            ToolTip = toolTip,
        };
        if (!ShellNotifyIcon(NIMAdd, ref data))
        {
            _ = DestroyIcon(icon);
            return null;
        }
        return new NativeTrayIcon(icon, data);
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
    }

	public void UpdateToolTip(string toolTip)
	{
		if (icon == IntPtr.Zero)
		{
			return;
		}
		data.Flags = NIFIcon | NIFTip;
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
}
