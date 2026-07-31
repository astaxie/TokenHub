package server

func (s *Server) requestLogsWithUsageForUser(user AdminUser) []RequestLog {
	logs := s.filterRequestLogsForUser(user, s.store.ListRequestLogs())
	records := s.filterUsageRecordsForUser(user, s.store.ListUsageRecords())
	return enrichRequestLogsWithUsage(logs, records)
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
		log.UsageRecordCount++
	}
	return result
}
