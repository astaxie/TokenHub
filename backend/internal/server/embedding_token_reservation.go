package server

func embeddingTokenReservation(input any) int64 {
	_, tokenized, err := embeddingInputCount(input)
	if err != nil || !tokenized {
		return EstimateTextTokens(EmbeddingInputText(input))
	}
	switch values := input.(type) {
	case []int:
		return int64(len(values))
	case []any:
		if _, flat := values[0].(float64); flat {
			return int64(len(values))
		}
		var total int64
		for _, tokens := range values {
			total = saturatingAddNonNegative(total, embeddingTokenReservation(tokens))
		}
		return total
	}
	return 0
}
