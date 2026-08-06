package server

func enrichRequestLogsWithUsage(logs []RequestLog, records []UsageRecord) []RequestLog {
	byRequestID := make(map[string]*RequestLog, len(logs))
	result := make([]RequestLog, len(logs))
	copy(result, logs)
	for index := range result {
		byRequestID[result[index].RequestID] = &result[index]
	}
	for _, record := range records {
		log := byRequestID[record.RequestID]
		if log == nil {
			continue
		}
		enrichRequestLogWithUsageRecord(log, record)
	}
	return result
}

func enrichRequestLogWithUsageRecord(log *RequestLog, record UsageRecord) {
	log.InputTokens += record.InputTokens
	log.CachedInputTokens += record.CachedInputTokens
	log.CacheWriteTokens += record.CacheWriteTokens
	log.InputAudioTokens += record.InputAudioTokens
	log.OutputTokens += record.OutputTokens
	log.ReasoningTokens += record.ReasoningTokens
	log.OutputAudioTokens += record.OutputAudioTokens
	log.AcceptedPredictionTokens += record.AcceptedPredictionTokens
	log.RejectedPredictionTokens += record.RejectedPredictionTokens
	log.TotalTokens += record.TotalTokens
	log.EstimatedCostUSD += record.CostUSD
	log.ProviderCostUSD += record.ProviderCostUSD
	log.UsageRecordCount++
}

func (s *Server) redactProviderCostsForUser(user AdminUser, detail map[string]any) {
	if s.canViewGlobalOperations(user) {
		return
	}
	if usage, ok := detail["usage"].([]UsageRecord); ok {
		for index := range usage {
			usage[index].ProviderCostUSD = 0
		}
		detail["usage"] = usage
	}
}
