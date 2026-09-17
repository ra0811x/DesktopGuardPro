<script setup lang="ts">
import { computed, ref } from "vue";

import {
  MonitoringTargetKind,
  MonitoringTargetResolutionStatus,
  type MonitoringTarget,
} from "../bindings/desktopguardpro/internal/domain";

const props = defineProps<{
  modelValue: MonitoringTarget[];
  loaded: boolean;
  loading: boolean;
  saving: boolean;
  dirty: boolean;
  editable: boolean;
  connected: boolean;
  errorMessage: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: MonitoringTarget[]];
  save: [];
  reload: [];
}>();

const maximumTargets = 16;
const pathToAdd = ref("");
const kindToAdd = ref<MonitoringTargetKind>(MonitoringTargetKind.MonitoringTargetKindDirectory);
const recursiveToAdd = ref(true);
const inputMessage = ref("");
const editingLocked = computed(() => !props.editable || !props.loaded || props.loading || props.saving);
const reachedLimit = computed(() => props.modelValue.length >= maximumTargets);

function targetKindLabel(kind: MonitoringTargetKind): string {
  if (kind === MonitoringTargetKind.MonitoringTargetKindFile) return "单个文件";
  if (kind === MonitoringTargetKind.MonitoringTargetKindRemovableVolume) return "可移动磁盘";
  return "文件夹";
}

function resolutionLabel(target: MonitoringTarget): string {
  if (target.resolutionStatus === MonitoringTargetResolutionStatus.MonitoringTargetResolutionReparseResolved) {
    return "重解析已处理";
  }
  if (target.resolutionStatus === MonitoringTargetResolutionStatus.MonitoringTargetResolutionAvailable) {
    return "路径可用";
  }
  return "等待保存验证";
}

function updateTargets(targets: MonitoringTarget[]): void {
  emit("update:modelValue", targets);
}

function cleanTarget(target: MonitoringTarget): MonitoringTarget {
  return { path: target.path.trim(), kind: target.kind, recursive: target.kind === MonitoringTargetKind.MonitoringTargetKindDirectory && target.recursive };
}

function updateTarget(index: number, changes: Partial<MonitoringTarget>): void {
  const targets = props.modelValue.map((target, current) => current === index
    ? cleanTarget({ ...target, ...changes })
    : target);
  updateTargets(targets);
  inputMessage.value = "";
}

function addTarget(): void {
  const path = pathToAdd.value.trim();
  if (!path) {
    inputMessage.value = "请输入本地路径。";
    return;
  }
  if (reachedLimit.value) {
    inputMessage.value = `监控目标最多可添加 ${maximumTargets} 个。`;
    return;
  }
  if (props.modelValue.some((target) => target.kind === kindToAdd.value && target.path.localeCompare(path, undefined, { sensitivity: "accent" }) === 0)) {
    inputMessage.value = "同类型路径已经在监控范围中。";
    return;
  }
  updateTargets([...props.modelValue, {
    path,
    kind: kindToAdd.value,
    recursive: kindToAdd.value === MonitoringTargetKind.MonitoringTargetKindDirectory && recursiveToAdd.value,
  }]);
  pathToAdd.value = "";
  inputMessage.value = "";
}

function removeTarget(index: number): void {
  updateTargets(props.modelValue.filter((_, current) => current !== index));
  inputMessage.value = "";
}
</script>

<template>
  <section class="directory-panel" aria-labelledby="directory-title">
    <header class="directory-heading">
      <div>
        <span class="panel-kicker">保护范围</span>
        <h2 id="directory-title">重点对象</h2>
      </div>
      <span class="directory-capacity" :class="{ full: reachedLimit }">{{ modelValue.length }} / {{ maximumTargets }}</span>
    </header>

    <div class="directory-body">
      <p id="directory-help">逐项配置本地文件、文件夹或可移动磁盘。文件夹可选择是否递归监控子目录。</p>

      <div class="directory-list" aria-describedby="directory-help directory-status">
        <p v-if="loaded && modelValue.length === 0" class="directory-empty">尚未添加重点对象，文件变化采集不会启用。</p>
        <div v-for="(target, index) in modelValue" :key="`${target.kind}-${target.path}-${index}`" class="directory-row">
          <span class="directory-number" aria-hidden="true">{{ String(index + 1).padStart(2, "0") }}</span>
          <select
            :value="target.kind"
            :aria-label="`目标 ${index + 1} 类型`"
            :disabled="editingLocked"
            @change="updateTarget(index, { kind: ($event.target as HTMLSelectElement).value as MonitoringTargetKind })"
          >
            <option :value="MonitoringTargetKind.MonitoringTargetKindDirectory">文件夹</option>
            <option :value="MonitoringTargetKind.MonitoringTargetKindFile">单个文件</option>
            <option :value="MonitoringTargetKind.MonitoringTargetKindRemovableVolume">可移动磁盘</option>
          </select>
          <input
            :value="target.configuredPath || target.path"
            type="text"
            :aria-label="`目标 ${index + 1} 路径`"
            :disabled="editingLocked"
            spellcheck="false"
            @change="updateTarget(index, { path: ($event.target as HTMLInputElement).value })"
          >
          <label class="recursive-toggle" :class="{ unavailable: target.kind !== MonitoringTargetKind.MonitoringTargetKindDirectory }">
            <input
              type="checkbox"
              :checked="target.recursive"
              :disabled="editingLocked || target.kind !== MonitoringTargetKind.MonitoringTargetKindDirectory"
              @change="updateTarget(index, { recursive: ($event.target as HTMLInputElement).checked })"
            >
            递归
          </label>
          <span class="resolution-status" :class="{ resolved: target.resolutionStatus === MonitoringTargetResolutionStatus.MonitoringTargetResolutionReparseResolved }" :title="target.resolutionDetail">
            {{ resolutionLabel(target) }}
          </span>
          <button type="button" :disabled="editingLocked" :aria-label="`移除${targetKindLabel(target.kind)} ${target.path}`" @click="removeTarget(index)">移除</button>
          <p v-if="target.resolutionDetail" class="resolution-detail">{{ target.resolutionDetail }}</p>
        </div>
      </div>

      <div class="add-directory">
        <label for="target-path-to-add">添加监控目标</label>
        <div class="add-target-controls">
          <select v-model="kindToAdd" aria-label="新增目标类型" :disabled="editingLocked || reachedLimit">
            <option :value="MonitoringTargetKind.MonitoringTargetKindDirectory">文件夹</option>
            <option :value="MonitoringTargetKind.MonitoringTargetKindFile">单个文件</option>
            <option :value="MonitoringTargetKind.MonitoringTargetKindRemovableVolume">可移动磁盘</option>
          </select>
          <input id="target-path-to-add" v-model="pathToAdd" type="text" placeholder="例如 C:\Users\Raymond\Documents" :disabled="editingLocked || reachedLimit" spellcheck="false" @keyup.enter="addTarget">
          <label class="recursive-toggle add-recursive" :class="{ unavailable: kindToAdd !== MonitoringTargetKind.MonitoringTargetKindDirectory }">
            <input v-model="recursiveToAdd" type="checkbox" :disabled="editingLocked || reachedLimit || kindToAdd !== MonitoringTargetKind.MonitoringTargetKindDirectory">
            递归
          </label>
          <button type="button" :disabled="editingLocked || reachedLimit" @click="addTarget">添加</button>
        </div>
      </div>

      <p v-if="inputMessage" class="directory-input-message" role="alert">{{ inputMessage }}</p>
      <p id="directory-status" class="directory-status" role="status">
        <template v-if="loading">正在读取监控范围。</template>
        <template v-else-if="!loaded">监控范围尚未读取，读取成功后可开启保护。</template>
        <template v-else-if="dirty">监控范围更改尚未保存，保存后可开启保护。</template>
        <template v-else-if="!editable && connected">会话结束后可修改监控范围。</template>
        <template v-else-if="reachedLimit">已达到监控目标数量上限。</template>
        <template v-else>保存时会验证路径、卷和重解析点，并显示处理结果。</template>
      </p>
      <p v-if="errorMessage" class="error-banner" role="alert">{{ errorMessage }}</p>
    </div>

    <footer class="directory-actions">
      <span>{{ dirty ? "存在未保存的范围更改" : "监控范围已保存" }}</span>
      <button type="button" :disabled="!editable || !loaded || loading || saving || !dirty" @click="emit('save')">{{ saving ? "正在保存" : "保存范围" }}</button>
      <button v-if="!loaded" type="button" :disabled="!connected || loading || saving" @click="emit('reload')">重新读取</button>
    </footer>
  </section>
</template>

<style scoped>
.directory-panel { overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.directory-heading { display: flex; gap: 12px; align-items: center; justify-content: space-between; min-height: 64px; padding: 12px 16px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); }
.panel-kicker { display: block; margin-bottom: 3px; color: var(--text-muted); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
h2 { margin: 0; font-size: 1.05rem; }
.directory-capacity { padding: 5px 8px; color: var(--text-secondary); font-size: 12px; font-weight: 700; background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
.directory-capacity.full { color: #735712; background: #fff9e8; border-color: #dfcc91; }
.directory-body { padding: 16px; }
.directory-body > p { margin: 0; color: var(--text-muted); font-size: 12px; line-height: 1.6; }
.directory-list { display: grid; gap: 8px; margin-top: 14px; }
.directory-empty { margin: 0; padding: 16px; color: var(--text-secondary); font-size: 13px; line-height: 1.55; text-align: center; background: #f8fafc; border: 1px dashed var(--border); border-radius: 7px; }
.directory-row { display: grid; grid-template-columns: 28px 112px minmax(180px, 1fr) auto auto auto; gap: 8px; align-items: center; padding: 8px; background: #f8fafc; border: 1px solid var(--border-subtle); border-radius: 7px; }
.directory-number { display: grid; width: 28px; height: 28px; place-items: center; color: var(--text-muted); font-size: 10px; font-weight: 700; background: var(--control-blue); border: 1px solid var(--border); border-radius: 5px; }
input[type="text"], select { display: block; width: 100%; min-width: 0; min-height: 36px; padding: 8px 10px; color: var(--navy); font-size: 13px; background: var(--surface); border: 1px solid var(--border); border-radius: 6px; }
input:disabled, select:disabled { color: var(--text-muted); background: var(--card-blue); }
button { min-height: 34px; padding: 7px 11px; color: var(--navy); font-size: 12px; font-weight: 600; background: var(--control-blue); border: 1px solid var(--border); border-radius: 6px; }
button:not(:disabled) { cursor: pointer; }
button:disabled { cursor: default; opacity: .52; }
.recursive-toggle { display: inline-flex; gap: 5px; align-items: center; color: var(--text-secondary); font-size: 12px; white-space: nowrap; }
.recursive-toggle.unavailable { color: var(--text-muted); }
.resolution-status { padding: 5px 7px; color: #24543c; font-size: 11px; font-weight: 700; white-space: nowrap; background: #edf8f1; border: 1px solid #bddbc8; border-radius: 5px; }
.resolution-status.resolved { color: #335e85; background: #edf5fc; border-color: #bed4e7; }
.resolution-detail { grid-column: 3 / -1; margin: 0; color: var(--text-muted); font-size: 11px; }
.add-directory { margin-top: 14px; padding-top: 14px; border-top: 1px solid var(--border-subtle); }
.add-directory > label { display: block; margin-bottom: 7px; color: var(--text-secondary); font-size: 12px; font-weight: 700; }
.add-target-controls { display: grid; grid-template-columns: 112px minmax(180px, 1fr) auto auto; gap: 8px; align-items: center; }
.add-directory button { color: #fff; background: var(--brand); border-color: var(--brand); }
.directory-input-message { margin-top: 10px !important; padding: 8px 10px; color: #852a35 !important; background: #fff2f3; border: 1px solid #e5b7bd; border-radius: 6px; }
.directory-status { margin-top: 12px !important; }
.error-banner { margin-top: 12px !important; }
.directory-actions { display: flex; gap: 10px; align-items: center; justify-content: flex-end; min-height: 56px; padding: 10px 16px; background: #f8fafc; border-top: 1px solid var(--border-subtle); }
.directory-actions span { margin-right: auto; color: var(--text-muted); font-size: 12px; }
.directory-actions button:first-of-type { color: #fff; background: var(--brand); border-color: var(--brand); }
@media (max-width: 900px) {
  .directory-row { grid-template-columns: 28px 110px minmax(0, 1fr) auto; }
  .resolution-status, .directory-row > button { justify-self: end; }
  .resolution-detail { grid-column: 2 / -1; }
  .add-target-controls { grid-template-columns: 110px minmax(0, 1fr) auto; }
  .add-target-controls button { grid-column: 3; }
}
</style>
