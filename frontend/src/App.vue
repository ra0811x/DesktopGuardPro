<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

import {
  type EventCategory,
	type EventSeverity,
	MonitoringLevel,
  type Session,
  SessionState,
} from "../bindings/desktopguardpro/internal/domain";
import type { RedactionPolicy } from "../bindings/desktopguardpro/internal/reporting";
import type { ReportFormat } from "../bindings/desktopguardpro/internal/service";
import type { SessionStartPreviewResult } from "../bindings/desktopguardpro/internal/service";
import { BaselineReviewResolution } from "../bindings/desktopguardpro/internal/storage";
import DirectoryMonitoringPanel from "./DirectoryMonitoringPanel.vue";
import AssetDifferencePanel from "./AssetDifferencePanel.vue";
import ReportExportPanel from "./ReportExportPanel.vue";
import RiskPanel from "./RiskPanel.vue";
import SessionSummaryPanel from "./SessionSummaryPanel.vue";
import TimelinePanel from "./TimelinePanel.vue";
import SessionHistoryPanel from "./SessionHistoryPanel.vue";
import { useSessionHistory } from "./sessionHistory";
import { useAnalysisState, type ReportSequenceRange } from "./analysisState";
import { useDirectoryMonitoring } from "./directoryMonitoring";
import { useAssetDifferenceState } from "./assetDifferenceState";
import { safeReportFilename, saveInlineReport } from "./reportExport";
import { useServiceConnection } from "./serviceConnection";

const busy = ref(false);
const selectedMonitoringLevel = ref<MonitoringLevel>(MonitoringLevel.MonitoringLevelStandard);
const startPreview = ref<SessionStartPreviewResult | null>(null);
const pendingStart = ref<{ id: string; name: string } | null>(null);
const exportFeedback = ref("");
const activeView = ref<"home" | "history" | "summary" | "timeline" | "risk" | "assets" | "report">("home");
const connection = useServiceConnection();
const analysis = useAnalysisState();
const assetDifferences = useAssetDifferenceState();
const directoryMonitoring = useDirectoryMonitoring();
const {
	 targets,
	 draftTargets,
  directories,
  loaded: directoriesLoaded,
  loading: directoriesLoading,
  saving: directoriesSaving,
  dirty: directoriesDirty,
  errorMessage: directoryError,
} = directoryMonitoring;
const {
  connected: serviceConnected,
  currentSession,
  errorMessage,
  healthStatus: collectionHealthStatus,
  phase: connectionPhase,
  status: serviceStatus,
	baselineReview,
} = connection;
const history = useSessionHistory(currentSession);
const {
  viewedSession, viewingHistory, items: historyItems, loading: historyLoading, loaded: historyLoaded,
  hasMore: historyHasMore, errorMessage: historyError, retentionBusy, retentionFeedback,
} = history;
const {
  errorMessage: analysisError,
  evaluation,
  exporting,
  filters: analysisFilters,
  hasMore,
  integrityVerified,
  loadingRisk,
  loadingTimeline,
  timeline,
	updatingFindingID,
} = analysis;

const stateLabels: Partial<Record<SessionState, string>> = {
  [SessionState.SessionStateDraft]: "草稿",
  [SessionState.SessionStatePreparing]: "准备中",
	[SessionState.SessionStateBaselineReview]: "基线待复核",
  [SessionState.SessionStateActive]: "保护中",
  [SessionState.SessionStateDegraded]: "降级保护",
  [SessionState.SessionStateFinalizing]: "正在结束",
  [SessionState.SessionStateCompleted]: "已完成",
  [SessionState.SessionStateFailed]: "失败",
};

const active = computed(
  () =>
    currentSession.value?.State === SessionState.SessionStateActive ||
    currentSession.value?.State === SessionState.SessionStateDegraded,
);

const directorySetupReady = computed(
  () => directoriesLoaded.value && !directoriesLoading.value && !directoriesSaving.value && !directoriesDirty.value,
);

const needsNewSession = computed(() => !currentSession.value ||
  currentSession.value.State === SessionState.SessionStateCompleted ||
  currentSession.value.State === SessionState.SessionStateFailed);

const canEditDirectories = computed(() =>
  serviceConnected.value && !busy.value && (!currentSession.value ||
    currentSession.value.State === SessionState.SessionStateCompleted ||
    currentSession.value.State === SessionState.SessionStateFailed),
);

const protectionDescription = computed(() => {
  if (!directoriesLoaded.value) return "记录进程、设备和系统设置变化；文件监控范围待读取。";
  if (targets.value.length === 0) return "未配置重点对象，仅记录进程、设备和系统设置变化。";
	const fileCount = targets.value.filter((target) => target.kind === "file").length;
	const volumeCount = targets.value.filter((target) => target.kind === "removable_volume").length;
  return `监控 ${targets.value.length} 个重点对象（${directories.value.length} 个文件夹、${fileCount} 个文件、${volumeCount} 个可移动磁盘）。`;
});

const protectionTitle = computed(() => {
  if (!currentSession.value) return "尚未开启保护";
  return stateLabels[currentSession.value.State] ?? currentSession.value.State;
});

const actionLabel = computed(() => {
  if (busy.value) return "正在处理";
  if (currentSession.value?.State === SessionState.SessionStateDraft) return "继续准备";
  if (currentSession.value?.State === SessionState.SessionStatePreparing) return "继续启动";
	if (currentSession.value?.State === SessionState.SessionStateBaselineReview) return "处理基线复核";
  if (currentSession.value?.State === SessionState.SessionStateFinalizing) return "完成收尾";
  return active.value ? "结束保护" : "开启保护";
});

const pageTitle = computed(() => {
  switch (activeView.value) {
    case "history": return "历史会话";
    case "summary": return "会话摘要";
    case "timeline": return "事件时间线";
    case "risk": return "风险与覆盖";
    case "assets": return "资产差异";
    case "report": return "报告导出";
    default: return "仪表盘";
  }
});

const riskCount = computed(() => evaluation.value?.findings.length ?? 0);
const collectionHealthLabel = computed(() => {
  if (!serviceConnected.value) return "恢复中";
  return collectionHealthStatus.value === "degraded" ? "降级" : "正常";
});

async function handleProtectionAction(): Promise<void> {
  if (!serviceConnected.value || busy.value || (needsNewSession.value && !directorySetupReady.value)) return;
	const id = crypto.randomUUID();
	const name = `离席保护 ${new Date().toLocaleString("zh-CN")}`;
	if (needsNewSession.value) {
		busy.value = true;
		errorMessage.value = "";
		try {
			startPreview.value = await connection.previewSessionStart(selectedMonitoringLevel.value);
			pendingStart.value = { id, name };
		} catch {
			errorMessage.value = "无法读取启动预览，请检查后台服务后重试";
		} finally {
			busy.value = false;
		}
		return;
	}
	await runProtectionAction(id, name, currentSession.value?.MonitoringLevel ?? selectedMonitoringLevel.value);
}

async function runProtectionAction(id: string, name: string, level: MonitoringLevel): Promise<void> {
  busy.value = true;
  errorMessage.value = "";
  try {
	await connection.performProtectionAction(id, name, level);
	if (currentSession.value?.State === SessionState.SessionStateBaselineReview) await connection.loadBaselineReview();
    history.viewCurrent();
    await analysis.refresh(currentSession.value?.ID ?? "");
  } catch {
    // 连接管理器会同步服务状态并生成面向用户的错误信息。
  } finally {
    busy.value = false;
  }
}

async function confirmSessionStart(): Promise<void> {
	const start = pendingStart.value;
	if (!start || !startPreview.value) return;
	startPreview.value = null;
	pendingStart.value = null;
	await runProtectionAction(start.id, start.name, selectedMonitoringLevel.value);
}

function cancelSessionStart(): void {
	startPreview.value = null;
	pendingStart.value = null;
}

function impactLabel(value: string): string {
	if (value === "high") return "高";
	if (value === "medium") return "中";
	return "低";
}

async function resolveBaselineReview(resolution: BaselineReviewResolution): Promise<void> {
	if (busy.value) return;
	busy.value = true;
	try {
		await connection.resolveBaselineReview(resolution);
		history.viewCurrent();
		await analysis.refresh(currentSession.value?.ID ?? "");
	} catch {
		// 连接状态负责显示服务端返回的复核错误。
	} finally {
		busy.value = false;
	}
}

function selectView(view: typeof activeView.value): void {
  activeView.value = view;
  if (view === "home") history.viewCurrent();
  if (view === "history") { if (serviceConnected.value) void history.load(); return; }
}

function selectHistoricalSession(session: Session): void {
  const previousID = viewedSession.value?.ID;
  history.select(session);
  activeView.value = "summary";
  if (previousID === viewedSession.value?.ID) void analysis.refresh(session.ID);
}

function toggleTimelineCategory(category: EventCategory): void {
  const index = analysisFilters.categories.indexOf(category);
  if (index >= 0) analysisFilters.categories.splice(index, 1);
  else analysisFilters.categories.push(category);
}

function toggleTimelineSeverity(severity: EventSeverity): void {
	const index = analysisFilters.severities.indexOf(severity);
	if (index >= 0) analysisFilters.severities.splice(index, 1);
	else analysisFilters.severities.push(severity);
}

async function handleReportExport(format: ReportFormat, redaction: RedactionPolicy, range: ReportSequenceRange = {}): Promise<void> {
  const exportedSession = viewedSession.value;
  if (!exportedSession) return;
  exportFeedback.value = "";
  try {
    const result = await analysis.exportReport(format, redaction, range);
    saveInlineReport(result, exportedSession.Name);
    exportFeedback.value = `已发起报告下载：${safeReportFilename(exportedSession.Name, result.format, result.generatedUtc)}`;
  } catch {
    // 分析状态会提供经过整理的用户提示。
  }
}

watch(
  () => viewedSession.value?.ID ?? "",
  (sessionID) => {
    void analysis.refresh(sessionID);
    void assetDifferences.refresh(sessionID);
  },
);

watch(serviceConnected, (connected) => {
  if (connected && !directoriesLoaded.value) void directoryMonitoring.load();
  if (connected && activeView.value === "history") void history.load();
}, { immediate: true });

watch(
	() => `${currentSession.value?.ID ?? ""}:${currentSession.value?.State ?? ""}`,
	() => {
		if (currentSession.value?.State === SessionState.SessionStateBaselineReview && !baselineReview.value) {
			void connection.loadBaselineReview();
		}
	},
	{ immediate: true },
);

onMounted(connection.start);
onBeforeUnmount(() => {
  directoryMonitoring.stop();
  history.stop();
  analysis.stop();
  assetDifferences.stop();
  connection.stop();
});
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar" aria-label="主导航">
      <div class="brand" aria-label="Desktop Guard Pro">
        <span class="brand-mark" aria-hidden="true">DG</span>
        <span class="brand-copy"><strong>Desktop Guard</strong><small>Pro</small></span>
      </div>

      <nav>
        <p class="nav-caption">保护会话</p>
        <button class="nav-item" :class="{ active: activeView === 'home' }" :aria-current="activeView === 'home' ? 'page' : undefined" type="button" @click="selectView('home')">仪表盘</button>
        <button class="nav-item" :class="{ active: activeView === 'history' }" :aria-current="activeView === 'history' ? 'page' : undefined" type="button" @click="selectView('history')">历史会话</button>
        <button class="nav-item" :class="{ active: activeView === 'summary' }" :aria-current="activeView === 'summary' ? 'page' : undefined" type="button" :disabled="!viewedSession" @click="selectView('summary')">会话摘要</button>
        <p class="nav-caption">审计结果</p>
        <button class="nav-item" :class="{ active: activeView === 'timeline' }" :aria-current="activeView === 'timeline' ? 'page' : undefined" type="button" :disabled="!viewedSession" @click="selectView('timeline')">事件时间线</button>
        <button class="nav-item" :class="{ active: activeView === 'risk' }" :aria-current="activeView === 'risk' ? 'page' : undefined" type="button" :disabled="!viewedSession" @click="selectView('risk')">风险分析</button>
        <button class="nav-item" :class="{ active: activeView === 'assets' }" :aria-current="activeView === 'assets' ? 'page' : undefined" type="button" :disabled="!viewedSession" @click="selectView('assets')">资产差异</button>
        <button class="nav-item" :class="{ active: activeView === 'report' }" :aria-current="activeView === 'report' ? 'page' : undefined" type="button" :disabled="!viewedSession" @click="selectView('report')">报告导出</button>
      </nav>

    </aside>

    <section class="desktop-workspace">
      <header class="desktop-command-bar" aria-label="会话命令栏">
        <h1>{{ pageTitle }}</h1>
        <div class="command-context" aria-label="当前会话">
          <span>当前会话</span>
          <strong :title="currentSession?.Name ?? '无活动会话'">
            {{ currentSession?.Name ?? "无活动会话" }}
          </strong>
        </div>
        <span class="service-indicator" :class="connectionPhase">
          <span class="indicator-dot" aria-hidden="true"></span>
          {{ serviceStatus }}
        </span>
		<label class="monitoring-level-control">
			<span>监控级别</span>
			<select v-model="selectedMonitoringLevel" class="monitoring-level-select" :disabled="busy || !needsNewSession">
				<option :value="MonitoringLevel.MonitoringLevelStandard">标准</option>
				<option :value="MonitoringLevel.MonitoringLevelStrict">严格</option>
			</select>
		</label>
        <button
          class="primary-action"
          type="button"
          :disabled="!serviceConnected || busy || (needsNewSession && !directorySetupReady)"
          @click="handleProtectionAction"
        >
          {{ actionLabel }}
        </button>
      </header>

	  <main class="content">
	  <p v-if="errorMessage" class="error-banner" role="alert">
        {{ errorMessage }}
      </p>

      <section v-if="viewedSession && activeView !== 'home' && activeView !== 'history'" class="viewed-session">
        <span>{{ viewingHistory ? '正在查看历史会话' : '正在查看当前会话' }}：{{ viewedSession.Name }}</span>
        <button v-if="viewingHistory && currentSession" type="button" @click="history.viewCurrent()">返回当前会话</button>
      </section>

      <template v-if="activeView === 'home'">
        <div class="home-workspace">
          <section class="protection-overview" :class="{ active, degraded: collectionHealthStatus === 'degraded' }" aria-labelledby="protection-title">
            <div class="protection-overview__main">
              <div class="hero-status-icon" :class="{ active, degraded: collectionHealthStatus === 'degraded' }" aria-hidden="true"></div>
              <div class="hero-copy">
                <span class="field-label">保护会话</span>
                <h2 id="protection-title">{{ protectionTitle }}</h2>
                <p class="description">{{ protectionDescription }}</p>
              </div>
            </div>
            <div class="protection-overview__status" aria-label="采集状态">
              <span>采集状态</span>
              <strong>{{ collectionHealthLabel }}</strong>
              <small>{{ serviceStatus }}</small>
            </div>
          </section>

          <section class="metric-grid" aria-label="保护摘要">
            <article class="metric-card">
              <span>重点对象</span>
              <strong>{{ targets.length }}</strong>
              <small>{{ targets.length === 0 ? "未启用文件变化采集" : "已纳入本次保护范围" }}</small>
            </article>
            <article class="metric-card">
              <span>风险提示</span>
              <strong>{{ riskCount }}</strong>
              <small>{{ evaluation ? "基于当前会话事件计算" : "会话结束后可查看分析结果" }}</small>
            </article>
            <article class="metric-card">
              <span>会话记录</span>
              <strong>{{ currentSession ? "进行中" : "等待开启" }}</strong>
              <small>{{ currentSession ? `修订 ${currentSession.Revision}` : "开启后将保存保护期间的事件" }}</small>
            </article>
          </section>

          <div class="home-detail-grid">
            <DirectoryMonitoringPanel
			  v-model="draftTargets"
              :loaded="directoriesLoaded"
              :loading="directoriesLoading"
              :saving="directoriesSaving"
              :dirty="directoriesDirty"
              :editable="canEditDirectories"
              :connected="serviceConnected"
              :error-message="directoryError"
              @save="directoryMonitoring.save"
              @reload="directoryMonitoring.load"
            />

            <aside class="protection-guidance" aria-labelledby="protection-guidance-title">
              <div class="protection-guidance__heading">
                <span class="field-label">保护说明</span>
                <h2 id="protection-guidance-title">审计结果如何读取</h2>
              </div>
              <dl>
                <div><dt>事件时间线</dt><dd>查看采集到的文件、进程、设备和系统变化。</dd></div>
                <div><dt>风险分析</dt><dd>按规则关联会话事件，并标记需要关注的变化。</dd></div>
                <div><dt>采集完整性</dt><dd>服务中断或采集缺口会显示在分析结果和报告中。</dd></div>
              </dl>
            </aside>
          </div>
        </div>
      </template>

      <SessionHistoryPanel v-else-if="activeView === 'history'"
        :items="historyItems" :loading="historyLoading" :loaded="historyLoaded" :has-more="historyHasMore"
        :connected="serviceConnected" :error-message="historyError" :retention-busy="retentionBusy"
        :retention-feedback="retentionFeedback" :selected-i-d="viewedSession?.ID"
        @refresh="history.load()" @load-more="history.loadMore()" @select="selectHistoricalSession"
        @update-retention="history.updateRetentionLock" @prune="history.prune" />

      <SessionSummaryPanel v-else-if="activeView === 'summary' && viewedSession"
        :session="viewedSession"
        :viewing-history="viewingHistory"
        :evaluation="evaluation"
        :timeline="timeline"
        :integrity-verified="integrityVerified"
        :loading="loadingRisk || loadingTimeline"
        @open-timeline="selectView('timeline')"
        @open-risk="selectView('risk')"
        @open-report="selectView('report')"
      />

	  <div v-else-if="activeView === 'timeline'" class="analysis-view">
		<TimelinePanel
		  :records="timeline"
		  :categories="analysisFilters.categories"
		  :severities="analysisFilters.severities"
		  :user="analysisFilters.user"
		  :process="analysisFilters.process"
		  :path="analysisFilters.path"
		  :from-utc="analysisFilters.fromUtc"
		  :to-utc="analysisFilters.toUtc"
		  :loading="loadingTimeline"
		  :has-more="hasMore"
		  :integrity-verified="integrityVerified"
		  :error-message="analysisError"
		  @toggle-category="toggleTimelineCategory"
		  @toggle-severity="toggleTimelineSeverity"
		  @update-user="analysisFilters.user = $event"
		  @update-process="analysisFilters.process = $event"
		  @update-path="analysisFilters.path = $event"
		  @update-from-utc="analysisFilters.fromUtc = $event"
		  @update-to-utc="analysisFilters.toUtc = $event"
		  @refresh="analysis.refresh(viewedSession?.ID ?? '')"
		  @load-more="analysis.loadMore"
		/>
	  </div>

	  <div v-else-if="activeView === 'risk'" class="analysis-view">
		<p v-if="analysisError" class="error-banner" role="alert">{{ analysisError }}</p>
		<RiskPanel
		  :evaluation="evaluation"
		  :timeline="timeline"
		  :loading="loadingRisk"
		  :updating-finding-i-d="updatingFindingID"
		  @update-finding-status="analysis.updateFindingStatus"
		/>
	  </div>

	  <div v-else-if="activeView === 'assets'" class="analysis-view">
		<AssetDifferencePanel
		  :differences="assetDifferences.differences.value"
		  :categories="assetDifferences.categories.value"
		  :baseline-available="assetDifferences.baselineAvailable.value"
		  :loading="assetDifferences.loading.value"
		  :error-message="assetDifferences.errorMessage.value"
		  :empty-message="assetDifferences.emptyMessage.value"
		  @refresh="assetDifferences.refresh(viewedSession?.ID ?? '')"
		  @toggle-category="assetDifferences.toggleCategory"
		/>
	  </div>

	  <div v-else class="analysis-view narrow-analysis-view">
		<p v-if="analysisError" class="error-banner" role="alert">{{ analysisError }}</p>
		<ReportExportPanel
		  :disabled="!viewedSession || !serviceConnected"
		  :exporting="exporting"
		  :export-feedback="exportFeedback"
		  @export="handleReportExport"
		/>
	  </div>
	  </main>

      <footer class="desktop-status-bar" role="status" aria-label="应用状态栏">
        <span><span class="status-dot" :class="connectionPhase" aria-hidden="true"></span>{{ serviceStatus }}</span>
        <span>采集健康：{{ collectionHealthLabel }}</span>
        <span>保护状态：{{ protectionTitle }}</span>
        <span class="status-local">审计数据保存在本机</span>
      </footer>
    </section>

	<div v-if="startPreview" class="start-preview-backdrop" role="presentation" @click.self="cancelSessionStart">
		<section class="start-preview-dialog" role="dialog" aria-modal="true" aria-labelledby="start-preview-title">
			<header>
				<span class="field-label">启动确认</span>
				<h2 id="start-preview-title">确认保护范围与影响</h2>
			</header>
			<dl class="start-preview-impact">
				<div><dt>监控级别</dt><dd>{{ startPreview.monitoringLevel === MonitoringLevel.MonitoringLevelStrict ? "严格" : "标准" }}</dd></div>
				<div><dt>重点对象</dt><dd>{{ startPreview.monitoredTargetCount }} 项</dd></div>
				<div><dt>预计事件量</dt><dd>{{ impactLabel(startPreview.impact.expectedEventVolume) }}</dd></div>
				<div><dt>性能影响</dt><dd>{{ impactLabel(startPreview.impact.performanceImpact) }}</dd></div>
				<div><dt>存储影响</dt><dd>{{ impactLabel(startPreview.impact.storageImpact) }}</dd></div>
				<div><dt>管理员权限</dt><dd>{{ startPreview.impact.requiresAdministrator ? "需要" : "无需额外权限" }}</dd></div>
			</dl>
			<p v-if="startPreview.monitoringLevel === MonitoringLevel.MonitoringLevelStrict" class="strict-impact-note">严格模式会启用对象访问读取审计，事件量与性能影响较高；窗口标题等敏感字段仍由单独设置控制。</p>
			<ul v-if="startPreview.targets.length" class="start-preview-targets">
				<li v-for="target in startPreview.targets" :key="`${target.kind}-${target.path}`"><span>{{ target.kind }}</span><strong>{{ target.configuredPath || target.path }}</strong></li>
			</ul>
			<footer><button type="button" @click="cancelSessionStart">取消</button><button class="confirm-start" type="button" @click="confirmSessionStart">确认并启动</button></footer>
		</section>
	</div>

	<div v-if="baselineReview" class="start-preview-backdrop" role="presentation">
		<section class="start-preview-dialog baseline-review-dialog" role="dialog" aria-modal="true" aria-labelledby="baseline-review-title">
			<header>
				<span class="field-label">启动基线复核</span>
				<h2 id="baseline-review-title">部分基线项目采集失败</h2>
			</header>
			<p class="baseline-review-copy">下列项目没有纳入本次基线。继续后仍会保存已成功采集的数据，并在报告中保留失败原因。</p>
			<ul class="baseline-review-failures">
				<li v-for="failure in baselineReview.decision.failures" :key="`${failure.item}-${failure.reason}`">
					<strong>{{ failure.item }}</strong><span>{{ failure.reason }}</span>
				</li>
			</ul>
			<footer>
				<button v-if="baselineReview.decision.canCancel" type="button" :disabled="busy" @click="resolveBaselineReview(BaselineReviewResolution.BaselineReviewResolutionCancel)">取消会话</button>
				<button v-if="baselineReview.decision.canContinue" class="confirm-start continue-baseline" type="button" :disabled="busy" @click="resolveBaselineReview(BaselineReviewResolution.BaselineReviewResolutionContinue)">接受缺失项并继续</button>
			</footer>
		</section>
	</div>
  </div>
</template>

<style scoped>
.viewed-session { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; margin-bottom: 16px; padding: 12px 16px; font-size: 13px; background: var(--card-blue); border: 1px solid var(--border); border-radius: 8px; }
.viewed-session span { overflow-wrap: anywhere; }
.viewed-session button { padding: 8px 12px; color: var(--navy); background: var(--surface); border: 1px solid var(--border); border-radius: 8px; cursor: pointer; }
.home-workspace { display: grid; gap: 16px; min-width: 0; }
.protection-overview__main { display: flex; gap: 14px; align-items: center; min-width: 0; }
.protection-overview__status { display: grid; flex: 0 0 188px; gap: 4px; padding-left: 18px; border-left: 1px solid var(--border-subtle); }
.protection-overview__status span { color: var(--text-muted); font-size: 12px; }
.protection-overview__status strong { color: var(--navy); font-size: 1.1rem; }
.protection-overview__status small { color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.home-detail-grid { display: grid; grid-template-columns: minmax(0, 1fr) 290px; gap: 16px; align-items: start; min-width: 0; }
.protection-guidance { padding: 18px; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.protection-guidance__heading { padding-bottom: 14px; border-bottom: 1px solid var(--border-subtle); }
.protection-guidance h2 { margin-bottom: 0; }
.protection-guidance dl { display: grid; gap: 14px; margin: 16px 0 0; }
.protection-guidance dl div { display: grid; gap: 4px; }
.protection-guidance dt { color: var(--navy); font-size: 13px; font-weight: 700; }
.protection-guidance dd { margin: 0; color: var(--text-secondary); font-size: 12px; line-height: 1.65; }
.monitoring-level-control { display: grid; gap: 3px; color: var(--text-muted); font-size: 10px; font-weight: 700; }
.monitoring-level-select { min-height: 32px; padding: 5px 24px 5px 8px; color: var(--navy); font-size: 12px; background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.start-preview-backdrop { position: fixed; z-index: 30; inset: 0; display: grid; padding: 28px; place-items: center; background: rgb(15 29 44 / 52%); }
.start-preview-dialog { width: min(660px, 100%); max-height: calc(100vh - 56px); overflow: auto; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: 0 18px 42px rgb(15 29 44 / 22%); }
.start-preview-dialog header { padding: 18px 20px 14px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); }
.start-preview-dialog h2 { margin: 4px 0 0; color: var(--navy); font-size: 1.2rem; }
.start-preview-impact { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; margin: 18px 20px; }
.start-preview-impact div { padding: 10px; background: #f8fafc; border: 1px solid var(--border-subtle); border-radius: 7px; }
.start-preview-impact dt { color: var(--text-muted); font-size: 11px; }
.start-preview-impact dd { margin: 5px 0 0; color: var(--navy); font-size: 14px; font-weight: 700; }
.strict-impact-note { margin: 0 20px 16px; padding: 10px 12px; color: #6b4d0b; font-size: 12px; line-height: 1.6; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 7px; }
.start-preview-targets { display: grid; gap: 6px; max-height: 180px; margin: 0 20px 18px; padding: 0; overflow: auto; list-style: none; }
.start-preview-targets li { display: grid; grid-template-columns: 92px minmax(0, 1fr); gap: 8px; padding: 7px 9px; font-size: 12px; background: #f8fafc; border: 1px solid var(--border-subtle); border-radius: 6px; }
.start-preview-targets span { color: var(--text-muted); }
.start-preview-targets strong { overflow-wrap: anywhere; color: var(--text-secondary); }
.start-preview-dialog footer { display: flex; gap: 8px; justify-content: flex-end; padding: 12px 20px; background: #f8fafc; border-top: 1px solid var(--border-subtle); }
.start-preview-dialog footer button { min-height: 34px; padding: 7px 13px; color: var(--navy); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; cursor: pointer; }
.start-preview-dialog footer .confirm-start { color: #fff; background: var(--brand); border-color: var(--brand); }
.baseline-review-copy { margin: 16px 20px 10px; color: var(--text-secondary); font-size: 12px; line-height: 1.65; }
.baseline-review-failures { display: grid; gap: 7px; max-height: 280px; margin: 0 20px 18px; padding: 0; overflow: auto; list-style: none; }
.baseline-review-failures li { display: grid; grid-template-columns: minmax(110px, .4fr) minmax(0, 1fr); gap: 10px; padding: 9px 10px; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 6px; }
.baseline-review-failures strong { overflow-wrap: anywhere; color: #664d0d; font-size: 12px; }
.baseline-review-failures span { overflow-wrap: anywhere; color: #735712; font-size: 12px; line-height: 1.5; }
@media (max-width: 960px) {
  .home-detail-grid { grid-template-columns: 1fr; }
  .protection-overview__status { grid-template-columns: auto 1fr; align-items: center; padding-top: 12px; padding-left: 0; border-top: 1px solid var(--border-subtle); border-left: 0; }
  .protection-overview__status span { grid-column: 1 / -1; }
}
@media (max-width: 768px) {
  .protection-overview__main { align-items: flex-start; }
}
</style>
