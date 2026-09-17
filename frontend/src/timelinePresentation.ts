import {
  EventCategory,
  type AuditEvent,
} from "../bindings/desktopguardpro/internal/domain";

export const timelineCategories = [
  EventCategory.EventCategoryFile,
  EventCategory.EventCategoryProcess,
  EventCategory.EventCategorySoftware,
  EventCategory.EventCategorySystem,
  EventCategory.EventCategoryDevice,
  EventCategory.EventCategoryHealth,
] as const;

const categoryLabels: Record<string, string> = {
  [EventCategory.EventCategoryFile]: "文件",
  [EventCategory.EventCategoryProcess]: "进程",
  [EventCategory.EventCategorySoftware]: "软件",
  [EventCategory.EventCategorySystem]: "系统",
  [EventCategory.EventCategoryDevice]: "设备",
  [EventCategory.EventCategoryHealth]: "采集健康",
};

const actionLabels: Record<string, string> = {
  process_started: "进程启动",
  process_stopped: "进程结束",
  device_connected: "设备连接",
  device_disconnected: "设备断开",
  registry_value_added: "注册表值新增",
  registry_value_modified: "注册表值修改",
  registry_value_removed: "注册表值删除",
  file_created: "文件创建",
  file_modified: "文件修改",
  file_deleted: "文件删除",
  file_renamed: "文件重命名",
  file_rename_old_name: "文件重命名旧名称",
  file_rename_new_name: "文件重命名新名称",
  observation_queue_overflow: "事件队列溢出",
  directory_snapshot_required: "需要目录校验",
  service_recovered_after_interruption: "服务中断后恢复",
};

export function categoryLabel(category: EventCategory): string {
  return categoryLabels[category] ?? (category || "未知");
}

export function actionLabel(action: string): string {
  return actionLabels[action] ?? (action.replaceAll("_", " ") || "未知动作");
}

export function formatTimelineTime(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "时间无效";
  return parsed.toLocaleString("zh-CN", { hour12: false });
}

export function displayObject(event: AuditEvent): string {
  const value = event.objectKey?.trim() || event.processKey?.trim() || "未记录对象";
  return truncate(value, 120);
}

export function utcToLocalInput(value: string): string {
  if (!value) return "";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "";
  const offset = parsed.getTimezoneOffset() * 60_000;
  return new Date(parsed.getTime() - offset).toISOString().slice(0, 16);
}

export function localInputToUTC(value: string): string {
  if (!value) return "";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
}

function truncate(value: string, maximum: number): string {
  return [...value].length <= maximum ? value : `${[...value].slice(0, maximum).join("")}…`;
}
