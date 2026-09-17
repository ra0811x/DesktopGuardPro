<script setup lang="ts">
import { computed } from "vue";

import type { Session } from "../bindings/desktopguardpro/internal/domain";
import type { Evaluation } from "../bindings/desktopguardpro/internal/risk";
import type { EventRecord } from "../bindings/desktopguardpro/internal/storage";
import { deriveAnalysisSummary, riskLevelClass, riskLevelLabel } from "./riskPresentation";

const props = defineProps<{
  session: Session;
  viewingHistory: boolean;
  evaluation: Evaluation | null;
  timeline: EventRecord[];
  integrityVerified: boolean;
  loading: boolean;
}>();

const emit = defineEmits<{
  openTimeline: [];
  openRisk: [];
  openReport: [];
}>();

const stateLabels: Record<string, string> = {
  draft: "草稿",
  preparing: "准备中",
  active: "保护中",
  degraded: "降级保护",
  finalizing: "正在结束",
  completed: "已完成",
  failed: "失败",
};
const summary = computed(() => deriveAnalysisSummary(props.evaluation, props.timeline));
const visibleEvents = computed(() => props.timeline.slice(0, 5));
const analysisState = computed(() => {
  if (props.loading) return "正在读取会话分析。";
  if (!props.evaluation) return "会话分析尚未获取。";
  if (summary.value.findingCount === 0) return "当前已完成分析，没有风险提示。";
  return `已识别 ${summary.value.findingCount} 条风险提示，最高风险为${riskLevelLabel(summary.value.highestRisk)}。`;
});

function eventActionLabel(action: string): string {
  if (!action) return "未提供操作";
  return action.replaceAll("_", " ");
}

function eventCategoryLabel(category: string): string {
  return {
    file: "文件",
    process: "进程",
    software: "软件",
    system: "系统",
    device: "设备",
    health: "健康",
  }[category] ?? (category || "未分类");
}

function eventObjectLabel(record: EventRecord): string {
  return record.Event.objectKey || "未提供对象";
}

function eventTimeLabel(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "未提供时间" : date.toLocaleString("zh-CN", { hour12: false });
}
</script>

<template>
  <section class="session-summary-workspace" aria-labelledby="session-summary-title">
    <header class="summary-header">
      <div>
        <span class="panel-kicker">{{ viewingHistory ? "历史会话" : "当前会话" }}</span>
        <h2 id="session-summary-title">{{ session.Name }}</h2>
        <p>{{ viewingHistory ? "正在查看历史会话的已加载审计结果。" : "保护期间的已加载审计结果会显示在此处。" }}</p>
      </div>
      <div class="session-identity">
        <span :class="`session-state session-state--${session.State}`">{{ stateLabels[session.State] ?? session.State }}</span>
        <small>修订 {{ session.Revision }}</small>
      </div>
    </header>

    <div class="summary-metrics" aria-label="会话摘要指标">
      <article class="summary-metric">
        <span>会话事件</span>
        <strong>{{ summary.eventCount ?? timeline.length }}</strong>
        <small>{{ summary.eventCount === null ? "当前已加载事件" : "服务评估事件总数" }}</small>
      </article>
      <article class="summary-metric summary-metric--risk">
        <span>风险提示</span>
        <strong>{{ summary.findingCount }}</strong>
        <small :class="riskLevelClass(summary.highestRisk)">最高风险：{{ riskLevelLabel(summary.highestRisk) }}</small>
      </article>
      <article class="summary-metric">
        <span>采集缺口</span>
        <strong>{{ summary.coverageGapCount ?? "未获取" }}</strong>
        <small>{{ summary.coverageGapCount ? "查看风险分析中的覆盖说明" : "当前未发现已报告缺口" }}</small>
      </article>
      <article class="summary-metric summary-metric--integrity">
        <span>页面完整性</span>
        <strong>{{ integrityVerified ? "已验证" : "等待验证" }}</strong>
        <small>{{ integrityVerified ? "当前时间线页面通过完整性检查" : "重新读取后会更新校验结果" }}</small>
      </article>
    </div>

    <div class="summary-content-grid">
      <section class="summary-analysis-card" aria-labelledby="analysis-state-title">
        <header><span class="panel-kicker">分析状态</span><h3 id="analysis-state-title">风险与采集覆盖</h3></header>
        <p class="summary-analysis-state">{{ analysisState }}</p>
        <ul v-if="evaluation?.findings.length" class="summary-finding-list">
          <li v-for="finding in evaluation.findings.slice(0, 3)" :key="finding.id">
            <span :class="riskLevelClass(finding.level)">{{ riskLevelLabel(finding.level) }}风险</span>
            <strong>{{ finding.title }}</strong>
          </li>
        </ul>
        <p v-if="summary.coverageGapCount" class="coverage-note">本会话存在 {{ summary.coverageGapCount }} 个采集缺口，查看风险分析可阅读关联说明。</p>
      </section>

      <section class="summary-events-card" aria-labelledby="summary-events-title">
        <header><span class="panel-kicker">已加载事件</span><h3 id="summary-events-title">会话证据</h3></header>
        <div v-if="visibleEvents.length" class="summary-event-list">
          <article v-for="record in visibleEvents" :key="record.Event.eventId">
            <span>#{{ record.Event.sequence }}</span>
            <div><strong>{{ eventActionLabel(record.Event.action) }}</strong><small>{{ eventObjectLabel(record) }}</small></div>
            <time>{{ eventTimeLabel(record.Event.observedUtc) }}</time>
            <em>{{ eventCategoryLabel(record.Event.category) }}</em>
          </article>
        </div>
        <p v-else class="summary-event-empty">当前页面尚未加载事件。读取时间线后会显示会话证据。</p>
      </section>
    </div>

    <footer class="summary-actions" aria-label="会话分析操作">
      <button type="button" @click="emit('openTimeline')">查看事件时间线</button>
      <button type="button" @click="emit('openRisk')">查看风险分析</button>
      <button type="button" @click="emit('openReport')">导出会话报告</button>
    </footer>
  </section>
</template>

<style scoped>
.session-summary-workspace { display: grid; gap: 16px; min-width: 0; }
.summary-header { display: flex; gap: 18px; align-items: flex-start; justify-content: space-between; min-height: 112px; padding: 20px 22px; background: var(--surface); border: 1px solid var(--border); border-left: 4px solid var(--interactive); border-radius: 8px; }
.panel-kicker { display: block; margin-bottom: 4px; color: var(--text-muted); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.summary-header h2, h3 { margin: 0; color: var(--navy); }
.summary-header h2 { font-size: 1.2rem; }
.summary-header p { margin: 8px 0 0; color: var(--text-secondary); font-size: 13px; line-height: 1.6; }
.session-identity { display: grid; flex: 0 0 auto; gap: 7px; justify-items: end; }
.session-identity small { color: var(--text-muted); font-size: 11px; }
.session-state { padding: 5px 8px; color: var(--text-secondary); font-size: 12px; font-weight: 700; background: var(--control-blue); border: 1px solid var(--border); border-radius: 6px; }
.session-state--active { color: #165d3a; background: #edf8f2; border-color: #a9d4bc; }
.session-state--degraded { color: #735712; background: #fff9e8; border-color: #dfcc91; }
.session-state--failed { color: #852a35; background: #fff2f3; border-color: #e5b7bd; }
.summary-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
.summary-metric { display: grid; gap: 7px; min-height: 116px; padding: 16px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
.summary-metric > span { color: var(--text-secondary); font-size: 12px; font-weight: 700; }
.summary-metric strong { color: var(--navy); font-size: 1.55rem; line-height: 1.15; }
.summary-metric small { color: var(--text-muted); font-size: 11px; line-height: 1.55; }
.summary-metric--risk small.risk-high, .summary-metric--risk small.risk-critical { color: #852a35; }
.summary-metric--risk small.risk-medium { color: #735712; }
.summary-metric--risk small.risk-low, .summary-metric--risk small.risk-informational { color: #165d3a; }
.summary-content-grid { display: grid; grid-template-columns: minmax(260px, .78fr) minmax(0, 1.22fr); gap: 16px; }
.summary-analysis-card, .summary-events-card { min-width: 0; padding: 18px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
.summary-analysis-card header, .summary-events-card header { padding-bottom: 13px; border-bottom: 1px solid var(--border-subtle); }
h3 { font-size: 1rem; }
.summary-analysis-state { margin: 14px 0; color: var(--text-secondary); font-size: 13px; line-height: 1.65; }
.summary-finding-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
.summary-finding-list li { display: flex; gap: 8px; align-items: center; padding: 8px 0; border-top: 1px solid var(--border-subtle); }
.summary-finding-list li:first-child { border-top: 0; }
.summary-finding-list span { flex: 0 0 auto; padding: 4px 6px; font-size: 10px; font-weight: 700; border-radius: 4px; }
.summary-finding-list strong { overflow: hidden; color: var(--navy); font-size: 12px; text-overflow: ellipsis; white-space: nowrap; }
.risk-none { color: #405d7e; background: #eef3f8; }
.risk-informational, .risk-low { color: #165d3a; background: #edf8f2; }
.risk-medium { color: #735712; background: #fff9e8; }
.risk-high, .risk-critical { color: #852a35; background: #fff2f3; }
.coverage-note { margin: 14px 0 0; padding: 10px; color: #735712; font-size: 12px; line-height: 1.55; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 6px; }
.summary-event-list { display: grid; margin-top: 14px; border: 1px solid var(--border-subtle); border-radius: 7px; overflow: hidden; }
.summary-event-list article { display: grid; grid-template-columns: 42px minmax(0, 1fr) auto auto; gap: 10px; align-items: center; min-height: 54px; padding: 8px 10px; border-bottom: 1px solid var(--border-subtle); }
.summary-event-list article:last-child { border-bottom: 0; }
.summary-event-list article > span { color: var(--text-muted); font-size: 11px; font-weight: 700; }
.summary-event-list div { display: grid; gap: 3px; min-width: 0; }
.summary-event-list strong { color: var(--navy); font-size: 12px; }
.summary-event-list small { overflow: hidden; color: var(--text-secondary); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.summary-event-list time, .summary-event-list em { color: var(--text-muted); font-size: 11px; font-style: normal; white-space: nowrap; }
.summary-event-list em { padding: 4px 6px; background: var(--control-blue); border: 1px solid var(--border); border-radius: 4px; }
.summary-event-empty { margin: 14px 0 0; padding: 32px 16px; color: var(--text-muted); font-size: 12px; line-height: 1.6; text-align: center; background: #f8fafc; border: 1px dashed var(--border); border-radius: 7px; }
.summary-actions { display: flex; gap: 10px; justify-content: flex-end; padding: 14px 16px; background: var(--card-blue); border: 1px solid var(--border); border-radius: 8px; }
.summary-actions button { min-height: 36px; padding: 8px 12px; color: var(--navy); font-size: 12px; font-weight: 700; background: var(--surface); border: 1px solid var(--border); border-radius: 6px; cursor: pointer; }
.summary-actions button:last-child { color: #fff; background: var(--brand); border-color: var(--brand); }
@media (max-width: 1020px) { .summary-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); } .summary-content-grid { grid-template-columns: 1fr; } }
@media (max-width: 640px) { .summary-header { display: grid; } .session-identity { justify-items: start; } .summary-metrics { grid-template-columns: 1fr; } .summary-event-list article { grid-template-columns: 36px minmax(0, 1fr); } .summary-event-list time, .summary-event-list em { grid-column: 2; } .summary-actions { align-items: stretch; flex-direction: column; } }
</style>
