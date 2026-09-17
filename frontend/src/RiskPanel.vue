<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { FindingStatus, type Evaluation, type Finding } from "../bindings/desktopguardpro/internal/risk";
import type { EventRecord } from "../bindings/desktopguardpro/internal/storage";
import {
  actionLabel,
  categoryLabel,
  displayObject,
  formatTimelineTime,
} from "./timelinePresentation";
import {
  confidenceLabel,
  deriveAnalysisSummary,
  evidenceLabel,
	formatRawEventPayload,
  riskLevelClass,
  riskLevelLabel,
} from "./riskPresentation";

const props = defineProps<{
  evaluation: Evaluation | null;
  timeline: EventRecord[];
  loading: boolean;
	updatingFindingID?: string;
}>();

const emit = defineEmits<{
	updateFindingStatus: [findingID: string, status: FindingStatus];
}>();

const selectedFindingID = ref("");
const summary = computed(() => deriveAnalysisSummary(props.evaluation, props.timeline));
const selectedFinding = computed(() => props.evaluation?.findings.find((finding) => finding.id === selectedFindingID.value));
const evidence = computed(() => (selectedFinding.value?.evidence ?? []).map((reference) => ({
  reference,
  record: props.timeline.find((record) => record.Event.eventId === reference.eventId || record.Event.sequence === reference.sequence),
})));

watch(() => props.evaluation?.findings, (findings) => {
  if (findings?.some((finding) => finding.id === selectedFindingID.value)) return;
  selectedFindingID.value = "";
});

function selectFinding(finding: Finding): void {
  selectedFindingID.value = finding.id;
}

const findingStatusOptions = [
	{ value: FindingStatus.FindingStatusKnown, label: "已知" },
	{ value: FindingStatus.FindingStatusPendingReview, label: "待确认" },
	{ value: FindingStatus.FindingStatusActionNeeded, label: "需处理" },
] as const;

function findingStatusLabel(status: FindingStatus): string {
	return findingStatusOptions.find((option) => option.value === status)?.label ?? "待确认";
}
</script>

<template>
  <section class="risk-workspace" aria-labelledby="risk-title">
    <header class="risk-command-strip">
      <div><span class="panel-kicker">会话分析</span><h2 id="risk-title">风险与覆盖</h2></div>
      <span v-if="summary.coverageGapCount" class="coverage-indicator">{{ summary.coverageGapCount }} 个采集缺口</span>
    </header>

    <div class="risk-body">
      <div class="risk-findings-pane">
        <p class="pane-description">风险提示用于缩小复核范围，结论需要结合设备使用场景判断。</p>
        <p v-if="loading" class="risk-empty">正在评估会话事件。</p>
        <div v-else-if="evaluation?.findings.length" class="finding-list">
          <article
            v-for="finding in evaluation.findings"
            :key="finding.id"
            class="finding-card"
            role="button"
            tabindex="0"
            :class="{ selected: selectedFindingID === finding.id }"
            :aria-selected="selectedFindingID === finding.id"
            @click="selectFinding(finding)"
            @keydown.enter="selectFinding(finding)"
            @keydown.space.prevent="selectFinding(finding)"
          >
            <div class="finding-heading">
              <span class="risk-level" :class="riskLevelClass(finding.level)">
                {{ riskLevelLabel(finding.level) }}风险
              </span>
              <strong>{{ finding.title }}</strong>
              <span class="score">评分 {{ finding.score }}</span>
            </div>
            <p>{{ finding.summary }}</p>
            <footer>
              <span>置信度 {{ confidenceLabel(finding.confidence) }}</span>
              <span>证据 {{ evidenceLabel(finding) }}</span>
              <span>规则 {{ finding.ruleId }}</span>
			  <span>处置 {{ findingStatusLabel(finding.status) }}</span>
            </footer>
          </article>
        </div>
        <p v-else class="risk-empty">{{ evaluation ? "当前会话没有风险发现。" : "会话风险结果尚未获取。" }}</p>

        <div v-if="evaluation?.failures?.length" class="rule-failures" role="status">
          <strong>部分规则执行异常</strong>
          <ul>
            <li v-for="failure in evaluation.failures" :key="failure.ruleId">
              {{ failure.ruleId }}：{{ failure.message }}
            </li>
          </ul>
        </div>
      </div>

      <aside class="risk-evidence-pane" aria-label="风险证据详情">
        <template v-if="selectedFinding">
          <div class="evidence-heading">
            <span class="panel-kicker">正在审阅</span>
            <strong>{{ selectedFinding.title }}</strong>
            <span>规则 {{ selectedFinding.ruleId }} · 置信度 {{ confidenceLabel(selectedFinding.confidence) }}</span>
          </div>
		  <div class="finding-status-actions" aria-label="风险处置状态">
			<span>处置状态</span>
			<button
				v-for="option in findingStatusOptions"
				:key="option.value"
				type="button"
				:class="{ active: selectedFinding.status === option.value }"
				:aria-pressed="selectedFinding.status === option.value"
				:disabled="updatingFindingID === selectedFinding.id"
				@click="emit('updateFindingStatus', selectedFinding.id, option.value)"
			>{{ option.label }}</button>
		  </div>
          <div v-if="evidence.length" class="evidence-list">
            <article v-for="item in evidence" :key="item.reference.eventId || item.reference.sequence" class="evidence-item">
              <div class="evidence-item__heading">
                <strong>#{{ item.reference.sequence }}</strong>
                <span :class="{ loaded: item.record }">{{ item.record ? "已加载" : "尚未加载" }}</span>
              </div>
              <template v-if="item.record">
                <p>{{ categoryLabel(item.record.Event.category) }} · {{ actionLabel(item.record.Event.action) }}</p>
                <dl>
                  <div><dt>对象</dt><dd>{{ displayObject(item.record.Event) }}</dd></div>
                  <div><dt>来源</dt><dd>{{ item.record.Event.source || "未提供" }}</dd></div>
                  <div><dt>观测时间</dt><dd>{{ formatTimelineTime(item.record.Event.observedUtc) }}</dd></div>
                </dl>
				<details class="raw-evidence">
					<summary>查看原始字段</summary>
					<pre>{{ formatRawEventPayload(item.record.Payload) }}</pre>
					<span v-if="item.record.previewTruncated">当前预览已截断，完整载荷仍保存在本地数据库和报告中。</span>
				</details>
              </template>
              <template v-else>
                <p>{{ categoryLabel(item.reference.category) }} · {{ actionLabel(item.reference.action) }}</p>
                <p class="evidence-pending">关联事件未包含在当前已加载的时间线页面中。事件序号 #{{ item.reference.sequence }} 仍可作为复核定位依据。</p>
              </template>
            </article>
          </div>
          <p v-else class="evidence-empty">该风险提示没有返回可展示的事件引用。</p>
        </template>
        <div v-else class="evidence-empty">
          <strong>选择一条风险提示</strong>
          <p>选择风险项后，可在此查看规则依据和已加载的关联事件。</p>
        </div>
      </aside>

      <aside class="risk-summary-pane" aria-label="风险摘要检查器">
        <div class="summary-heading">
          <strong>会话摘要</strong>
          <span class="highest-risk" :class="riskLevelClass(summary.highestRisk)">
            最高风险：{{ riskLevelLabel(summary.highestRisk) }}
          </span>
        </div>
        <div class="summary-grid" aria-label="会话摘要指标">
          <article><span>会话事件总数</span><strong>{{ summary.eventCount ?? "未获取" }}</strong></article>
          <article><span>风险发现</span><strong>{{ summary.findingCount }}</strong></article>
          <article><span>会话采集缺口</span><strong>{{ summary.coverageGapCount ?? "未获取" }}</strong></article>
          <article><span>规则故障</span><strong>{{ summary.ruleFailureCount }}</strong></article>
        </div>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.risk-workspace { display: grid; grid-template-rows: auto minmax(0, 1fr); min-width: 0; min-height: 500px; overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.risk-command-strip { display: flex; gap: 14px; align-items: center; justify-content: space-between; min-height: 66px; padding: 12px 16px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); }
.panel-kicker { display: block; margin-bottom: 3px; color: var(--text-muted); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.risk-command-strip h2 { margin: 0; color: var(--navy); font-size: 1.05rem; }
.coverage-indicator { padding: 6px 9px; color: #735712; font-size: 12px; font-weight: 700; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 6px; }
.risk-body { display: grid; grid-template-columns: minmax(280px, 1fr) minmax(260px, .82fr) 230px; min-width: 0; }
.risk-findings-pane { min-width: 0; padding: 14px; }
.pane-description { margin: 0 0 12px; color: var(--text-muted); font-size: 12px; line-height: 1.6; }
.finding-list { overflow: hidden; border: 1px solid var(--border); border-radius: 7px; }
.finding-card { padding: 13px 14px; background: var(--surface); border: 0; border-bottom: 1px solid var(--border-subtle); cursor: pointer; transition: background-color 160ms ease; }
.finding-card:last-child { border-bottom: 0; }
.finding-card:hover, .finding-card.selected { background: var(--selected-blue); }
.finding-card.selected { box-shadow: inset 3px 0 0 var(--interactive); }
.finding-card:focus-visible { position: relative; z-index: 1; outline: 2px solid var(--interactive); outline-offset: -2px; }
.finding-heading { display: flex; gap: 10px; align-items: center; }
.finding-heading strong { color: var(--navy); font-size: 13px; }
.finding-heading .score { margin-left: auto; color: var(--text-secondary); font-size: 12px; font-weight: 600; }
.highest-risk, .risk-level { display: inline-block; padding: 5px 8px; font-size: 12px; font-weight: 700; border: 1px solid transparent; border-radius: 6px; }
.risk-none { color: #405d7e; background: #eef3f8; border-color: #c9d6e4; }
.risk-informational, .risk-low { color: #165d3a; background: #edf8f2; border-color: #a9d4bc; }
.risk-medium { color: #735712; background: #fff9e8; border-color: #dfcc91; }
.risk-high, .risk-critical { color: #852a35; background: #fff2f3; border-color: #e5b7bd; }
.finding-card p { margin: 10px 0; color: var(--text-secondary); font-size: 13px; line-height: 1.6; }
.finding-card footer { display: flex; flex-wrap: wrap; gap: 7px; color: var(--text-muted); font-size: 11px; }
.finding-card footer span { padding: 4px 7px; background: var(--control-blue); border: 1px solid var(--border); border-radius: 5px; }
.risk-empty, .evidence-empty { margin: 0; padding: 38px 18px; color: var(--text-muted); font-size: 13px; line-height: 1.65; text-align: center; background: #f8fafc; border: 1px dashed var(--border); border-radius: 7px; }
.rule-failures { margin-top: 14px; padding: 12px 14px; color: #735712; font-size: 12px; line-height: 1.55; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 7px; }
.rule-failures ul { margin: 7px 0 0; padding-left: 18px; }
.risk-evidence-pane { min-width: 0; padding: 14px; background: #f8fafc; border-right: 1px solid var(--border-subtle); border-left: 1px solid var(--border-subtle); }
.evidence-heading { display: grid; gap: 5px; padding-bottom: 13px; border-bottom: 1px solid var(--border-subtle); }
.evidence-heading strong { color: var(--navy); font-size: 14px; line-height: 1.45; }
.evidence-heading > span:last-child { color: var(--text-muted); font-size: 11px; line-height: 1.5; }
.finding-status-actions { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin-top: 12px; padding-bottom: 12px; border-bottom: 1px solid var(--border-subtle); }
.finding-status-actions > span { margin-right: 2px; color: var(--text-muted); font-size: 11px; font-weight: 700; }
.finding-status-actions button { padding: 5px 8px; color: var(--navy); font-size: 11px; font-weight: 700; background: var(--surface); border: 1px solid var(--border); border-radius: 5px; cursor: pointer; }
.finding-status-actions button.active { color: #fff; background: var(--interactive); border-color: var(--interactive); }
.finding-status-actions button:disabled { cursor: wait; opacity: .62; }
.evidence-list { display: grid; gap: 9px; margin-top: 14px; }
.evidence-item { padding: 10px; background: var(--surface); border: 1px solid var(--border); border-radius: 7px; }
.evidence-item__heading { display: flex; gap: 8px; align-items: center; justify-content: space-between; }
.evidence-item__heading strong { color: var(--navy); font-size: 13px; }
.evidence-item__heading span { padding: 3px 6px; color: #735712; font-size: 10px; font-weight: 700; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 4px; }
.evidence-item__heading span.loaded { color: #165d3a; background: #edf8f2; border-color: #a9d4bc; }
.evidence-item > p { margin: 8px 0; color: var(--text-secondary); font-size: 12px; line-height: 1.5; }
.evidence-item dl { display: grid; gap: 6px; margin: 0; }
.evidence-item dl div { display: grid; gap: 2px; }
.evidence-item dt { color: var(--text-muted); font-size: 10px; }
.evidence-item dd { margin: 0; overflow-wrap: anywhere; color: var(--navy); font-size: 11px; line-height: 1.45; }
.evidence-pending { margin-bottom: 0 !important; color: #735712 !important; }
.raw-evidence { margin-top: 9px; padding-top: 8px; border-top: 1px solid var(--border-subtle); }
.raw-evidence summary { color: var(--interactive); font-size: 11px; font-weight: 700; cursor: pointer; }
.raw-evidence pre { max-height: 180px; margin: 8px 0 0; padding: 8px; overflow: auto; color: var(--navy); font: 11px/1.55 Consolas, monospace; white-space: pre-wrap; overflow-wrap: anywhere; background: #f3f6f9; border: 1px solid var(--border-subtle); border-radius: 5px; }
.raw-evidence > span { display: block; margin-top: 6px; color: #735712; font-size: 10px; line-height: 1.5; }
.risk-summary-pane { padding: 14px; background: var(--surface); }
.summary-heading { display: grid; gap: 8px; margin-bottom: 12px; color: var(--navy); font-size: 12px; }
.summary-grid { display: grid; grid-template-columns: 1fr; gap: 8px; margin: 0; }
.summary-grid article { display: grid; gap: 7px; min-height: 70px; padding: 10px 11px; background: var(--card-blue); border: 1px solid var(--border); border-radius: 7px; }
.summary-grid span { color: var(--text-secondary); font-size: 11px; font-weight: 600; }
.summary-grid strong { color: var(--navy); font-size: 1.4rem; line-height: 1.2; }
@media (max-width: 1180px) { .risk-body { grid-template-columns: minmax(0, 1fr) 280px; } .risk-summary-pane { grid-column: 1 / -1; border-top: 1px solid var(--border-subtle); } .summary-grid { grid-template-columns: repeat(4, minmax(120px, 1fr)); } }
@media (max-width: 840px) { .risk-body { grid-template-columns: 1fr; } .risk-evidence-pane { border-top: 1px solid var(--border-subtle); border-right: 0; border-left: 0; } .summary-grid { grid-template-columns: repeat(2, minmax(120px, 1fr)); } }
@media (max-width: 560px) { .risk-command-strip { display: grid; } .coverage-indicator { justify-self: start; } .finding-heading { flex-wrap: wrap; } .finding-heading .score { flex: 1 1 100%; margin-left: 0; } .summary-grid { grid-template-columns: 1fr; } }
</style>
