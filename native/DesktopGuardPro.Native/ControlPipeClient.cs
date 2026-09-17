using System.Buffers.Binary;
using System.IO.Pipes;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace DesktopGuardPro.Native;

internal sealed class ControlPipeClient
{
    private const ushort ProtocolVersion = 1;
    private const string PipeName = "DesktopGuardPro.Control.v1";
    private const int MaximumMessageSize = 1024 * 1024;
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    public async Task<HealthResult> GetHealthAsync(CancellationToken cancellationToken)
    {
        return await CallAsync<HealthResult>(
            "health.get", new { }, "health.result", TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> GetDirectoryMonitoringAsync(
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.get", new { }, "directory_monitoring.result", TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> UpdateDirectoryMonitoringAsync(
        IReadOnlyList<MonitoringTargetInfo> targets,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { targets },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> UpdateMonitoringExclusionsAsync(
        IReadOnlyList<MonitoringExclusionInfo> exclusions,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { exclusions },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> UpdateInputActivityPreferenceAsync(
        bool inputActivityEnabled,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { inputActivityEnabled },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> UpdateWindowTitlePreferenceAsync(
        bool windowTitleEnabled,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { windowTitleEnabled },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> UpdateHighRiskShortcutsPreferenceAsync(
        bool highRiskShortcutsEnabled,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { highRiskShortcutsEnabled },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> UpdateMonitoringPolicyAsync(
        MonitoringPolicyInfo policy,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { monitoringPolicy = policy },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<DirectoryMonitoringResult> ResetMonitoringPolicyAsync(
        string resetMonitoringMode,
        CancellationToken cancellationToken)
    {
        return await CallAsync<DirectoryMonitoringResult>(
            "directory_monitoring.update",
            new { resetMonitoringMode },
            "directory_monitoring.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<InputShieldCredentialResult> GetInputShieldCredentialStatusAsync(
        CancellationToken cancellationToken)
    {
        return await CallAsync<InputShieldCredentialResult>(
            "input_shield.credential.get", new { }, "input_shield.credential.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<InputShieldCredentialResult> UpdateInputShieldCredentialsAsync(
        byte[] password,
        bool enableRecovery,
        CancellationToken cancellationToken)
    {
        return await CallAsync<InputShieldCredentialResult>(
            "input_shield.credential.update", new { password, enableRecovery }, "input_shield.credential.result",
            TimeSpan.FromSeconds(15), cancellationToken);
    }

    public async Task<InputShieldCredentialResult> DeleteInputShieldCredentialsAsync(
        CancellationToken cancellationToken)
    {
        return await CallAsync<InputShieldCredentialResult>(
            "input_shield.credential.delete", new { }, "input_shield.credential.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<InputShieldStatusResult> GetInputShieldStatusAsync(
        CancellationToken cancellationToken)
    {
        return await CallAsync<InputShieldStatusResult>(
            "input_shield.status.get", new { }, "input_shield.status.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<InputControlResult> GetInputControlAsync(CancellationToken cancellationToken)
    {
        return await CallAsync<InputControlResult>(
            "input_control.get", new { }, "input_control.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<InputControlResult> StartInputControlAsync(
        InputShieldPolicyInfo policy,
        int durationMinutes,
        bool indefinite,
        CancellationToken cancellationToken)
    {
        return await CallAsync<InputControlResult>(
            "input_control.start", new { policy, durationMinutes, indefinite }, "input_control.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<InputControlResult> StopInputControlAsync(CancellationToken cancellationToken)
    {
        return await CallAsync<InputControlResult>(
            "input_control.stop", new { }, "input_control.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<SessionStartPreviewResult> GetSessionStartPreviewAsync(
        string monitoringMode,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionStartPreviewResult>(
            "session.start_preview.get",
            new { monitoringMode },
            "session.start_preview.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<SessionBaselineReviewResult> GetSessionBaselineReviewAsync(
        string sessionId,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionBaselineReviewResult>(
            "session.baseline_review.get", new { sessionId }, "session.baseline_review.result",
            TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<SessionBaselineReviewResult> ResolveSessionBaselineReviewAsync(
        string sessionId,
        string resolution,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionBaselineReviewResult>(
            "session.baseline_review.resolve", new { sessionId, resolution }, "session.baseline_review.result",
            TimeSpan.FromSeconds(30), cancellationToken);
    }

    public async Task<SessionHistoryPage> ListSessionsAsync(
        string cursor,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionHistoryPage>(
            "session.list",
            new { cursor },
            "session.list.result",
            TimeSpan.FromSeconds(15),
            cancellationToken);
    }

    public async Task<SessionRetentionLockResult> UpdateSessionRetentionLockAsync(
        string sessionId,
        bool locked,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionRetentionLockResult>(
            "session.retention_lock.update",
            new { sessionId, locked },
            "session.retention_lock.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<TimelinePage> QueryTimelineAsync(
        string sessionId,
        string cursor,
        IReadOnlyCollection<string> categories,
        IReadOnlyCollection<string> severities,
        string user,
        string process,
        string path,
        DateTimeOffset? fromUtc,
        DateTimeOffset? toUtc,
        CancellationToken cancellationToken)
    {
        return await CallAsync<TimelinePage>(
            "timeline.query",
			new { sessionId, cursor, limit = 100, categories, severities,
				userSidHash = user.Length == 64 && user.All(Uri.IsHexDigit) ? user : string.Empty,
				user = user.Length == 64 && user.All(Uri.IsHexDigit) ? string.Empty : user,
				process, path, fromUtc, toUtc },
            "timeline.result",
            TimeSpan.FromSeconds(15),
            cancellationToken);
    }

    public async Task<RiskEvaluation> EvaluateRiskAsync(
        string sessionId,
        CancellationToken cancellationToken)
    {
        return await CallAsync<RiskEvaluation>(
            "risk.evaluate",
            new { sessionId },
            "risk.result",
            TimeSpan.FromSeconds(15),
            cancellationToken);
    }

    public async Task<RiskFindingStatusResult> UpdateRiskFindingStatusAsync(
        string sessionId,
        string findingId,
        string status,
        CancellationToken cancellationToken)
    {
        return await CallAsync<RiskFindingStatusResult>(
            "risk.finding_status.update",
            new { sessionId, findingId, status },
            "risk.finding_status.result",
            TimeSpan.FromSeconds(10),
            cancellationToken);
    }

    public async Task<AssetDifferenceResult> QueryAssetDifferencesAsync(
        string sessionId,
        IReadOnlyCollection<string> categories,
        CancellationToken cancellationToken)
    {
        return await CallAsync<AssetDifferenceResult>(
            "asset_difference.query",
            new { sessionId, categories },
            "asset_difference.result",
            TimeSpan.FromSeconds(15),
            cancellationToken);
    }

    public async Task<ReportResult> ExportReportAsync(
        string sessionId,
        string format,
        object redaction,
        ulong? fromSequence,
        ulong? toSequence,
        CancellationToken cancellationToken)
    {
        var result = await CallAsync<ReportResult>(
            "report.export",
            new { sessionId, format, redaction, fromSequence, toSequence },
            "report.result",
            TimeSpan.FromSeconds(30),
            cancellationToken);
        var content = result.Content ?? (result.Transfer is null
            ? throw new InvalidDataException("服务未提供报告内容。")
            : await ReadReportTransferAsync(result.Transfer, cancellationToken));
        VerifyReport(result with { Content = content, Transfer = null }, sessionId, format);
        return result with { Content = content, Transfer = null };
    }

    private static async Task<string> ReadReportTransferAsync(
        ReportTransfer transfer,
        CancellationToken cancellationToken)
    {
        if (transfer.Size < 1 || transfer.ChunkBytes < 1 || transfer.ExpiresUtc <= DateTimeOffset.UtcNow ||
            transfer.Token.Length < 32 || transfer.Sha256.Length != 64)
        {
            throw new InvalidDataException("服务返回的报告传输信息无效。");
        }
        using var content = new MemoryStream(transfer.Size);
        var offset = 0;
        for (var index = 0; index < transfer.Size / transfer.ChunkBytes + 2; index++)
        {
            var chunk = await CallAsync<ReportChunkResult>(
                "report.chunk.get", new { token = transfer.Token, offset }, "report.chunk.result",
                TimeSpan.FromSeconds(30), cancellationToken);
            if (chunk.Content.Length == 0 || chunk.NextOffset != offset + chunk.Content.Length ||
                chunk.NextOffset > transfer.Size || chunk.Done != (chunk.NextOffset == transfer.Size))
            {
                throw new InvalidDataException("服务返回的报告分块无效。");
            }
            await content.WriteAsync(chunk.Content, cancellationToken);
            offset = chunk.NextOffset;
            if (chunk.Done)
            {
                var bytes = content.ToArray();
                if (bytes.Length != transfer.Size || !Convert.ToHexString(SHA256.HashData(bytes)).Equals(transfer.Sha256, StringComparison.OrdinalIgnoreCase))
                {
                    throw new InvalidDataException("报告传输完整性校验失败。");
                }
                return new UTF8Encoding(false, true).GetString(bytes);
            }
        }
        throw new InvalidDataException("报告分块传输未完成。");
    }

    private static void VerifyReport(ReportResult result, string sessionId, string format)
    {
        if (result.Format != format || string.IsNullOrEmpty(result.Content) || result.Verification is null ||
            result.Verification.Algorithm != "SHA-256" || !result.Verification.IntegrityVerified ||
            result.Verification.SessionId != sessionId || result.Verification.MediaType != result.MediaType ||
            result.Verification.ReportGeneratedUtc != result.GeneratedUtc)
        {
            throw new InvalidDataException("服务返回的报告验证信息无效。");
        }
        var bytes = Encoding.UTF8.GetBytes(result.Content);
        if (bytes.Length != result.Verification.ReportSize ||
            !Convert.ToHexString(SHA256.HashData(bytes)).Equals(result.Verification.ReportSha256, StringComparison.OrdinalIgnoreCase))
        {
            throw new InvalidDataException("报告完整性校验失败。");
        }
    }

    public async Task<SessionResult> CreateSessionAsync(
        string id,
        string name,
	    string monitoringMode,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionResult>(
            "session.create", new { id, name, monitoringMode }, "session.result", TimeSpan.FromSeconds(3), cancellationToken);
    }

    public async Task<SessionResult> TransitionSessionAsync(
        string state,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionResult>(
            "session.transition", new { state }, "session.result", TimeSpan.FromSeconds(30), cancellationToken);
    }

    public async Task<EndVerificationChallenge> CreateEndVerificationChallengeAsync(
        CancellationToken cancellationToken)
    {
        return await CallAsync<EndVerificationChallenge>(
            "session.end_verification.create",
            new { },
            "session.end_verification.result",
            TimeSpan.FromSeconds(3),
            cancellationToken);
    }

    public async Task<SessionResult> EndSessionAsync(
        EndVerificationChallenge challenge,
        SystemCredentials credentials,
        CancellationToken cancellationToken)
    {
        return await CallAsync<SessionResult>(
            "session.transition",
            new
            {
                state = "finalizing",
                verification = new
                {
                    token = challenge.Token,
                    userName = credentials.UserName,
                    domain = credentials.Domain,
                    password = credentials.Password,
                },
            },
            "session.result",
            TimeSpan.FromSeconds(30),
            cancellationToken);
    }

    private static async Task<T> CallAsync<T>(
        string requestType,
        object requestPayload,
        string expectedResponseType,
        TimeSpan timeoutDuration,
        CancellationToken cancellationToken)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(timeoutDuration);
        using var pipe = new NamedPipeClientStream(
            ".", PipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
        await pipe.ConnectAsync(timeout.Token);

        var requestId = Guid.NewGuid().ToString("D");
        var request = new ControlMessage(
            ProtocolVersion,
            requestId,
            requestType,
            DateTimeOffset.UtcNow.Add(timeoutDuration),
            JsonSerializer.SerializeToElement(requestPayload, JsonOptions));
        await WriteMessageAsync(pipe, request, timeout.Token);

        var response = await ReadMessageAsync(pipe, timeout.Token);
        if (response.Version != ProtocolVersion || response.RequestId != requestId)
        {
            throw new InvalidDataException("服务返回了不匹配的协议消息。");
        }
        if (response.Type == "error")
        {
            var error = response.Payload.Deserialize<ServiceError>(JsonOptions);
            throw new InvalidOperationException(error?.Message ?? "服务拒绝了请求。");
        }
        if (response.Type != expectedResponseType)
        {
            throw new InvalidDataException("服务返回了意外的响应类型。");
        }
        return response.Payload.Deserialize<T>(JsonOptions)
            ?? throw new InvalidDataException("服务返回的响应数据无效。");
    }

    private static async Task WriteMessageAsync(
        Stream stream,
        ControlMessage message,
        CancellationToken cancellationToken)
    {
        var payload = JsonSerializer.SerializeToUtf8Bytes(message, JsonOptions);
        if (payload.Length == 0 || payload.Length > MaximumMessageSize)
        {
            throw new InvalidDataException("协议消息长度无效。");
        }
        var header = new byte[sizeof(uint)];
        BinaryPrimitives.WriteUInt32LittleEndian(header, (uint)payload.Length);
        await stream.WriteAsync(header, cancellationToken);
        await stream.WriteAsync(payload, cancellationToken);
        await stream.FlushAsync(cancellationToken);
    }

    private static async Task<ControlMessage> ReadMessageAsync(
        Stream stream,
        CancellationToken cancellationToken)
    {
        var header = new byte[sizeof(uint)];
        await stream.ReadExactlyAsync(header, cancellationToken);
        var length = BinaryPrimitives.ReadUInt32LittleEndian(header);
        if (length == 0 || length > MaximumMessageSize)
        {
            throw new InvalidDataException("协议消息长度无效。");
        }
        var payload = new byte[length];
        await stream.ReadExactlyAsync(payload, cancellationToken);
        return JsonSerializer.Deserialize<ControlMessage>(payload, JsonOptions)
            ?? throw new InvalidDataException("服务返回的协议消息无效。");
    }
}
