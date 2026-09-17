<script setup lang="ts">
import { computed, ref, watch } from "vue";

import {
  AssetCategory,
  AssetDifferenceKind,
} from "../bindings/desktopguardpro/internal/domain";
import type {
  Asset,
  AssetDifference,
} from "../bindings/desktopguardpro/internal/domain";

const props = defineProps<{
  differences: AssetDifference[];
  categories: AssetCategory[];
  baselineAvailable: boolean;
  loading: boolean;
  errorMessage: string;
  emptyMessage: string;
}>();

const emit = defineEmits<{
  refresh: [];
  toggleCategory: [category: AssetCategory];
}>();

const selected = ref<AssetDifference>();
const assetCategories: AssetCategory[] = [
  AssetCategory.AssetCategorySoftware,
  AssetCategory.AssetCategoryDevice,
  AssetCategory.AssetCategoryNetwork,
  AssetCategory.AssetCategoryAccount,
  AssetCategory.AssetCategorySystem,
];
const changeCount = computed(() => props.differences.length);

watch(() => props.differences, (differences) => {
  if (!selected.value || differences.includes(selected.value)) return;
  selected.value = undefined;
});

function assetFor(difference: AssetDifference): Asset | undefined {
  return difference.after ?? difference.before ?? undefined;
}

function categoryLabel(category: AssetCategory): string {
  switch (category) {
    case AssetCategory.AssetCategorySoftware: return "软件";
    case AssetCategory.AssetCategoryDevice: return "设备";
    case AssetCategory.AssetCategoryNetwork: return "网络";
    case AssetCategory.AssetCategoryAccount: return "账户";
    case AssetCategory.AssetCategorySystem: return "系统";
    default: return "未分类";
  }
}

function changeLabel(kind: AssetDifferenceKind): string {
  switch (kind) {
    case AssetDifferenceKind.AssetDifferenceAdded: return "新增";
    case AssetDifferenceKind.AssetDifferenceRemoved: return "移除";
    case AssetDifferenceKind.AssetDifferenceChanged: return "变更";
    default: return "未分类";
  }
}

function changeClass(kind: AssetDifferenceKind): string {
  return `change-${kind}`;
}

function changedFields(difference: AssetDifference): string {
  return (difference.changedAttributes ?? []).map(fieldLabel).join("、") || "—";
}

function fieldLabel(field: string): string {
  return { displayName: "显示名", version: "版本" }[field] ?? field;
}

function selectDifference(difference: AssetDifference): void {
  selected.value = difference;
}
</script>

<template>
  <section class="asset-workspace" aria-labelledby="asset-title">
    <header class="asset-command-strip">
      <div>
        <span class="panel-kicker">系统基线</span>
        <h2 id="asset-title">资产变化</h2>
      </div>
      <span class="asset-status" :class="{ available: baselineAvailable }">
        {{ baselineAvailable ? `${changeCount} 项变化` : "基线不可用" }}
      </span>
    </header>

    <div class="asset-body">
      <aside class="asset-filter-pane" aria-label="资产类别筛选">
        <div class="filter-heading">
          <strong>资产类别</strong>
          <span>{{ categories.length ? `${categories.length} 个类别` : "全部类别" }}</span>
        </div>
        <div class="asset-category-filter">
          <button
            v-for="category in assetCategories"
            :key="category"
            type="button"
            :class="{ selected: categories.includes(category) }"
            :aria-pressed="categories.includes(category)"
            :disabled="loading"
            @click="emit('toggleCategory', category)"
          >
            {{ categoryLabel(category) }}
          </button>
        </div>
        <button class="refresh-button" type="button" :disabled="loading" @click="emit('refresh')">
          {{ loading ? "正在读取" : "刷新变化" }}
        </button>
      </aside>

      <div class="asset-results-pane">
        <p v-if="errorMessage" class="asset-error" role="alert">{{ errorMessage }}</p>
        <div class="asset-table-wrap" role="region" aria-label="资产变化数据表" tabindex="0">
          <table v-if="differences.length" class="asset-difference-table">
            <thead><tr><th>变化</th><th>类别</th><th>显示名</th><th>变更字段</th></tr></thead>
            <tbody>
              <tr
                v-for="difference in differences"
                :key="`${difference.kind}-${assetFor(difference)?.category}-${assetFor(difference)?.identifier}`"
                class="asset-difference-row"
                :class="{ selected: selected === difference }"
                role="button"
                tabindex="0"
                :aria-selected="selected === difference"
                @click="selectDifference(difference)"
                @keydown.enter="selectDifference(difference)"
                @keydown.space.prevent="selectDifference(difference)"
              >
                <td><span class="change-pill" :class="changeClass(difference.kind)">{{ changeLabel(difference.kind) }}</span></td>
                <td>{{ categoryLabel(assetFor(difference)?.category ?? AssetCategory.AssetCategorySystem) }}</td>
                <td class="asset-name" :title="assetFor(difference)?.displayName">{{ assetFor(difference)?.displayName }}</td>
                <td>{{ changedFields(difference) }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="asset-empty">{{ loading ? "正在读取资产基线。" : emptyMessage }}</p>
        </div>
        <footer class="asset-status-bar"><span>已显示 {{ differences.length }} 项变化</span></footer>
      </div>

      <aside class="asset-detail-pane" aria-label="资产变化详情">
        <template v-if="selected">
          <div class="detail-heading">
            <span class="panel-kicker">已选择资产</span>
            <strong>{{ assetFor(selected)?.displayName }}</strong>
            <span :class="['change-pill', changeClass(selected.kind)]">{{ changeLabel(selected.kind) }}</span>
          </div>
          <dl class="detail-list">
            <div><dt>类别</dt><dd>{{ categoryLabel(assetFor(selected)?.category ?? AssetCategory.AssetCategorySystem) }}</dd></div>
            <div><dt>标识</dt><dd class="detail-break">{{ assetFor(selected)?.identifier }}</dd></div>
            <div><dt>变更字段</dt><dd>{{ changedFields(selected) }}</dd></div>
          </dl>
        </template>
        <div v-else class="detail-empty">
          <strong>选择一项资产变化</strong>
          <p>选择表格记录后，可在此查看资产标识和发生变化的字段。</p>
        </div>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.asset-workspace { display: grid; grid-template-rows: auto minmax(0, 1fr); min-width: 0; min-height: 500px; overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.asset-command-strip { display: flex; gap: 16px; align-items: center; justify-content: space-between; min-height: 66px; padding: 12px 16px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); }
.panel-kicker { display: block; margin-bottom: 3px; color: var(--text-muted); font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.asset-command-strip h2 { margin: 0; color: var(--navy); font-size: 1.05rem; }
.asset-status, .change-pill { display: inline-block; padding: 5px 8px; color: #735712; font-size: 12px; font-weight: 700; background: #fff9e8; border: 1px solid #dfcc91; border-radius: 6px; }
.asset-status.available, .change-added { color: #165d3a; background: #edf8f2; border-color: #a9d4bc; }
.change-removed { color: #852a35; background: #fff2f3; border-color: #e5b7bd; }
.change-changed { color: #294d78; background: var(--control-blue); border-color: var(--border); }
.asset-body { display: grid; grid-template-columns: 212px minmax(0, 1fr) 286px; min-width: 0; min-height: 0; }
.asset-filter-pane { display: grid; align-content: start; gap: 12px; padding: 14px; background: #f8fafc; border-right: 1px solid var(--border-subtle); }
.filter-heading { display: grid; gap: 3px; }
.filter-heading strong { color: var(--navy); font-size: 12px; }
.filter-heading span { color: var(--text-muted); font-size: 11px; }
.asset-category-filter { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px; }
.asset-category-filter button, .refresh-button { min-height: 34px; padding: 6px 9px; color: var(--text-secondary); font-size: 12px; font-weight: 600; background: var(--control-blue); border: 1px solid var(--border); border-radius: 6px; cursor: pointer; }
.asset-category-filter button:hover { color: var(--navy); background: var(--selected-blue); }
.asset-category-filter button.selected { color: #fff; background: var(--brand); border-color: var(--brand); }
.asset-category-filter button:disabled, .refresh-button:disabled { cursor: default; opacity: .52; }
.refresh-button { width: 100%; padding-inline: 16px; color: #fff; background: var(--brand); border-color: var(--brand); }
.refresh-button:not(:disabled):hover { background: var(--interactive); border-color: var(--interactive); }
.asset-results-pane { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; min-width: 0; min-height: 0; padding: 12px; }
.asset-error { margin: 0 0 10px; padding: 10px 12px; color: #852a35; font-size: 13px; background: #fff2f3; border: 1px solid #e5b7bd; border-radius: 6px; }
.asset-table-wrap { max-height: calc(100vh - 228px); overflow: auto; border: 1px solid var(--border); border-radius: 7px; }
.asset-table-wrap:focus-visible { outline: 0; border-color: var(--interactive); box-shadow: 0 0 0 3px rgb(11 91 181 / 22%); }
.asset-difference-table { width: 100%; min-width: 640px; border-collapse: collapse; color: var(--text-secondary); font-size: 12px; }
.asset-difference-table th, .asset-difference-table td { height: 44px; padding: 9px 10px; text-align: left; vertical-align: middle; border-bottom: 1px solid var(--border-subtle); }
.asset-difference-table th { position: sticky; top: 0; z-index: 1; color: var(--navy); font-size: 11px; font-weight: 700; background: var(--card-blue); }
.asset-difference-row { background: var(--surface); cursor: pointer; transition: background-color 180ms ease; }
.asset-difference-row:nth-child(even) { background: #f8fafc; }
.asset-difference-row:hover, .asset-difference-row.selected { background: var(--selected-blue); }
.asset-difference-row:focus-visible { position: relative; z-index: 2; outline: 2px solid var(--interactive); outline-offset: -2px; }
.asset-name { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.asset-empty { margin: 0; padding: 44px 20px; color: var(--text-muted); font-size: 13px; line-height: 1.6; text-align: center; background: #f8fafc; }
.asset-status-bar { display: flex; align-items: center; min-height: 38px; padding-top: 7px; color: var(--text-muted); font-size: 11px; }
.asset-detail-pane { min-width: 0; padding: 16px; background: #f8fafc; border-left: 1px solid var(--border-subtle); }
.detail-heading { display: grid; gap: 6px; padding-bottom: 14px; border-bottom: 1px solid var(--border-subtle); }
.detail-heading strong { overflow-wrap: anywhere; color: var(--navy); font-size: 1.1rem; line-height: 1.45; }
.detail-heading .change-pill { width: fit-content; }
.detail-list { display: grid; gap: 11px; margin: 16px 0 0; }
.detail-list div { display: grid; gap: 3px; }
.detail-list dt { color: var(--text-muted); font-size: 11px; }
.detail-list dd { margin: 0; color: var(--navy); font-size: 12px; line-height: 1.5; }
.detail-break { overflow-wrap: anywhere; }
.detail-empty { padding: 28px 4px; color: var(--text-secondary); }
.detail-empty strong { color: var(--navy); font-size: 13px; }
.detail-empty p { margin: 8px 0 0; font-size: 12px; line-height: 1.65; }
@media (max-width: 1120px) { .asset-body { grid-template-columns: 190px minmax(0, 1fr); } .asset-detail-pane { grid-column: 1 / -1; border-top: 1px solid var(--border-subtle); border-left: 0; } .detail-list { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 768px) { .asset-workspace { border-radius: 8px; } .asset-command-strip { display: grid; } .asset-status { justify-self: start; } .asset-body { grid-template-columns: 1fr; } .asset-filter-pane { border-right: 0; border-bottom: 1px solid var(--border-subtle); } .detail-list { grid-template-columns: 1fr; } }
</style>
