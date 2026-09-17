<script setup lang="ts">
import type { Session } from "../bindings/desktopguardpro/internal/domain";
import type { SessionHistoryItem } from "./sessionHistory";

defineProps<{ items: SessionHistoryItem[]; loading: boolean; loaded: boolean; hasMore: boolean; connected: boolean; errorMessage: string; retentionBusy: boolean; retentionFeedback: string; selectedID?: string }>();
const emit = defineEmits<{ refresh: []; loadMore: []; select: [session: Session]; updateRetention: [sessionID: string, locked: boolean]; prune: [beforeUtc: string] }>();
const labels: Record<string, string> = { draft: "草稿", preparing: "准备中", active: "保护中", degraded: "降级保护", finalizing: "正在结束", completed: "已完成", failed: "失败" };
const formatTime = (value: string) => new Date(value).toLocaleString("zh-CN", { hour12: false });
function confirmPrune(): void {
  const before = new Date();
  before.setDate(before.getDate() - 30);
  if (window.confirm("将永久删除 30 天前未锁定的已完成或失败会话。是否继续？")) emit("prune", before.toISOString());
}
</script>

<template>
  <section class="history-workspace" aria-label="已保存的保护会话">
    <header class="history-command-strip">
      <strong>会话记录</strong>
      <span>已加载 {{ items.length }} 条会话</span>
      <button type="button" :disabled="!connected || loading" @click="emit('refresh')">刷新列表</button>
      <button type="button" :disabled="!connected || loading || retentionBusy" @click="confirmPrune">清理 30 天前会话</button>
    </header>
    <div class="history-content">
      <p v-if="errorMessage" class="error-banner" role="alert">{{ errorMessage }}</p>
      <p v-if="retentionFeedback" class="retention-feedback" role="status">{{ retentionFeedback }}</p>
      <p v-if="loading" role="status">正在读取历史会话</p>
      <p v-if="loaded && !loading && items.length === 0" class="history-empty">没有已保存的保护会话。</p>
    <div v-if="items.length" class="history-table-wrap" role="region" aria-label="历史会话数据表" tabindex="0">
      <table>
        <thead><tr><th scope="col">会话名称</th><th scope="col">创建时间</th><th scope="col">状态</th><th scope="col">保留</th><th scope="col">操作</th></tr></thead>
        <tbody>
          <tr v-for="item in items" :key="item.session.ID" :class="{ selected: item.session.ID === selectedID }">
            <td><span class="session-name" :title="item.session.Name">{{ item.session.Name }}</span><small :title="item.session.ID">{{ item.session.ID }}</small></td>
            <td>{{ formatTime(item.createdUtc) }}</td>
            <td>{{ labels[item.session.State] ?? item.session.State }}</td>
            <td><button type="button" :disabled="!connected || retentionBusy || !['completed', 'failed'].includes(item.session.State)" @click="emit('updateRetention', item.session.ID, !item.retentionLocked)">{{ item.retentionLocked ? "解除锁定" : "锁定保留" }}</button></td>
            <td><button type="button" :disabled="!connected" :aria-label="'查看 ' + item.session.Name" @click="emit('select', item.session)">查看记录</button></td>
          </tr>
        </tbody>
      </table>
    </div>
    </div>
    <footer class="history-status-bar">
      <span>已显示 {{ items.length }} 条</span>
      <span>{{ hasMore ? "还有更早的会话记录" : "已到记录末尾" }}</span>
      <button v-if="hasMore" class="load-history" type="button" :disabled="loading || !connected" @click="emit('loadMore')">加载更早会话</button>
    </footer>
  </section>
</template>

<style scoped>
.history-workspace { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; min-height: 420px; overflow: hidden; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; box-shadow: var(--card-shadow); }
.history-command-strip { display: flex; align-items: center; gap: 12px; min-height: 48px; padding: 8px 12px; background: var(--card-blue); border-bottom: 1px solid var(--border-subtle); font-size: 12px; }
.history-command-strip strong { color: var(--navy); font-size: 13px; }
.history-command-strip span { margin-right: auto; color: var(--text-muted); }
.history-content { min-width: 0; min-height: 0; padding: 12px; }
button { padding: 6px 10px; border: 1px solid var(--border); border-radius: 7px; color: var(--navy); background: var(--control-blue); cursor: pointer; font-size: 12px; }
button:disabled { opacity: .55; cursor: default; }
.history-table-wrap { max-height: calc(100vh - 220px); overflow: auto; border: 1px solid var(--border-subtle); border-radius: 7px; }
.history-table-wrap:focus-visible { border-color: var(--interactive); box-shadow: 0 0 0 3px rgb(11 91 181 / 22%); outline: 0; }
table { width: 100%; border-collapse: collapse; font-size: 13px; text-align: left; }
th, td { height: 44px; padding: 8px 10px; border-bottom: 1px solid var(--border-subtle); }
th { position: sticky; top: 0; z-index: 1; background: #dbeaff; white-space: nowrap; }
td:not(:first-child) { white-space: nowrap; }
tbody tr:nth-child(even) { background: #f7faff; }
tbody tr:hover, tbody tr.selected { background: var(--selected-blue); }
.session-name, small { display: block; max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
small { margin-top: 4px; color: var(--text-muted); font-size: 12px; }
tbody tr.selected small { color: var(--text-secondary); }
.history-empty { color: var(--text-muted); }
.retention-feedback { margin: 0 0 12px; padding: 8px 10px; color: var(--text-secondary); background: var(--card-blue); border: 1px solid var(--border-subtle); border-radius: 7px; font-size: 12px; }
.history-status-bar { display: flex; gap: 14px; align-items: center; min-height: 34px; padding: 5px 10px; color: var(--text-muted); background: var(--card-blue); border-top: 1px solid var(--border-subtle); font-size: 11px; }
.history-status-bar .load-history { margin-left: auto; }
@media (max-width: 560px) {
  .history-command-strip, .history-status-bar { flex-wrap: wrap; }
  .history-command-strip span { width: 100%; }
  .history-status-bar .load-history { width: 100%; margin-left: 0; }
}
</style>
