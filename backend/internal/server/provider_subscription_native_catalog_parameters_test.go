package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestGoogleSubscriptionCatalogAdmitsEncodedChatControls(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					return
				}
				switch r.URL.Path {
				case "/v1internal:retrieveUserQuota":
					writeJSON(w, 200, map[string]any{"buckets": []any{map[string]any{"modelId": "gemini-3-pro-preview"}}})
				case "/v1internal:listExperiments":
					writeJSON(w, 200, map[string]any{"flags": []any{}})
				case "/v1internal:fetchAvailableModels":
					writeJSON(w, 200, map[string]any{"models": map[string]any{"gemini-3-flash": map[string]any{}, "claude-sonnet-4-6": map[string]any{}, "gpt-5.4": map[string]any{}}})
				default:
					t.Errorf("Unexpected catalog request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(upstream.Close)
			adapter := googleSubscriptionTestAdapter(upstream, providerType)
			catalog, err := adapter.ModelsWithCredentials(context.Background(), ProviderResourceCredentials{AccessToken: "synthetic-access", OrganizationID: "project"})
			if err != nil || len(catalog.Models) == 0 {
				t.Fatalf("Catalog discovery failed: models=%d error=%v", len(catalog.Models), err)
			}
			for _, entry := range catalog.Models {
				t.Run(entry.ID, func(t *testing.T) {
					model := providerModelFromCatalog("provider", entry)
					controls := []struct{ field, value, wire string }{
						{"tools", `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`, ""},
						{"tool_choice", `"auto"`, ""}, {"parallel_tool_calls", "true", ""},
						{"max_tokens", "128", "maxOutputTokens"}, {"max_completion_tokens", "128", "maxOutputTokens"},
						{"temperature", "0.5", "temperature"}, {"top_p", "0.9", "topP"}, {"stop", `["END"]`, "stopSequences"},
					}
					_, supportsEffort := geminiThinkingConfig(entry.ID, "high")
					if supportsEffort {
						controls = append(controls, struct{ field, value, wire string }{"reasoning_effort", `"high"`, "thinkingConfig"})
					}
					for _, control := range controls {
						t.Run(control.field, func(t *testing.T) {
							request := nativeSubscriptionCatalogRequest(t, control.field, control.value)
							assertNativeSubscriptionCatalogAdmits(t, model, request)
							payload, err := buildGeminiRequest(entry.ID, request)
							if err != nil || adapter.preparePayload(entry.ID, payload) != nil {
								t.Fatalf("Advertised request was not encodable: %v", err)
							}
							if control.wire != "" {
								config, _ := payload["generationConfig"].(map[string]any)
								if config[control.wire] == nil {
									t.Fatalf("Advertised control was dropped: %s -> %s", control.field, control.wire)
								}
							} else if payload["tools"] == nil || payload["toolConfig"] == nil {
								t.Fatal("Advertised tool controls were dropped")
							}
						})
					}
					for _, control := range []struct{ field, value string }{
						{"response_format", `{"type":"json_object"}`}, {"presence_penalty", "0.5"}, {"frequency_penalty", "0.5"},
						{"min_p", "0.2"}, {"top_k", "3"}, {"reasoning", `{"effort":"high"}`}, {"max_output_tokens", "128"},
					} {
						assertNativeSubscriptionCatalogRejects(t, model, control.field, control.value)
					}
					if !supportsEffort {
						assertNativeSubscriptionCatalogRejects(t, model, "reasoning_effort", `"high"`)
					}
					request := nativeSubscriptionCatalogRequest(t, "parallel_tool_calls", "false")
					if _, err := buildGeminiRequest(entry.ID, request); err == nil {
						t.Fatal("Parameter declaration removed the unsupported parallel=false constraint")
					}
				})
			}
		})
	}
}

func TestClaudeSubscriptionCatalogAdmitsOnlyCLIChatControls(t *testing.T) {
	adapter, provider, ctx := claudeSubscriptionFixture(t)
	catalog, _, err := adapter.ResourceModels(ctx, provider, ProviderResource{ID: "account"}, "")
	if err != nil || len(catalog.Models) == 0 {
		t.Fatalf("Catalog discovery failed: models=%d error=%v", len(catalog.Models), err)
	}
	for _, entry := range catalog.Models {
		t.Run(entry.ID, func(t *testing.T) {
			model := providerModelFromCatalog("provider", entry)
			for _, control := range []struct{ field, value string }{
				{"tools", `[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`},
				{"tool_choice", `"auto"`}, {"tool_choice", `"none"`}, {"parallel_tool_calls", "true"},
				{"max_tokens", "128"}, {"max_completion_tokens", "128"}, {"reasoning_effort", `"medium"`},
			} {
				t.Run(control.field+"="+control.value, func(t *testing.T) {
					request := nativeSubscriptionCatalogRequest(t, control.field, control.value)
					assertNativeSubscriptionCatalogAdmits(t, model, request)
					if _, err := claudeSubscriptionTools(request); err != nil {
						t.Fatalf("Advertised CLI controls were rejected: %v", err)
					}
				})
			}
			for _, control := range []struct{ field, value string }{
				{"temperature", "0.5"}, {"top_p", "0.9"}, {"stop", `["END"]`}, {"response_format", `{"type":"json_object"}`},
				{"presence_penalty", "0.5"}, {"frequency_penalty", "0.5"}, {"min_p", "0.2"}, {"top_k", "3"},
				{"reasoning", `{"effort":"high"}`}, {"max_output_tokens", "128"},
			} {
				assertNativeSubscriptionCatalogRejects(t, model, control.field, control.value)
			}
			for _, control := range []struct{ field, value string }{{"tool_choice", `"required"`}, {"parallel_tool_calls", "false"}} {
				request := nativeSubscriptionCatalogRequest(t, control.field, control.value)
				if _, err := claudeSubscriptionTools(request); err == nil {
					t.Fatalf("Parameter declaration removed the value-specific constraint: %s=%s", control.field, control.value)
				}
			}
		})
	}
}

func TestClaudeSubscriptionCatalogBudgetsAndEffortReachCLI(t *testing.T) {
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		t.Run(field, func(t *testing.T) {
			adapter, provider, ctx := claudeSubscriptionFixture(t)
			adapter.ExecutableArgs = []string{"-test.run=^TestNativeSubscriptionCatalogCLIControlsProcess$", "--", "tokenhub-catalog-cli-controls"}
			catalog, _, err := adapter.ResourceModels(ctx, provider, ProviderResource{ID: "account"}, "")
			if err != nil || len(catalog.Models) == 0 {
				t.Fatalf("Catalog discovery failed: %v", err)
			}
			request := nativeSubscriptionCatalogRequest(t, field, "128")
			effort := "medium"
			request.ReasoningEffort = &effort
			model := providerModelFromCatalog("provider", catalog.Models[0])
			assertNativeSubscriptionCatalogAdmits(t, model, request)
			response, _, err := adapter.Chat(ctx, provider, model.UpstreamModel, request)
			if err != nil {
				t.Fatalf("Imported CLI controls did not reach inference: %v", err)
			}
			if choiceText(response.(map[string]any)) != "verified" {
				t.Fatal("CLI helper did not verify the output budget and reasoning effort")
			}
		})
	}
}

func TestNativeSubscriptionCatalogCLIControlsProcess(t *testing.T) {
	if !slices.Contains(os.Args, "tokenhub-catalog-cli-controls") {
		return
	}
	effort := slices.Index(os.Args, "--effort")
	if effort < 0 || effort+1 >= len(os.Args) || os.Args[effort+1] != "medium" || os.Getenv("CLAUDE_CODE_MAX_OUTPUT_TOKENS") != "128" {
		os.Exit(12)
	}
	var input any
	if json.NewDecoder(os.Stdin).Decode(&input) != nil {
		os.Exit(13)
	}
	output := json.NewEncoder(os.Stdout)
	if err := output.Encode(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "verified"}}}}); err != nil {
		os.Exit(14)
	}
	if err := output.Encode(map[string]any{"type": "result", "is_error": false}); err != nil {
		os.Exit(15)
	}
	os.Exit(0)
}

func nativeSubscriptionCatalogRequest(t *testing.T, field, value string) ChatCompletionRequest {
	t.Helper()
	body := map[string]json.RawMessage{"model": json.RawMessage(`"public-model"`), "messages": json.RawMessage(`[{"role":"user","content":"Hello"}]`), field: json.RawMessage(value)}
	if field == "tool_choice" || field == "parallel_tool_calls" {
		body["tools"] = json.RawMessage(`[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]`)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var request ChatCompletionRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func assertNativeSubscriptionCatalogAdmits(t *testing.T, model ProviderModel, request ChatCompletionRequest) {
	t.Helper()
	before := append([]string(nil), model.SupportedParameters...)
	if compatibility := jevChatCompatibility(model, request); compatibility.Reason != "" {
		t.Fatalf("Imported catalog rejected an encoded control: %+v parameters=%v", compatibility, model.SupportedParameters)
	}
	if !semanticModelFitsRequest(model, request, 5) {
		t.Fatal("Explicit parameter declarations regressed legacy semantic eligibility")
	}
	if !reflect.DeepEqual(model.SupportedParameters, before) || model.Metadata["request_parameter_profile"] != "" {
		t.Fatal("Admission mutated the catalog or granted a blanket parameter profile")
	}
}

func assertNativeSubscriptionCatalogRejects(t *testing.T, model ProviderModel, field, value string) {
	t.Helper()
	result := jevChatCompatibility(model, nativeSubscriptionCatalogRequest(t, field, value))
	if result.Reason != "parameter_not_declared" || result.Parameter != field {
		t.Fatalf("Unsupported control escaped catalog admission: %s=%s result=%+v", field, value, result)
	}
}
