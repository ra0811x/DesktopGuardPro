using System.Text.Json;
using System.Text.Json.Serialization;

namespace DesktopGuardPro.Native;

internal sealed record ControlMessage(
    [property: JsonPropertyName("version")] ushort Version,
    [property: JsonPropertyName("requestId")] string RequestId,
    [property: JsonPropertyName("type")] string Type,
    [property: JsonPropertyName("deadlineUtc")] DateTimeOffset DeadlineUtc,
    [property: JsonPropertyName("payload")] JsonElement Payload);

internal sealed record HealthResult(
    [property: JsonPropertyName("maintenanceGate")] bool MaintenanceGate,
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("session")] SessionInfo? Session);

internal sealed record SessionInfo(
    [property: JsonPropertyName("ID")] string Id,
    [property: JsonPropertyName("Name")] string Name,
    [property: JsonPropertyName("State")] string State,
    [property: JsonPropertyName("Revision")] ulong Revision,
    [property: JsonPropertyName("MonitoringLevel")] string MonitoringLevel,
    [property: JsonPropertyName("MonitoringPolicy")] MonitoringPolicyInfo? MonitoringPolicy = null);

internal sealed record SessionResult(
    [property: JsonPropertyName("session")] SessionInfo? Session);

internal sealed record EndVerificationChallenge(
    [property: JsonPropertyName("token")] string Token,
    [property: JsonPropertyName("expiresUtc")] DateTimeOffset ExpiresUtc);

internal sealed record DirectoryMonitoringResult(
    [property: JsonPropertyName("directories")] IReadOnlyList<string> Directories,
    [property: JsonPropertyName("targets")] IReadOnlyList<MonitoringTargetInfo>? Targets,
    [property: JsonPropertyName("exclusions")] IReadOnlyList<MonitoringExclusionInfo>? Exclusions,
    [property: JsonPropertyName("inputActivityEnabled")] bool? InputActivityEnabled,
    [property: JsonPropertyName("windowTitleEnabled")] bool? WindowTitleEnabled,
    [property: JsonPropertyName("highRiskShortcutsEnabled")] bool? HighRiskShortcutsEnabled,
    [property: JsonPropertyName("monitoringPolicy")] MonitoringPolicyInfo? MonitoringPolicy = null,
    [property: JsonPropertyName("monitoringProfiles")] MonitoringProfilesInfo? MonitoringProfiles = null);

internal sealed record MonitoringProfilesInfo(
    [property: JsonPropertyName("relaxed")] MonitoringPolicyInfo Relaxed,
    [property: JsonPropertyName("standard")] MonitoringPolicyInfo Standard,
    [property: JsonPropertyName("strict")] MonitoringPolicyInfo Strict,
    [property: JsonPropertyName("custom")] MonitoringPolicyInfo Custom);

internal sealed record MonitoringPolicyInfo(
    [property: JsonPropertyName("fileActivityEnabled")] bool FileActivityEnabled,
    [property: JsonPropertyName("processAndSoftwareEnabled")] bool ProcessAndSoftwareEnabled,
    [property: JsonPropertyName("systemAndNetworkEnabled")] bool SystemAndNetworkEnabled,
    [property: JsonPropertyName("externalDevicesEnabled")] bool ExternalDevicesEnabled,
    [property: JsonPropertyName("userSessionActivityEnabled")] bool UserSessionActivityEnabled,
    [property: JsonPropertyName("strictReadAuditEnabled")] bool StrictReadAuditEnabled,
    [property: JsonPropertyName("version")] int Version = 3,
    [property: JsonPropertyName("file")] FileAuditPolicyInfo? File = null,
    [property: JsonPropertyName("processAndSoftware")] ProcessSoftwarePolicyInfo? ProcessAndSoftware = null,
    [property: JsonPropertyName("systemAndNetwork")] SystemNetworkPolicyInfo? SystemAndNetwork = null,
    [property: JsonPropertyName("externalDevices")] ExternalDevicePolicyInfo? ExternalDevices = null,
    [property: JsonPropertyName("userSession")] UserSessionPolicyInfo? UserSession = null,
    [property: JsonPropertyName("riskRules")] RiskRulePolicyInfo? RiskRules = null,
    [property: JsonPropertyName("inputShieldEnabled")] bool InputShieldEnabled = false,
    [property: JsonPropertyName("inputShield")] InputShieldPolicyInfo? InputShield = null,
    [property: JsonPropertyName("mode")] string Mode = "standard")
{
    internal static MonitoringPolicyInfo CreateDefault() => new(
        true, true, true, true, false, false, 3,
        new FileAuditPolicyInfo(true, true, true, true, true, false),
        new ProcessSoftwarePolicyInfo(false, false, false, false, false, 300),
        new SystemNetworkPolicyInfo(true, true, true, true, true, true, true, true, true, true, true, true, 30),
        new ExternalDevicePolicyInfo(true, true, 5),
        new UserSessionPolicyInfo(true, false, true, true, true, false, 5, 30),
        new RiskRulePolicyInfo(true, true, true, true, true, true, true),
        false,
        InputShieldPolicyInfo.CreateDefault());

    internal MonitoringPolicyInfo Resolved()
    {
        var defaults = CreateDefault();
        return this with
        {
            Version = 3,
            Mode = string.IsNullOrWhiteSpace(Mode) ? "custom" : Mode,
            File = File ?? defaults.File,
            ProcessAndSoftware = ProcessAndSoftware ?? defaults.ProcessAndSoftware,
            SystemAndNetwork = SystemAndNetwork ?? defaults.SystemAndNetwork,
            ExternalDevices = ExternalDevices ?? defaults.ExternalDevices,
            UserSession = UserSession ?? defaults.UserSession,
            RiskRules = RiskRules ?? defaults.RiskRules,
            InputShield = InputShield ?? defaults.InputShield,
        };
    }
}

internal sealed record FileAuditPolicyInfo(
    [property: JsonPropertyName("recordCreate")] bool RecordCreate,
    [property: JsonPropertyName("recordModify")] bool RecordModify,
    [property: JsonPropertyName("recordDelete")] bool RecordDelete,
    [property: JsonPropertyName("recordRename")] bool RecordRename,
    [property: JsonPropertyName("captureContentHash")] bool CaptureContentHash,
    [property: JsonPropertyName("captureBaseline")] bool CaptureBaseline);

internal sealed record ProcessSoftwarePolicyInfo(
    [property: JsonPropertyName("recordProcessStart")] bool RecordProcessStart,
    [property: JsonPropertyName("recordProcessStop")] bool RecordProcessStop,
    [property: JsonPropertyName("detectInstallers")] bool DetectInstallers,
    [property: JsonPropertyName("detectSoftwareRepair")] bool DetectSoftwareRepair,
    [property: JsonPropertyName("captureImageMetadata")] bool CaptureImageMetadata,
    [property: JsonPropertyName("snapshotIntervalSeconds")] int SnapshotIntervalSeconds);

internal sealed record SystemNetworkPolicyInfo(
    [property: JsonPropertyName("monitorAccounts")] bool MonitorAccounts,
    [property: JsonPropertyName("monitorNetwork")] bool MonitorNetwork,
    [property: JsonPropertyName("monitorProxy")] bool MonitorProxy,
    [property: JsonPropertyName("monitorFirewall")] bool MonitorFirewall,
    [property: JsonPropertyName("monitorRemoteDesktop")] bool MonitorRemoteDesktop,
    [property: JsonPropertyName("monitorAuditPolicy")] bool MonitorAuditPolicy,
    [property: JsonPropertyName("monitorSecurityLog")] bool MonitorSecurityLog,
    [property: JsonPropertyName("monitorClock")] bool MonitorClock,
    [property: JsonPropertyName("monitorServices")] bool MonitorServices,
    [property: JsonPropertyName("monitorDrivers")] bool MonitorDrivers,
    [property: JsonPropertyName("monitorScheduledTasks")] bool MonitorScheduledTasks,
    [property: JsonPropertyName("monitorStartupItems")] bool MonitorStartupItems,
    [property: JsonPropertyName("snapshotIntervalSeconds")] int SnapshotIntervalSeconds);

internal sealed record ExternalDevicePolicyInfo(
    [property: JsonPropertyName("recordConnect")] bool RecordConnect,
    [property: JsonPropertyName("recordDisconnect")] bool RecordDisconnect,
    [property: JsonPropertyName("snapshotIntervalSeconds")] int SnapshotIntervalSeconds);

internal sealed record UserSessionPolicyInfo(
    [property: JsonPropertyName("recordForegroundApplication")] bool RecordForegroundApplication,
    [property: JsonPropertyName("recordWindowTitle")] bool RecordWindowTitle,
    [property: JsonPropertyName("recordKeyboardActivity")] bool RecordKeyboardActivity,
    [property: JsonPropertyName("recordMouseClicks")] bool RecordMouseClicks,
    [property: JsonPropertyName("recordMouseWheel")] bool RecordMouseWheel,
    [property: JsonPropertyName("recordHighRiskShortcuts")] bool RecordHighRiskShortcuts,
    [property: JsonPropertyName("sampleIntervalSeconds")] int SampleIntervalSeconds,
    [property: JsonPropertyName("reportIntervalSeconds")] int ReportIntervalSeconds);

internal sealed record InputShieldPolicyInfo(
    [property: JsonPropertyName("blockPhysicalKeyboard")] bool BlockPhysicalKeyboard,
    [property: JsonPropertyName("blockPhysicalMouse")] bool BlockPhysicalMouse,
    [property: JsonPropertyName("blockPointerMovement")] bool BlockPointerMovement,
    [property: JsonPropertyName("injectedInputMode")] string InjectedInputMode,
    [property: JsonPropertyName("showWarningOverlay")] bool ShowWarningOverlay,
    [property: JsonPropertyName("warningDurationSeconds")] int WarningDurationSeconds,
    [property: JsonPropertyName("warningMessage")] string WarningMessage,
    [property: JsonPropertyName("trackActiveDevices")] bool TrackActiveDevices,
    [property: JsonPropertyName("warnOnDeviceArrival")] bool WarnOnDeviceArrival,
    [property: JsonPropertyName("recordDeviceRemoval")] bool RecordDeviceRemoval,
    [property: JsonPropertyName("unlockTrigger")] string UnlockTrigger,
    [property: JsonPropertyName("unlockKeyCode")] int UnlockKeyCode,
    [property: JsonPropertyName("unlockRequireControl")] bool UnlockRequireControl,
    [property: JsonPropertyName("unlockRequireAlt")] bool UnlockRequireAlt,
    [property: JsonPropertyName("unlockRequireShift")] bool UnlockRequireShift,
    [property: JsonPropertyName("unlockTapCount")] int UnlockTapCount,
    [property: JsonPropertyName("unlockTapWindowMilliseconds")] int UnlockTapWindowMilliseconds,
    [property: JsonPropertyName("credentialMode")] string CredentialMode,
    [property: JsonPropertyName("allowRecoveryCode")] bool AllowRecoveryCode,
    [property: JsonPropertyName("unlockAction")] string UnlockAction,
    [property: JsonPropertyName("maxFailedUnlockAttempts")] int MaxFailedUnlockAttempts,
    [property: JsonPropertyName("unlockLockoutSeconds")] int UnlockLockoutSeconds,
    [property: JsonPropertyName("recordBlockedInputCategory")] bool RecordBlockedInputCategory,
    [property: JsonPropertyName("recordKeyNames")] bool RecordKeyNames,
    [property: JsonPropertyName("recordPointerCoordinates")] bool RecordPointerCoordinates,
    [property: JsonPropertyName("restoreAfterRestart")] bool RestoreAfterRestart,
    [property: JsonPropertyName("hookHeartbeatSeconds")] int HookHeartbeatSeconds)
{
    internal InputShieldPolicyInfo ForTemporaryControl() => this with
    {
        BlockPhysicalKeyboard = true,
        BlockPhysicalMouse = true,
        BlockPointerMovement = true,
        InjectedInputMode = "compatible",
        CredentialMode = "local",
        AllowRecoveryCode = true,
        RestoreAfterRestart = false,
        UnlockAction = "suspend",
    };

    internal static InputShieldPolicyInfo CreateDefault() => new(
        true, true, true, "compatible", true, 5,
        "检测到本地键盘或鼠标输入，当前设备处于保护状态。",
        true, true, true, "combination", 0x20, true, true, false,
        5, 1500, "windows", false, "suspend", 5, 60,
        true, false, false, false, 5);
}

internal sealed record InputShieldCredentialResult(
    [property: JsonPropertyName("configured")] bool Configured,
    [property: JsonPropertyName("recoveryCodeEnabled")] bool RecoveryCodeEnabled,
    [property: JsonPropertyName("recoveryCode")] string? RecoveryCode,
    [property: JsonPropertyName("verified")] bool Verified);

internal sealed record InputShieldDeviceInfo(
    [property: JsonPropertyName("kind")] string Kind,
    [property: JsonPropertyName("interfacePath")] string? InterfacePath,
    [property: JsonPropertyName("instanceId")] string? InstanceId,
    [property: JsonPropertyName("vendorId")] string? VendorId,
    [property: JsonPropertyName("productId")] string? ProductId,
    [property: JsonPropertyName("active")] bool Active,
    [property: JsonPropertyName("lastActiveUtc")] DateTimeOffset? LastActiveUtc);

internal sealed record InputShieldStatusResult(
    [property: JsonPropertyName("sessionId")] string? SessionId,
    [property: JsonPropertyName("state")] string State,
    [property: JsonPropertyName("hookRunning")] bool HookRunning,
    [property: JsonPropertyName("droppedEvents")] ulong DroppedEvents,
    [property: JsonPropertyName("observedUtc")] DateTimeOffset? ObservedUtc,
    [property: JsonPropertyName("devices")] IReadOnlyList<InputShieldDeviceInfo>? Devices);

internal sealed record InputControlResult(
    [property: JsonPropertyName("controlId")] string? ControlId,
    [property: JsonPropertyName("source")] string? Source,
    [property: JsonPropertyName("enabled")] bool Enabled,
    [property: JsonPropertyName("state")] string State,
    [property: JsonPropertyName("startedUtc")] DateTimeOffset? StartedUtc,
    [property: JsonPropertyName("expiresUtc")] DateTimeOffset? ExpiresUtc,
    [property: JsonPropertyName("indefinite")] bool Indefinite,
    [property: JsonPropertyName("policy")] InputShieldPolicyInfo? Policy,
    [property: JsonPropertyName("hookRunning")] bool HookRunning,
    [property: JsonPropertyName("droppedEvents")] ulong DroppedEvents,
    [property: JsonPropertyName("observedUtc")] DateTimeOffset? ObservedUtc,
    [property: JsonPropertyName("devices")] IReadOnlyList<InputShieldDeviceInfo>? Devices);

internal sealed record RiskRulePolicyInfo(
    [property: JsonPropertyName("fileActivity")] bool FileActivity,
    [property: JsonPropertyName("sensitiveProcess")] bool SensitiveProcess,
    [property: JsonPropertyName("startupChange")] bool StartupChange,
    [property: JsonPropertyName("softwareChange")] bool SoftwareChange,
    [property: JsonPropertyName("deviceConnection")] bool DeviceConnection,
    [property: JsonPropertyName("collectionGap")] bool CollectionGap,
    [property: JsonPropertyName("securityState")] bool SecurityState);

internal sealed record MonitoringTargetInfo(
    [property: JsonPropertyName("path")] string Path,
    [property: JsonPropertyName("kind")] string Kind,
    [property: JsonPropertyName("recursive")] bool Recursive,
    [property: JsonPropertyName("configuredPath")] string? ConfiguredPath = null,
    [property: JsonPropertyName("resolutionStatus")] string? ResolutionStatus = null,
    [property: JsonPropertyName("resolutionDetail")] string? ResolutionDetail = null);

internal sealed record MonitoringExclusionInfo(
    [property: JsonPropertyName("kind")] string Kind,
    [property: JsonPropertyName("pattern")] string Pattern);

internal sealed record MonitoringImpactInfo(
    [property: JsonPropertyName("requiresAdministrator")] bool RequiresAdministrator,
    [property: JsonPropertyName("expectedEventVolume")] string ExpectedEventVolume,
    [property: JsonPropertyName("performanceImpact")] string PerformanceImpact,
    [property: JsonPropertyName("storageImpact")] string StorageImpact);

internal sealed record SessionStartPreviewResult(
    [property: JsonPropertyName("monitoringMode")] string MonitoringMode,
    [property: JsonPropertyName("monitoringLevel")] string MonitoringLevel,
    [property: JsonPropertyName("monitoringPolicy")] MonitoringPolicyInfo? MonitoringPolicy,
    [property: JsonPropertyName("monitoredTargetCount")] int MonitoredTargetCount,
    [property: JsonPropertyName("targets")] IReadOnlyList<MonitoringTargetInfo> Targets,
    [property: JsonPropertyName("impact")] MonitoringImpactInfo Impact);

internal sealed record BaselineCaptureFailureInfo(
    [property: JsonPropertyName("item")] string Item,
    [property: JsonPropertyName("reason")] string Reason);

internal sealed record BaselineStartDecisionInfo(
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("failures")] IReadOnlyList<BaselineCaptureFailureInfo> Failures,
    [property: JsonPropertyName("requiresUserChoice")] bool RequiresUserChoice,
    [property: JsonPropertyName("canContinue")] bool CanContinue,
    [property: JsonPropertyName("canCancel")] bool CanCancel);

internal sealed record SessionBaselineReviewResult(
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("decision")] BaselineStartDecisionInfo Decision,
    [property: JsonPropertyName("resolution")] string? Resolution,
    [property: JsonPropertyName("session")] SessionInfo? Session);

internal sealed record SessionHistoryItem(
    [property: JsonPropertyName("session")] SessionInfo Session,
    [property: JsonPropertyName("createdUtc")] DateTimeOffset CreatedUtc,
    [property: JsonPropertyName("updatedUtc")] DateTimeOffset UpdatedUtc,
    [property: JsonPropertyName("retentionLocked")] bool RetentionLocked);

internal sealed record SessionHistoryPage(
    [property: JsonPropertyName("items")] IReadOnlyList<SessionHistoryItem> Items,
    [property: JsonPropertyName("hasMore")] bool HasMore,
    [property: JsonPropertyName("nextCursor")] string? NextCursor);

internal sealed record SessionRetentionLockResult(
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("locked")] bool Locked);

internal sealed record AuditEventInfo(
    [property: JsonPropertyName("eventId")] string EventId,
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("sequence")] ulong Sequence,
    [property: JsonPropertyName("category")] string Category,
    [property: JsonPropertyName("action")] string Action,
    [property: JsonPropertyName("severity")] string Severity,
    [property: JsonPropertyName("observedUtc")] DateTimeOffset ObservedUtc,
    [property: JsonPropertyName("processKey")] string? ProcessKey,
    [property: JsonPropertyName("objectKey")] string? ObjectKey,
    [property: JsonPropertyName("source")] string Source,
    [property: JsonPropertyName("confidence")] string Confidence);

internal sealed record TimelineRecord(
    [property: JsonPropertyName("Event")] AuditEventInfo Event,
    [property: JsonPropertyName("Payload")] string? Payload,
    [property: JsonPropertyName("previewTruncated")] bool PreviewTruncated);

internal sealed record TimelinePage(
    [property: JsonPropertyName("integrityScope")] string? IntegrityScope,
    [property: JsonPropertyName("scannedEvents")] int ScannedEvents,
    [property: JsonPropertyName("records")] IReadOnlyList<TimelineRecord> Records,
    [property: JsonPropertyName("nextCursor")] string? NextCursor,
    [property: JsonPropertyName("hasMore")] bool HasMore,
    [property: JsonPropertyName("integrityVerified")] bool IntegrityVerified);

internal sealed record RiskEvidence(
    [property: JsonPropertyName("eventId")] string EventId,
    [property: JsonPropertyName("sequence")] ulong Sequence,
    [property: JsonPropertyName("observedUtc")] DateTimeOffset ObservedUtc,
    [property: JsonPropertyName("category")] string Category,
    [property: JsonPropertyName("action")] string Action,
    [property: JsonPropertyName("objectKey")] string? ObjectKey);

internal sealed record RiskFinding(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("ruleId")] string RuleId,
    [property: JsonPropertyName("title")] string Title,
    [property: JsonPropertyName("summary")] string Summary,
    [property: JsonPropertyName("level")] string Level,
    [property: JsonPropertyName("score")] byte Score,
    [property: JsonPropertyName("confidence")] double Confidence,
    [property: JsonPropertyName("status")] string Status,
    [property: JsonPropertyName("firstObservedUtc")] DateTimeOffset FirstObservedUtc,
    [property: JsonPropertyName("lastObservedUtc")] DateTimeOffset LastObservedUtc,
    [property: JsonPropertyName("evidence")] IReadOnlyList<RiskEvidence> Evidence,
    [property: JsonPropertyName("tags")] IReadOnlyList<string>? Tags);

internal sealed record RiskFindingStatusResult(
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("findingId")] string FindingId,
    [property: JsonPropertyName("status")] string Status);

internal sealed record RiskRuleFailure(
    [property: JsonPropertyName("ruleId")] string RuleId,
    [property: JsonPropertyName("message")] string Message);

internal sealed record RiskEvaluation(
    [property: JsonPropertyName("eventCount")] ulong EventCount,
    [property: JsonPropertyName("coverageGapCount")] ulong CoverageGapCount,
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("findings")] IReadOnlyList<RiskFinding> Findings,
    [property: JsonPropertyName("failures")] IReadOnlyList<RiskRuleFailure> Failures);

internal sealed record AssetInfo(
    [property: JsonPropertyName("category")] string Category,
    [property: JsonPropertyName("identifier")] string Identifier,
    [property: JsonPropertyName("displayName")] string DisplayName,
    [property: JsonPropertyName("attributes")] IReadOnlyDictionary<string, string>? Attributes);

internal sealed record AssetDifference(
    [property: JsonPropertyName("kind")] string Kind,
    [property: JsonPropertyName("presenceStatus")] string? PresenceStatus,
    [property: JsonPropertyName("before")] AssetInfo? Before,
    [property: JsonPropertyName("after")] AssetInfo? After,
    [property: JsonPropertyName("changedAttributes")] IReadOnlyList<string> ChangedAttributes);

internal sealed record AssetDifferenceResult(
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("baselineAvailable")] bool BaselineAvailable,
    [property: JsonPropertyName("differences")] IReadOnlyList<AssetDifference> Differences);

internal sealed record ReportTransfer(
    [property: JsonPropertyName("token")] string Token,
    [property: JsonPropertyName("size")] int Size,
    [property: JsonPropertyName("sha256")] string Sha256,
    [property: JsonPropertyName("chunkBytes")] int ChunkBytes,
    [property: JsonPropertyName("expiresUtc")] DateTimeOffset ExpiresUtc);

internal sealed record VerificationManifest(
    [property: JsonPropertyName("schemaVersion")] int SchemaVersion,
    [property: JsonPropertyName("softwareVersion")] string SoftwareVersion,
    [property: JsonPropertyName("algorithm")] string Algorithm,
    [property: JsonPropertyName("reportSha256")] string ReportSha256,
    [property: JsonPropertyName("reportSize")] int ReportSize,
    [property: JsonPropertyName("mediaType")] string MediaType,
    [property: JsonPropertyName("sessionId")] string SessionId,
    [property: JsonPropertyName("sessionRevision")] ulong SessionRevision,
    [property: JsonPropertyName("reportGeneratedUtc")] DateTimeOffset ReportGeneratedUtc,
    [property: JsonPropertyName("integrityVerified")] bool IntegrityVerified);

internal sealed record ReportResult(
    [property: JsonPropertyName("format")] string Format,
    [property: JsonPropertyName("mediaType")] string MediaType,
    [property: JsonPropertyName("content")] string? Content,
    [property: JsonPropertyName("transfer")] ReportTransfer? Transfer,
    [property: JsonPropertyName("verification")] VerificationManifest? Verification,
    [property: JsonPropertyName("generatedUtc")] DateTimeOffset GeneratedUtc);

internal sealed record ReportChunkResult(
    [property: JsonPropertyName("content")] byte[] Content,
    [property: JsonPropertyName("nextOffset")] int NextOffset,
    [property: JsonPropertyName("done")] bool Done);

internal sealed record ServiceError(
    [property: JsonPropertyName("code")] string Code,
    [property: JsonPropertyName("message")] string Message);
