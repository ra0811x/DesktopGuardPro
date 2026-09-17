<script setup lang="ts">
import { computed, ref, watch } from "vue";

import { EventSeverity, type EventCategory } from "../bindings/desktopguardpro/internal/domain";
import type { EventRecord } from "../bindings/desktopguardpro/internal/storage";
import {
  actionLabel,
  categoryLabel,
  displayObject,
  formatTimelineTime,
  localInputToUTC,
  timelineCategories,
  utcToLocalInput,
} from "./timelinePresentation";

const props = defineProps<{
  records: EventRecord[];
  categories: EventCategory[];
	severities: EventSeverity[];
	user: string;
	process: string;
	path: string;
  fromUtc: string;
  toUtc: string;
  loading: boolean;
  hasMore: boolean;
  integrityVerified: boolean;
  errorMessage: string;
}>();

const emit = defineEmits<{
  refresh: [];
  loadMore: [];
  toggleCategory: [category: EventCategory];
	toggleSeverity: [severity: EventSeverity];
	updateUser: [value: string];
	updateProcess: [value: string];
	updatePath: [value: string];
  updateFromUtc: [value: string];
  updateToUtc: [value: string];
}>();

const selectedRecord = ref<EventRecord>();
const filterSummary = computed(() => {
  const conditions: string[] = [];
  if (props.categories.length > 0) conditions.push(`${props.categories.length} 个类别`);
	if (props.severities.length > 0) conditions.push(`${props.severities.length} 个风险级别`);
	if (props.user) conditions.push("用户");
	if (props.process) conditions.push("进程");
	if (props.path) conditions.push("路径");
  if (props.fromUtc || props.toUtc) conditions.push("时间范围");
  return conditions.length > 0 ? conditions.join("、") : "全部事件";
});

watch(() => props.records, (records) => {
  if (!selectedRecord.value) return;
  if (!records.some((record) => record.Event.eventId === selectedRecord.value?.Event.eventId)) {
    selectedRecord.value = undefined;
  }
});

function dateInput(event: Event): string {
  return (event.target as HTMLInputElement).value;
}

function textInput(event: Event): string {
	return (event.target as HTMLInputElement).value;
}

const severityOptions = [
	{ value: EventSeverity.EventSeverityHigh, label: "高风险" },
	{ value: EventSeverity.EventSeverityMedium, label: "中风险" },
	{ value: EventSeverity.EventSeverityLow, label: "低风险" },
] as const;

function selectRecord(record: EventRecord): void {
  selectedRecord.value = record;
}

function severityLabel(value: string): string {
  return { high: "高风险", medium: "中风险", low: "低风险" }[value] ?? "未评级";
}

function confidenceLabel(value: string): string {
  return {
    direct: "直接观测",
    correlated: "关联推断",
    snapshot_diff: "基线差异",
  }[value] ?? "未标记";
}
</script>

<template>
  <section class="timeline-workspace" aria-labelledby="timeline-title">
    <header class="timeline-command-strip">
      <div>
        <span class="panel-kicker">审计证据</span>
        <h2 id="timeline-title">事件时间线</h2>
        <span>按类别和时间检查会话内的已采集事件。</span>
      </div>
      <span class="integrity-badge" :class="{ verified: integrityVerified }">
        {{ integrityVerified ? "当前扫描页完整性已验证" : "等待本页完整性验证" }}
      </span>
    </header>

    <div class="timeline-body">
      <aside class="timeline-filter-pane" aria-label="时间线筛选">
        <div class="filter-heading">
          <strong class="filter-title">筛选条件</strong>
          <span>{{ filterSummary }}</span>
        </div>
        <div class="category-filter">
          <button
            v-for="category in timelineCategories"
            :key="category"
            type="button"
            :class="{ selected: categories.includes(category) }"
            :aria-pressed="categories.includes(category)"
            @click="emit('toggleCategory', category)"
          >
            {{ categoryLabel(category) }}
          </button>
        </div>
		<div class="severity-filter" aria-label="风险级别筛选">
		  <button
			v-for="option in severityOptions"
			:key="option.value"
			type="button"
			:class="{ selected: severities.includes(option.value) }"
			:aria-pressed="severities.includes(option.value)"
			@click="emit('toggleSeverity', option.value)"
		  >{{ option.label }}</button>
		</div>
		<label>用户<input class="user-filter" type="text" :value="user" placeholder="用户名" @input="emit('updateUser', textInput($event))"></label>
		<label>进程<input class="process-filter" type="text" :value="process" placeholder="进程名或路径" @input="emit('updateProcess', textInput($event))"></label>
		<label>路径<input class="path-filter" type="text" :value="path" placeholder="文件或目录路径" @input="emit('updatePath', textInput($event))"></label>
        <label>
          开始时间
          <input
            type="datetime-local"
            :value="utcToLocalInput(fromUtc)"
            @change="emit('updateFromUtc', localInputToUTC(dateInput($event)))"
          >
        </label>
        <label>
          结束时间
          <input
            type="datetime-local"
            :value="utcToLocalInput(toUtc)"
            @change="emit('updateToUtc', localInputToUTC(dateInput($event)))"
          >
        </label>
        <button class="refresh-button" type="button" :disabled="loading" @click="emit('refresh')">
          {{ loading ? "正在读取" : "应用筛选" }}
        </button>
      </aside>

      <div class="timeline-results-pane">
        <p v-if="errorMessage" class="timeline-error" role="alert">{{ errorMessage }}</p>
        <div class="timeline-table-wrap" role="region" aria-label="会话事件数据表" tabindex="0">
          <table v-if="records.length" class="timeline-table">
            <thead><tr><th>序列</th><th>时间</th><th>类别</th><th>动作</th><th>对象</th><th>来源</th></tr></thead>
            <tbody>
              <tr
                v-for="record in records"
                :key="record.Event.eventId"
                role="button"
                tabindex="0"
                :class="{ selected: selectedRecord?.Event.eventId === record.Event.eventId }"
                :aria-selected="selectedRecord?.Event.eventId === record.Event.eventId"
                @click="selectRecord(record)"
                @keydown.enter="selectRecord(record)"
                @keydown.space.prevent="selectRecord(record)"
              >
                <td>#{{ record.Event.sequence }}</td>
                <td>{{ formatTimelineTime(record.Event.observedUtc) }}</td>
                <td><span class="category-pill">{{ categoryLabel(record.Event.category) }}</span></td>
                <td>{{ actionLabel(record.Event.action) }}</td>
                <td class="object-cell" :title="record.Event.objectKey">{{ displayObject(record.Event) }}</td>
                <td>{{ record.Event.source }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="timeline-empty">{{ loading ? "正在读取会话事件，请稍候。" : (hasMore ? "当前扫描页没有匹配事件，可继续加载更早记录。" : "当前筛选范围没有事件。") }}</p>
        </div>
        <footer class="timeline-status-bar">
          <span>已显示 {{ records.length }} 条事件</span>
          <span v-if="selectedRecord">已选择 #{{ selectedRecord.Event.sequence }}</span>
          <button v-if="hasMore" class="load-more" type="button" :disabled="loading" @click="emit('loadMore')">
            {{ loading ? "正在加载" : "加载更多事件" }}
          </button>
        </footer>
      </div>

      <aside class="timeline-detail-pane" aria-label="事件详情">
        <template v-if="selectedRecord">
          <div class="detail-heading">
            <span class="panel-kicker">已选择事件</span>
            <strong>#{{ selectedRecord.Event.sequence }}</strong>
            <span class="detail-category">{{ categoryLabel(selectedRecord.Event.category) }}</span>
          </div>
          <dl class="detail-list">
            <div><dt>观测时间</dt><dd>{{ formatTimelineTime(selectedRecord.Event.observedUtc) }}</dd></div>
            <div><dt>动作</dt><dd>{{ actionLabel(selectedRecord.Event.action) }}</dd></div>
            <div><dt>对象</dt><dd class="detail-break">{{ selectedRecord.Event.objectKey || "未提供" }}</dd></div>
            <div><dt>来源</dt><dd>{{ selectedRecord.Event.source || "未提供" }}</dd></div>
            <div><dt>风险级别</dt><dd>{{ severityLabel(selectedRecord.Event.severity) }}</dd></div>
            <div><dt>观测方式</dt><dd>{{ confidenceLabel(selectedRecord.Event.confidence) }}</dd></div>
            <div><dt>进程关联</dt><dd class="detail-break">{{ selectedRecord.Event.processKey || "未提供" }}</dd></div>
            <div><dt>页面完整性</dt><dd>{{ integrityVerified ? "已验证" : "等待验证" }}</dd></div>
          </dl>
          <p class="detail-note">事件详情仅展示服务已返回的审计字段，原始负载不会在此页面直接显示。</p>
        </template>
        <div v-else class="detail-empty">
          <strong>选择一条事件</strong>
          <p>选择列表中的记录后，可在此检查其对象、来源、风险级别和观测方式。</p>
        </div>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.timeline-workspace { display: grid; grid-template-rows: auto minmax(0, 1fr); min-width: 0; min-height: 500px; overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.timeline-command-strip { display: flex; gap: 16px; align-items: center; justify-content: space-between; min-height: 70px; padding: 12px 16px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); }
.panel-kicker { display: block; margin-bottom: 3px; color: var(--text-muted); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.timeline-command-strip h2 { margin: 0; color: var(--navy); font-size: 1.05rem; }
.timeline-command-strip div > span:last-child { color: var(--text-muted); font-size: 12px; }
.integrity-badge { flex: 0 0 auto; padding: 6px 9px; color: #735712; font-size: 12px; font-weight: 600; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 6px; }
.integrity-badge.verified { color: #165d3a; background: #edf8f2; border-color: #a9d4bc; }
.timeline-body { display: grid; grid-template-columns: 212px minmax(0, 1fr) 286px; min-width: 0; min-height: 0; }
.timeline-filter-pane { display: grid; align-content: start; gap: 12px; padding: 14px; background: #f8fafc; border-right: 1px solid var(--border-subtle); }
.filter-heading { display: grid; gap: 3px; }
.filter-title { color: var(--navy); font-size: 12px; font-weight: 700; }
.filter-heading span { color: var(--text-muted); font-size: 11px; }
.category-filter { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px; }
.severity-filter { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 5px; }
.category-filter button, .severity-filter button, .refresh-button, .load-more { min-height: 34px; padding: 6px 9px; color: var(--text-secondary); font-size: 12px; font-weight: 600; white-space: nowrap; background: var(--control-blue); border: 1px solid var(--border); border-radius: 6px; }
.category-filter button:not(:disabled), .severity-filter button:not(:disabled), .refresh-button:not(:disabled), .load-more:not(:disabled) { cursor: pointer; }
.category-filter button:hover, .severity-filter button:hover, .load-more:hover { color: var(--navy); background: var(--selected-blue); border-color: #aebfce; }
.category-filter button.selected, .severity-filter button.selected { color: #fff; background: var(--brand); border-color: var(--brand); }
.timeline-filter-pane label { display: grid; gap: 5px; color: var(--text-secondary); font-size: 12px; font-weight: 600; }
.timeline-filter-pane input { width: 100%; min-height: 36px; padding: 8px 9px; color: var(--navy); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.refresh-button { width: 100%; padding-inline: 16px; color: #fff; background: var(--brand); border-color: var(--brand); }
.refresh-button:not(:disabled):hover { background: var(--interactive); border-color: var(--interactive); }
.refresh-button:disabled, .load-more:disabled { cursor: default; opacity: .52; }
.timeline-results-pane { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; min-width: 0; min-height: 0; padding: 12px; }
.timeline-error { margin: 0 0 10px; padding: 10px 12px; color: #852a35; font-size: 13px; background: #fff2f3; border: 1px solid #e5b7bd; border-radius: 6px; }
.timeline-table-wrap { max-height: calc(100vh - 228px); overflow: auto; border: 1px solid var(--border); border-radius: 7px; }
.timeline-table-wrap:focus-visible { outline: 0; border-color: var(--interactive); box-shadow: 0 0 0 3px rgb(11 91 181 / 22%); }
.timeline-table { width: 100%; min-width: 760px; border-collapse: collapse; color: var(--text-secondary); font-size: 12px; }
.timeline-table th, .timeline-table td { height: 44px; padding: 9px 10px; text-align: left; vertical-align: middle; border-bottom: 1px solid var(--border-subtle); }
.timeline-table th { position: sticky; top: 0; z-index: 1; color: var(--navy); font-size: 11px; font-weight: 700; background: var(--card-blue); }
.timeline-table tbody tr { content-visibility: auto; contain-intrinsic-size: 44px; background: var(--surface); cursor: pointer; transition: background-color 160ms ease; }
.timeline-table tbody tr:nth-child(even) { background: #f8fafc; }
.timeline-table tbody tr:hover, .timeline-table tbody tr.selected { background: var(--selected-blue); }
.timeline-table tbody tr.selected { box-shadow: inset 3px 0 0 var(--interactive); }
.timeline-table tbody tr:focus-visible { position: relative; z-index: 2; outline: 2px solid var(--interactive); outline-offset: -2px; }
.timeline-table tbody tr:last-child td { border-bottom: 0; }
.category-pill { display: inline-block; padding: 3px 7px; color: var(--text-secondary); font-weight: 600; background: var(--control-blue); border: 1px solid var(--border); border-radius: 5px; }
.object-cell { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.timeline-empty { margin: 0; padding: 44px 20px; color: var(--text-muted); font-size: 13px; line-height: 1.6; text-align: center; background: #f8fafc; }
.timeline-status-bar { display: flex; gap: 12px; align-items: center; min-height: 38px; padding-top: 7px; color: var(--text-muted); font-size: 11px; }
.timeline-status-bar .load-more { margin-left: auto; }
.timeline-detail-pane { min-width: 0; padding: 16px; background: #f8fafc; border-left: 1px solid var(--border-subtle); }
.detail-heading { display: grid; gap: 6px; padding-bottom: 14px; border-bottom: 1px solid var(--border-subtle); }
.detail-heading strong { color: var(--navy); font-size: 1.35rem; }
.detail-category { width: fit-content; padding: 4px 7px; color: var(--text-secondary); font-size: 11px; font-weight: 700; background: var(--control-blue); border: 1px solid var(--border); border-radius: 5px; }
.detail-list { display: grid; gap: 11px; margin: 16px 0 0; }
.detail-list div { display: grid; gap: 3px; }
.detail-list dt { color: var(--text-muted); font-size: 11px; }
.detail-list dd { margin: 0; color: var(--navy); font-size: 12px; line-height: 1.5; }
.detail-break { overflow-wrap: anywhere; }
.detail-note { margin: 16px 0 0; padding-top: 12px; color: var(--text-muted); font-size: 11px; line-height: 1.6; border-top: 1px solid var(--border-subtle); }
.detail-empty { padding: 28px 4px; color: var(--text-secondary); }
.detail-empty strong { color: var(--navy); font-size: 13px; }
.detail-empty p { margin: 8px 0 0; font-size: 12px; line-height: 1.65; }
@media (max-width: 1120px) { .timeline-body { grid-template-columns: 190px minmax(0, 1fr); } .timeline-detail-pane { grid-column: 1 / -1; border-top: 1px solid var(--border-subtle); border-left: 0; } .detail-list { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 768px) { .timeline-workspace { border-radius: 8px; } .timeline-command-strip { display: grid; } .integrity-badge { justify-self: start; } .timeline-body { grid-template-columns: 1fr; } .timeline-filter-pane { border-right: 0; border-bottom: 1px solid var(--border-subtle); } .detail-list { grid-template-columns: 1fr; } }
</style>
