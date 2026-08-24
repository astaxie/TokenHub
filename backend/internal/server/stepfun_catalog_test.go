package server

import (
	"slices"
	"strings"
	"testing"
)

func TestStandardCatalogIncludesStepFunModels(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Model, len(models))
	for _, model := range models {
		byName[strings.ToLower(model.Name)] = model
	}

	tests := []struct {
		name         string
		modality     string
		inputs       []string
		outputs      []string
		capabilities []string
	}{
		{name: "step-3.7-flash", modality: "chat", inputs: []string{"text", "image", "video"}, outputs: []string{"text"}, capabilities: []string{"chat", "vision", "video_input", "reasoning", "tools"}},
		{name: "step-3.5-flash-2603", modality: "chat", inputs: []string{"text"}, outputs: []string{"text"}, capabilities: []string{"chat", "reasoning", "tools"}},
		{name: "step-3.5-flash", modality: "chat", inputs: []string{"text"}, outputs: []string{"text"}, capabilities: []string{"chat", "reasoning", "tools"}},
		{name: "step-2-16k", modality: "chat", inputs: []string{"text"}, outputs: []string{"text"}, capabilities: []string{"chat", "tools"}},
		{name: "step-1-32k", modality: "chat", inputs: []string{"text"}, outputs: []string{"text"}, capabilities: []string{"chat", "tools"}},
		{name: "stepaudio-2.5-realtime", modality: "audio", inputs: []string{"text", "audio"}, outputs: []string{"text", "audio"}, capabilities: []string{"audio", "realtime", "tools"}},
		{name: "stepaudio-2.5-chat", modality: "audio", inputs: []string{"text", "audio"}, outputs: []string{"text"}, capabilities: []string{"audio", "chat"}},
		{name: "stepaudio-2.5-tts", modality: "audio", inputs: []string{"text"}, outputs: []string{"audio"}, capabilities: []string{"audio", "text_to_speech"}},
		{name: "stepaudio-2.5-asr", modality: "audio", inputs: []string{"audio"}, outputs: []string{"text"}, capabilities: []string{"audio", "speech_to_text"}},
		{name: "step-tts-2", modality: "audio", inputs: []string{"text"}, outputs: []string{"audio"}, capabilities: []string{"audio", "text_to_speech"}},
		{name: "step-router-v1", modality: "chat", inputs: []string{"text"}, outputs: []string{"text"}, capabilities: []string{"chat", "reasoning", "tools"}},
		{name: "step-image-edit-2", modality: "image", inputs: []string{"text", "image"}, outputs: []string{"image"}, capabilities: []string{"image", "image_edit", "vision"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, ok := byName[test.name]
			if !ok {
				t.Fatalf("expected %s in standard catalog", test.name)
			}
			if model.Category != "stepfun" || model.Family != "step" || model.Modality != test.modality ||
				!slices.Equal(model.InputModalities, test.inputs) || !slices.Equal(model.OutputModalities, test.outputs) {
				t.Fatalf("unexpected StepFun catalog metadata: %+v", model)
			}
			for _, capability := range test.capabilities {
				if !slices.Contains(model.Capabilities, capability) {
					t.Fatalf("%s missing capability %q: %v", test.name, capability, model.Capabilities)
				}
			}
		})
	}

	step37 := byName["step-3.7-flash"]
	if step37.ContextWindow != 256000 || step37.Metadata["max_output_tokens"] != "256000" ||
		step37.InputPriceUSDPer1M != 0.185 || step37.CacheReadPriceUSDPer1M != 0.037 || step37.OutputPriceUSDPer1M != 1.11 ||
		step37.Metadata["reasoning_effort_options"] != "low,medium,high" || step37.Metadata["endpoints"] != "chat/completions,anthropic" {
		t.Fatalf("incomplete Step 3.7 Flash metadata: %+v", step37)
	}
	step352603 := byName["step-3.5-flash-2603"]
	if step352603.Metadata["reasoning_effort_options"] != "low,high" {
		t.Fatalf("unexpected Step 3.5 Flash 2603 reasoning metadata: %+v", step352603.Metadata)
	}
	for _, name := range []string{"step-2-16k", "step-1-32k"} {
		if slices.Contains(byName[name].Capabilities, "reasoning") || slices.Contains(byName[name].SupportedParameters, "reasoning") {
			t.Fatalf("%s must not advertise unsupported reasoning: %+v", name, byName[name])
		}
	}
	if byName["step-router-v1"].Metadata["billing_mode"] != "subscription" ||
		byName["step-router-v1"].Metadata["max_output_tokens"] != "384000" ||
		byName["step-image-edit-2"].Metadata["pricing_unit"] != "image" {
		t.Fatalf("incomplete Step Plan-only model metadata: router=%+v image=%+v", byName["step-router-v1"], byName["step-image-edit-2"])
	}
}

func TestTrackedStepFunProvidersSeparateDirectAPIAndStepPlan(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]ProviderCatalogEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}

	directModels := []string{
		"step-1-32k", "step-2-16k", "step-3.5-flash", "step-3.5-flash-2603",
		"step-3.7-flash", "step-tts-2", "stepaudio-2.5-asr", "stepaudio-2.5-tts",
	}
	chinaPlanModels := []string{
		"step-3.5-flash", "step-3.5-flash-2603", "step-3.7-flash", "step-image-edit-2", "step-router-v1",
		"stepaudio-2.5-asr", "stepaudio-2.5-chat", "stepaudio-2.5-realtime", "stepaudio-2.5-tts",
	}
	globalPlanModels := []string{
		"step-3.5-flash", "step-3.5-flash-2603", "step-3.7-flash", "step-image-edit-2", "step-router-v1",
		"stepaudio-2.5-asr", "stepaudio-2.5-tts",
	}
	tests := []struct {
		id      string
		baseURL string
		docURL  string
		models  []string
	}{
		{id: "stepfun", baseURL: "https://api.stepfun.com/v1", docURL: "https://platform.stepfun.com/docs/zh/overview/concept", models: directModels},
		{id: "stepfun-global", baseURL: "https://api.stepfun.ai/v1", docURL: "https://platform.stepfun.ai/docs/en/overview/concept", models: directModels},
		{id: "stepfun-plan", baseURL: "https://api.stepfun.com/step_plan/v1", docURL: "https://platform.stepfun.com/docs/zh/step-plan/overview", models: chinaPlanModels},
		{id: "stepfun-ai", baseURL: "https://api.stepfun.ai/step_plan/v1", docURL: "https://platform.stepfun.ai/docs/en/step-plan/overview", models: globalPlanModels},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			entry, ok := byID[test.id]
			if !ok {
				t.Fatalf("expected provider catalog entry %s", test.id)
			}
			if entry.BaseURL != test.baseURL || entry.DocURL != test.docURL {
				t.Fatalf("unexpected %s endpoint metadata: %+v", test.id, entry)
			}
			actual := make([]string, 0, len(entry.Models))
			for _, model := range entry.Models {
				actual = append(actual, model.ID)
			}
			slices.Sort(actual)
			if !slices.Equal(actual, test.models) {
				t.Fatalf("unexpected %s models: got %v want %v", test.id, actual, test.models)
			}
		})
	}

	for _, providerID := range []string{"stepfun", "stepfun-global"} {
		models := providerModelsByID(byID[providerID].Models)
		step37 := models["step-3.7-flash"]
		if step37.Category != "stepfun" || step37.ContextWindow != 256000 || step37.MaxOutputTokens != 256000 ||
			step37.InputPriceUSDPer1M != 0.185 || step37.CacheReadPriceUSDPer1M != 0.037 || step37.OutputPriceUSDPer1M != 1.11 ||
			step37.Metadata["reasoning_effort_options"] != "low,medium,high" ||
			!slices.Equal(step37.InputModalities, []string{"image", "text", "video"}) {
			t.Fatalf("unexpected %s Step 3.7 Flash metadata: %+v", providerID, step37)
		}
		for _, modelID := range []string{"step-tts-2", "stepaudio-2.5-asr", "stepaudio-2.5-tts"} {
			model := models[modelID]
			if model.Type != "audio" || !slices.Contains(model.Capabilities, "audio") {
				t.Fatalf("unexpected %s %s audio metadata: %+v", providerID, modelID, model)
			}
		}
	}

	chinaPlan := providerModelsByID(byID["stepfun-plan"].Models)
	if chinaPlan["step-router-v1"].Metadata["billing_mode"] != "subscription" ||
		chinaPlan["step-router-v1"].Metadata["endpoints"] != "chat/completions,anthropic" ||
		chinaPlan["step-router-v1"].MaxOutputTokens != 384000 ||
		chinaPlan["stepaudio-2.5-realtime"].Type != "audio" ||
		chinaPlan["step-image-edit-2"].Type != "image-generation" {
		t.Fatalf("incomplete Step Plan provider metadata: %+v", chinaPlan)
	}
}

func providerModelsByID(models []ProviderCatalogModel) map[string]ProviderCatalogModel {
	byID := make(map[string]ProviderCatalogModel, len(models))
	for _, model := range models {
		byID[strings.ToLower(model.ID)] = model
	}
	return byID
}
