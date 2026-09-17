package storage

import (
	"encoding/json"
	"strings"

	"desktopguardpro/internal/domain"
)

const maximumTimelinePayloadPreview = 16 * 1024
const maximumTimelinePageBytes = 512 * 1024

func timelinePreview(event domain.AuditEvent, payload []byte) EventRecord {
	event.EncryptedPayload = nil
	record := EventRecord{Event: event, Payload: payload}
	if len(payload) > maximumTimelinePayloadPreview {
		record.PreviewTruncated = true
		record.Payload, _ = json.Marshal(struct {
			Preview       string `json:"preview"`
			OriginalBytes int    `json:"originalBytes"`
			Notice        string `json:"notice"`
		}{strings.ToValidUTF8(string(payload[:maximumTimelinePayloadPreview]), ""), len(payload), "预览已截断；完整数据保留在数据库，可在报告中选择包含原始内容。"})
	}
	return record
}
