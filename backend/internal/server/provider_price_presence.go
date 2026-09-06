package server

func applyProviderPricePresence(model *ProviderModel, current ProviderModel, patch providerModelPatchRequest) {
	metadata := cloneStringMap(current.Metadata)
	if patch.Metadata != nil {
		metadata = cloneStringMap(patch.Metadata)
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	for _, field := range []struct {
		key        string
		value      *float64
		configured *bool
		target     *float64
	}{
		{"input_price_configured", patch.InputPriceUSDPer1M, patch.InputPriceConfigured, &model.InputPriceUSDPer1M},
		{"output_price_configured", patch.OutputPriceUSDPer1M, patch.OutputPriceConfigured, &model.OutputPriceUSDPer1M},
		{cacheReadConfiguredKey, patch.CacheReadPriceUSDPer1M, patch.CacheReadPriceConfigured, &model.CacheReadPriceUSDPer1M},
	} {
		known := current.Metadata[field.key] == "true"
		if field.value != nil {
			known = true
		}
		if field.configured != nil {
			known = *field.configured
			if !known {
				*field.target = 0
			}
		}
		if known {
			metadata[field.key] = "true"
		} else {
			metadata[field.key] = "false"
		}
	}
	model.Metadata = metadata
}
