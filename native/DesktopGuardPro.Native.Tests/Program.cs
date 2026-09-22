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
