import { describe, expect, it } from "vitest";

import {
  AuditEvent,
  EventCategory,
} from "../bindings/desktopguardpro/internal/domain";
import {
  actionLabel,
  categoryLabel,
  displayObject,
  formatTimelineTime,
  localInputToUTC,
} from "./timelinePresentation";

describe("timeline presentation", () => {
  it("uses Chinese labels and readable fallbacks", () => {
    expect(categoryLabel(EventCategory.EventCategoryProcess)).toBe("进程");
    expect(actionLabel("device_connected")).toBe("设备连接");
    expect(actionLabel("service_recovered_after_interruption")).toBe("服务中断后恢复");
    expect(actionLabel("file_rename_old_name")).toBe("文件重命名旧名称");
    expect(actionLabel("file_rename_new_name")).toBe("文件重命名新名称");
    expect(actionLabel("custom_action")).toBe("custom action");
  });

  it("handles invalid time and converts local input to UTC", () => {
    expect(formatTimelineTime("invalid")).toBe("时间无效");
    expect(localInputToUTC("invalid")).toBe("");
    expect(localInputToUTC("2026-08-23T14:30")).toMatch(/^2026-08-23T/);
  });

  it("prefers the object key and limits displayed detail", () => {
    const event = new AuditEvent({
      objectKey: "A".repeat(140),
      processKey: "process-key",
    });
    const displayed = displayObject(event);
    expect(displayed.endsWith("…")).toBe(true);
    expect([...displayed]).toHaveLength(121);
  });
});
