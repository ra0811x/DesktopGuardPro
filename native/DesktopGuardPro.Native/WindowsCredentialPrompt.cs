using System.Runtime.InteropServices;
using System.Text;

namespace DesktopGuardPro.Native;

internal sealed class SystemCredentials : IDisposable
{
    public SystemCredentials(string userName, string domain, byte[] password)
    {
        UserName = userName;
        Domain = domain;
        Password = password;
    }

    public string UserName { get; }

    public string Domain { get; }

    public byte[] Password { get; private set; }

    public void Dispose()
    {
        Array.Clear(Password);
        Password = [];
    }
}

internal static class WindowsCredentialPrompt
{
    private const int ErrorCancelled = 1223;
    private const int ErrorInsufficientBuffer = 122;
    private const uint CredUiWinPromptFlags = 0;

    public static SystemCredentials PromptForEndProtection()
    {
        var caption = "Desktop Guard Pro";
        var message = "结束保护需要确认 Windows 系统凭据。";
        var info = new CredUiInfo
        {
            Size = Marshal.SizeOf<CredUiInfo>(),
            CaptionText = caption,
            MessageText = message,
        };
        uint authenticationPackage = 0;
        var save = false;
        var result = CredUIPromptForWindowsCredentials(
            ref info,
            0,
            ref authenticationPackage,
            IntPtr.Zero,
            0,
            out var authenticationBuffer,
            out var authenticationBufferSize,
            ref save,
            CredUiWinPromptFlags);
        if (result == ErrorCancelled)
        {
            throw new OperationCanceledException("已取消 Windows 系统凭据确认。");
        }
        if (result != 0)
        {
            throw new InvalidOperationException($"无法打开 Windows 系统凭据窗口：{result}。");
        }
        try
        {
            return Unpack(authenticationBuffer, authenticationBufferSize);
        }
        finally
        {
            var buffer = new byte[checked((int)authenticationBufferSize)];
            Marshal.Copy(buffer, 0, authenticationBuffer, buffer.Length);
            Array.Clear(buffer);
            Marshal.FreeCoTaskMem(authenticationBuffer);
        }
    }

    private static SystemCredentials Unpack(IntPtr authenticationBuffer, uint authenticationBufferSize)
    {
        var userLength = 0;
        var domainLength = 0;
        var passwordLength = 0;
        _ = CredUnPackAuthenticationBuffer(
            0,
            authenticationBuffer,
            authenticationBufferSize,
            null,
            ref userLength,
            null,
            ref domainLength,
            null,
            ref passwordLength);
        if (Marshal.GetLastWin32Error() != ErrorInsufficientBuffer || userLength == 0 || passwordLength == 0)
        {
            throw new InvalidOperationException("Windows 系统凭据无效。");
        }

        var user = new StringBuilder(userLength);
        var domain = new StringBuilder(Math.Max(domainLength, 1));
        var password = new StringBuilder(passwordLength);
        if (!CredUnPackAuthenticationBuffer(
                0,
                authenticationBuffer,
                authenticationBufferSize,
                user,
                ref userLength,
                domain,
                ref domainLength,
                password,
                ref passwordLength))
        {
            throw new InvalidOperationException("无法读取 Windows 系统凭据。");
        }
        try
        {
            var passwordBytes = Encoding.UTF8.GetBytes(password.ToString());
            if (user.Length == 0 || passwordBytes.Length == 0)
            {
                Array.Clear(passwordBytes);
                throw new InvalidOperationException("Windows 系统凭据无效。");
            }
            return new SystemCredentials(user.ToString(), domain.ToString(), passwordBytes);
        }
        finally
        {
            user.Clear();
            domain.Clear();
            password.Clear();
        }
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct CredUiInfo
    {
        public int Size;
        public IntPtr Parent;
        public string? MessageText;
        public string? CaptionText;
        public IntPtr Banner;
    }

    [DllImport("credui.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern int CredUIPromptForWindowsCredentials(
        ref CredUiInfo uiInfo,
        uint authenticationError,
        ref uint authenticationPackage,
        IntPtr inputAuthenticationBuffer,
        uint inputAuthenticationBufferSize,
        out IntPtr outputAuthenticationBuffer,
        out uint outputAuthenticationBufferSize,
        ref bool save,
        uint flags);

    [DllImport("credui.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CredUnPackAuthenticationBuffer(
        uint flags,
        IntPtr authenticationBuffer,
        uint authenticationBufferSize,
        StringBuilder? userName,
        ref int maxUserName,
        StringBuilder? domainName,
        ref int maxDomainName,
        StringBuilder? password,
        ref int maxPassword);
}
