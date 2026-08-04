package server

func (s *Server) requestLogsWithUsageForUser(user AdminUser) []RequestLog {
	logs := s.filterRequestLogsForUser(user, s.store.ListRequestLogs())
	records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
	result := enrichRequestLogsWithUsage(logs, records)
	if !s.canViewGlobalOperations(user) {
		for index := range result {
			result[index].ProviderCostUSD = 0
		}
	}
	return result
}

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
	return result
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
