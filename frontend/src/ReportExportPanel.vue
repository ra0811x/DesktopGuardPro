<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";

import { ObjectDetailMode, RedactionPolicy } from "../bindings/desktopguardpro/internal/reporting";
import { ReportFormat } from "../bindings/desktopguardpro/internal/service";
import type { ReportSequenceRange } from "./analysisState";
import type { ReportExportSelection } from "./reportExport";

const props = defineProps<{
  disabled: boolean;
  exporting: boolean;
  exportFeedback: string;
}>();

const emit = defineEmits<{
  export: [format: ReportFormat, redaction: RedactionPolicy, range: ReportSequenceRange];
}>();

const selection = reactive<ReportExportSelection>({
  format: ReportFormat.ReportFormatHTML,
  objectDetails: ObjectDetailMode.ObjectDetailBasename,
  includeProcessKey: false,
  includePayload: false,
});
const sequenceInputs = reactive({ from: "", to: "" });
const sensitiveAcknowledged = ref(false);
const range = computed<ReportSequenceRange>(() => ({
  fromSequence: sequenceInputs.from === "" ? undefined : Number(sequenceInputs.from),
  toSequence: sequenceInputs.to === "" ? undefined : Number(sequenceInputs.to),
}));
const invalidRange = computed(() => {
  const { fromSequence: from, toSequence: to } = range.value;
  return [from, to].some((value) => value !== undefined && (!Number.isSafeInteger(value) || value < 1)) ||
    (from !== undefined && to !== undefined && (to < from || to - from + 1 > 100000));
});
const exposesSensitiveDetails = computed(() =>
  selection.objectDetails === ObjectDetailMode.ObjectDetailFull || selection.includeProcessKey || selection.includePayload);
const formatLabel = computed(() => {
  const labels: Record<string, string> = {
    [ReportFormat.ReportFormatHTML]: "HTML",
    [ReportFormat.ReportFormatMarkdown]: "Markdown",
    [ReportFormat.ReportFormatJSON]: "JSON",
  };
  return labels[selection.format] ?? "格式无效";
});
const rangeLabel = computed(() => {
  const { fromSequence: from, toSequence: to } = range.value;
  if (from && to) return `事件序列 #${from} 至 #${to}`;
  if (from) return `从事件序列 #${from} 开始`;
  if (to) return `截至事件序列 #${to}`;
  return "会话内全部可导出事件";
});
const objectDetailLabel = computed(() => {
  const labels: Record<string, string> = {
    [ObjectDetailMode.ObjectDetailOmit]: "隐藏对象信息",
    [ObjectDetailMode.ObjectDetailBasename]: "仅保留文件名",
    [ObjectDetailMode.ObjectDetailFull]: "包含完整对象路径",
  };
  return labels[selection.objectDetails] ?? "对象信息设置无效";
});
const canExport = computed(() => !props.disabled && !props.exporting && !invalidRange.value &&
  (!exposesSensitiveDetails.value || sensitiveAcknowledged.value));

watch(exposesSensitiveDetails, (exposed) => {
  if (!exposed) sensitiveAcknowledged.value = false;
});

function requestExport(): void {
  if (!canExport.value) return;
  emit("export", selection.format, new RedactionPolicy({
    objectDetails: selection.objectDetails,
    includeProcessKey: selection.includeProcessKey,
    includePayload: selection.includePayload,
    maximumTextLength: 512,
  }), range.value);
}
</script>

<template>
  <section class="report-workspace" aria-labelledby="export-title">
    <header class="report-header">
      <div><span class="panel-kicker">会话输出</span><h2 id="export-title">导出审计报告</h2></div>
      <span class="report-format-badge">{{ formatLabel }}</span>
    </header>

    <div class="report-body">
      <div class="report-properties">
        <strong class="options-title">导出内容</strong>
        <label><span>报告格式</span><select v-model="selection.format" id="report-format"><option :value="ReportFormat.ReportFormatHTML">HTML 报告</option><option :value="ReportFormat.ReportFormatMarkdown">Markdown 报告</option><option :value="ReportFormat.ReportFormatJSON">JSON 报告</option></select></label>
        <label><span>对象细节</span><select v-model="selection.objectDetails" id="report-object-details"><option :value="ObjectDetailMode.ObjectDetailOmit">全部隐藏</option><option :value="ObjectDetailMode.ObjectDetailBasename">仅文件名</option><option :value="ObjectDetailMode.ObjectDetailFull">完整对象路径</option></select></label>
        <label class="check-option"><span>进程关联键</span><span class="check-control"><input v-model="selection.includeProcessKey" type="checkbox">包含</span></label>
        <label class="check-option"><span>原始事件负载</span><span class="check-control"><input v-model="selection.includePayload" type="checkbox">包含</span></label>
        <label><span>起始事件序列</span><input v-model="sequenceInputs.from" type="number" min="1" step="1" placeholder="留空从头开始"></label>
        <label><span>结束事件序列</span><input v-model="sequenceInputs.to" type="number" min="1" step="1" placeholder="留空到末尾"></label>
      </div>

      <aside class="report-review" aria-label="导出前检查">
        <strong>导出前检查</strong>
        <dl>
          <div><dt>报告格式</dt><dd>{{ formatLabel }} 报告</dd></div>
          <div><dt>事件范围</dt><dd>{{ rangeLabel }}</dd></div>
          <div><dt>对象细节</dt><dd>{{ objectDetailLabel }}</dd></div>
          <div><dt>进程关联键</dt><dd>{{ selection.includeProcessKey ? "包含" : "隐藏" }}</dd></div>
          <div><dt>原始事件负载</dt><dd>{{ selection.includePayload ? "包含" : "隐藏" }}</dd></div>
        </dl>
        <p v-if="invalidRange" class="report-warning" role="alert">请填写有效的正整数序列。结束序列不得小于起始序列，单次范围不超过 100000 条。</p>
        <div v-if="exposesSensitiveDetails" class="sensitive-confirmation">
          <p>当前配置会包含敏感审计信息。保存后需限制报告的访问范围。</p>
          <label for="sensitive-export-acknowledgement"><input id="sensitive-export-acknowledgement" v-model="sensitiveAcknowledged" type="checkbox">我已确认本次导出包含敏感信息。</label>
        </div>
        <p v-else class="review-safe">默认导出仅包含完成审计所需的必要细节。</p>
      </aside>
    </div>

    <footer class="report-command-bar">
      <p v-if="exportFeedback" class="export-feedback" role="status">{{ exportFeedback }}</p>
      <p v-else-if="exposesSensitiveDetails && !sensitiveAcknowledged" class="export-status">确认敏感信息范围后可导出。</p>
      <p v-else-if="invalidRange" class="export-status">修正事件范围后可导出。</p>
      <p v-else class="export-status">报告将在本机浏览器下载目录中保存。</p>
      <button class="export-button" type="button" :disabled="!canExport" @click="requestExport">
        {{ exporting ? "正在生成报告" : "生成并保存报告" }}
      </button>
    </footer>
  </section>
</template>

<style scoped>
.report-workspace { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; min-height: 480px; overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.report-header { display: flex; gap: 14px; align-items: center; justify-content: space-between; min-height: 66px; padding: 12px 16px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); }
.panel-kicker { display: block; margin-bottom: 3px; color: var(--text-muted); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.report-header h2 { margin: 0; color: var(--navy); font-size: 1.05rem; }
.report-format-badge { padding: 5px 8px; color: var(--text-secondary); font-size: 11px; font-weight: 700; background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.report-body { display: grid; grid-template-columns: minmax(0, 1fr) 300px; min-width: 0; }
.report-properties { display: grid; align-content: start; min-width: 0; padding: 14px; }
.options-title { padding: 8px 10px; color: var(--navy); font-size: 12px; font-weight: 700; background: var(--control-blue); border: 1px solid var(--border); border-radius: 7px 7px 0 0; }
.report-properties label { display: grid; grid-template-columns: 160px minmax(0, 1fr); gap: 12px; align-items: center; min-height: 48px; padding: 7px 10px; color: var(--text-secondary); font-size: 12px; font-weight: 600; border-right: 1px solid var(--border); border-bottom: 1px solid var(--border); border-left: 1px solid var(--border); }
.report-properties label:last-child { border-radius: 0 0 7px 7px; }
.report-properties select, .report-properties input[type="number"] { width: 100%; min-height: 38px; padding: 8px 10px; color: var(--navy); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.check-control { display: inline-flex; gap: 8px; align-items: center; font-weight: 500; }
.check-option input, .sensitive-confirmation input { width: 16px; height: 16px; margin: 0; accent-color: var(--interactive); }
.report-review { padding: 16px; color: var(--text-secondary); background: #f8fafc; border-left: 1px solid var(--border-subtle); font-size: 12px; }
.report-review > strong { color: var(--navy); }
.report-review dl { display: grid; gap: 10px; margin: 14px 0; }
.report-review dl div { display: grid; gap: 3px; }
.report-review dt { color: var(--text-muted); font-size: 11px; }
.report-review dd { margin: 0; color: var(--navy); line-height: 1.5; }
.report-warning, .sensitive-confirmation { padding: 11px; color: #735712; font-size: 12px; line-height: 1.6; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 6px; }
.sensitive-confirmation p { margin: 0 0 10px; }
.sensitive-confirmation label { display: flex; gap: 8px; align-items: flex-start; color: #5f4911; font-weight: 700; }
.review-safe { margin: 0; padding: 10px; color: #165d3a; line-height: 1.6; background: #edf8f2; border: 1px solid #a9d4bc; border-radius: 6px; }
.report-command-bar { display: flex; gap: 14px; align-items: center; justify-content: flex-end; min-height: 62px; padding: 10px 14px; background: var(--card-blue); border-top: 1px solid var(--border-subtle); }
.export-status, .export-feedback { margin: 0 auto 0 0; color: var(--text-muted); font-size: 12px; line-height: 1.5; }
.export-feedback { color: #165d3a; font-weight: 700; }
.export-button { display: inline-flex; align-items: center; justify-content: center; min-height: 38px; padding: 9px 16px; color: #fff; font-size: 13px; font-weight: 700; background: var(--brand); border: 1px solid var(--brand); border-radius: 6px; }
.export-button:not(:disabled) { cursor: pointer; }
.export-button:not(:disabled):hover { background: var(--interactive); border-color: var(--interactive); }
.export-button:focus-visible, .report-properties select:focus-visible, .report-properties input:focus-visible, .check-option input:focus-visible, .sensitive-confirmation input:focus-visible { outline: 0; border-color: var(--interactive); box-shadow: 0 0 0 3px rgb(11 91 181 / 22%); }
.export-button:disabled { cursor: default; opacity: .52; }
@media (max-width: 768px) { .report-workspace { border-radius: 8px; } .report-body { grid-template-columns: 1fr; } .report-review { border-top: 1px solid var(--border-subtle); border-left: 0; } .report-properties label { grid-template-columns: 1fr; gap: 5px; } .report-command-bar { align-items: stretch; flex-direction: column; } .export-status, .export-feedback { margin-right: 0; } .export-button { width: 100%; } }
</style>
