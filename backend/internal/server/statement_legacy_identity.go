package server

import "gorm.io/gorm"

// Read explicitly recorded supplier references, never infer them from local IDs.
func statementLegacyInvocationIDs(tx *gorm.DB, usages []UsageRecord) (map[string]string, error) {
	ids := []string{}
	for _, usage := range usages {
		if usage.RequestID != "" {
			ids = append(ids, usage.RequestID)
		}
	}
	result := map[string]string{}
	ambiguous := map[string]bool{}
	for start := 0; start < len(ids); start += 200 {
		var logs []RequestLog
		if err := tx.Select("request_id", "upstream_request_id").Where("request_id IN ?", ids[start:min(start+200, len(ids))]).Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, log := range logs {
			if log.UpstreamRequestID == "" || ambiguous[log.RequestID] {
				continue
			}
			if previous, ok := result[log.RequestID]; ok && previous != log.UpstreamRequestID {
				delete(result, log.RequestID)
				ambiguous[log.RequestID] = true
				continue
			}
			result[log.RequestID] = log.UpstreamRequestID
		}
	}
	return result, nil
}
