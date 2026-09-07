package server

func cloneUsageEvidence(e *usageEvidence) *usageEvidence {
	if e == nil {
		return nil
	}
	copy := *e
	copy.Fields = map[string]usageEvidenceField{}
	for k, v := range e.Fields {
		copy.Fields[k] = v
	}
	return &copy
}
func markUsageStream(usage Usage, complete bool) Usage {
	usage.Evidence = cloneUsageEvidence(evidenceForUsage(usage))
	usage.Evidence.StreamComplete = &complete
	return usage
}
func mergeOpenAIUsageEvidence(current, next Usage) Usage {
	if current.Evidence == nil {
		return next
	}
	e := cloneUsageEvidence(current.Evidence)
	if next.Evidence == nil {
		return current
	}
	for key, field := range next.Evidence.Fields {
		if field.State != "missing" {
			e.Fields[key] = field
		}
	}
	for key, target := range map[string]*int64{"input_total": &current.PromptTokens, "cache_read": &current.CachedInputTokens, "cache_write_total": &current.CacheWriteInputTokens, "cache_write_5m": &current.CacheWrite5mInputTokens, "cache_write_1h": &current.CacheWrite1hInputTokens, "output": &current.CompletionTokens, "input_audio": &current.InputAudioTokens, "output_audio": &current.OutputAudioTokens, "reasoning": &current.ReasoningOutputTokens, "accepted_prediction": &current.AcceptedPredictionTokens, "rejected_prediction": &current.RejectedPredictionTokens} {
		if f, ok := next.Evidence.Fields[key]; ok && f.Value != nil {
			*target = *f.Value
		}
	}
	current.TotalTokens = saturatingAddNonNegative(current.PromptTokens, current.CompletionTokens)
	if f := next.Evidence.Fields["total"]; f.State == "reported" && f.Value != nil {
		current.TotalTokens = *f.Value
	} else {
		e.Fields["total"] = derivedEvidence(current.TotalTokens)
	}
	if next.Evidence.ResponseID != "" {
		e.ResponseID = next.Evidence.ResponseID
	}
	current.Evidence = e
	return current
}
func usageReports(usage Usage, key string) bool {
	return usage.Evidence != nil && usage.Evidence.has(key)
}

func recordUsageEstimate(usage *Usage, field string, value int64) {
	usage.Evidence = cloneUsageEvidence(evidenceForUsage(*usage))
	if usage.Evidence.Protocol == "legacy" {
		usage.Evidence.Protocol = "local_estimate"
	}
	usage.Evidence.Fields[field] = usageEvidenceField{Value: &value, State: "estimated"}
}
