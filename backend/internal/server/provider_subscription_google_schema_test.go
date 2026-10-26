package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestGoogleSubscriptionAntigravityToolSchemaCompatibility(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var request ChatCompletionRequest
			if err := json.Unmarshal([]byte(`{"model":"public","messages":[{"role":"user","content":"Read a file"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object","additionalProperties":false,"properties":{"path":{"type":["string","null"]},"options":{"$ref":"#/$defs/Options"},"count":{"anyOf":[{"type":"integer"},{"type":"null"}]}},"required":["path","options"],"$defs":{"Options":{"type":"object","properties":{"verbose":{"type":"boolean","default":false}}}}}}}]}`), &request); err != nil {
				t.Fatal(err)
			}
			tool := request.Tools.([]any)[0].(map[string]any)["function"].(map[string]any)
			tool["parameters"].(map[string]any)["allOf"] = []any{map[string]any{"required": []any{"count"}}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					return
				}
				var envelope map[string]any
				if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
					t.Fatal(err)
				}
				body := envelope["request"].(map[string]any)
				declaration := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
				if _, exists := declaration["parametersJsonSchema"]; exists {
					t.Error("Antigravity received unsupported parametersJsonSchema")
				}
				var expected any
				_ = json.Unmarshal([]byte(`{"type":"object","properties":{"path":{"type":"string","nullable":true},"options":{"type":"object","properties":{"verbose":{"type":"boolean"}}},"count":{"type":"integer","nullable":true}},"required":["path","options","count"]}`), &expected)
				if !reflect.DeepEqual(declaration["parameters"], expected) {
					t.Errorf("incompatible tool schema: %#v", declaration["parameters"])
				}
				mode := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"]
				if mode != "VALIDATED" {
					t.Errorf("Antigravity Claude tool mode = %v, want VALIDATED", mode)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
			}))
			defer server.Close()
			adapter := googleSubscriptionTestAdapter(server, providerAntigravitySubscription)
			provider := Provider{Type: providerAntigravitySubscription, APIKey: "test-access", Options: map[string]string{"organization_id": "project"}}
			if stream {
				var output bytes.Buffer
				if _, err := adapter.ChatStream(context.Background(), provider, "claude-sonnet", request, &output); err != nil {
					t.Fatal(err)
				}
			} else if _, _, err := adapter.Chat(context.Background(), provider, "claude-sonnet", request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGoogleSubscriptionToolModePreservesExplicitChoiceAndGeminiSchema(t *testing.T) {
	for _, providerType := range []string{providerAntigravitySubscription, providerGeminiCLISubscription} {
		for _, choice := range []string{"auto", "none", "required"} {
			request := ChatCompletionRequest{Tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "read", "parameters": map[string]any{"type": "object", "additionalProperties": false}}}}, ToolChoice: choice}
			body, err := buildGeminiRequest("claude-sonnet", request)
			if err != nil {
				t.Fatal(err)
			}
			adapter := GoogleSubscriptionAdapter{ProviderType: providerType}
			if err := adapter.preparePayload("claude-sonnet", body); err != nil {
				t.Fatal(err)
			}
			mode := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"]
			want := map[string]string{"auto": "AUTO", "none": "NONE", "required": "ANY"}[choice]
			if choice == "auto" && providerType == providerAntigravitySubscription {
				want = "VALIDATED"
			}
			if mode != want {
				t.Errorf("%s choice %s: mode = %v, want %s", providerType, choice, mode, want)
			}
			declaration := body["tools"].([]map[string]any)[0]["functionDeclarations"].([]map[string]any)[0]
			if providerType == providerGeminiCLISubscription && declaration["parametersJsonSchema"] == nil {
				t.Error("Gemini CLI lost its full JSON schema")
			}
		}
	}
}

func TestGoogleSubscriptionAntigravityAddsMissingArrayItems(t *testing.T) {
	got, err := antigravitySubscriptionSchema(map[string]any{"type": "object", "properties": map[string]any{"values": map[string]any{"type": "array"}}})
	want := map[string]any{"type": "object", "properties": map[string]any{"values": map[string]any{"type": "array", "items": map[string]any{}}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("untyped array cannot reach Code Assist: %#v %v", got, err)
	}
}

func TestGoogleSubscriptionAntigravitySchemaExpansionIsBounded(t *testing.T) {
	for name, raw := range map[string]string{
		"recursive":          `{"type":"object","properties":{"node":{"$ref":"#/$defs/Node"}},"$defs":{"Node":{"type":"object","properties":{"left":{"$ref":"#/$defs/Node"},"right":{"$ref":"#/$defs/Node"}}}}}`,
		"missing":            `{"type":"object","properties":{"node":{"$ref":"#/$defs/Missing"}}}`,
		"external":           `{"type":"object","properties":{"node":{"$ref":"https://example.test/schema.json"}}}`,
		"invalid properties": `{"allOf":[{"type":"object","properties":"invalid"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var schema any
			if err := json.Unmarshal([]byte(raw), &schema); err != nil {
				t.Fatal(err)
			}
			if _, err := antigravitySubscriptionSchema(schema); err == nil || AsHTTPError(err).Status != 400 {
				t.Fatalf("expected an explicit client error, got %v", err)
			}
		})
	}
	// A finite shared-reference graph can still expand exponentially without
	// a total node budget, even when every reference is acyclic.
	definitions := map[string]any{"Node12": map[string]any{"type": "string"}}
	for i := 11; i >= 0; i-- {
		ref := map[string]any{"$ref": fmt.Sprintf("#/$defs/Node%d", i+1)}
		definitions[fmt.Sprintf("Node%d", i)] = map[string]any{"type": "object", "properties": map[string]any{"left": ref, "right": ref}}
	}
	schema := map[string]any{"$ref": "#/$defs/Node0", "$defs": definitions}
	if _, err := antigravitySubscriptionSchema(schema); err == nil || AsHTTPError(err).Status != 400 {
		t.Fatalf("shared-reference expansion was not bounded: %v", err)
	}
}

func TestGoogleSubscriptionAntigravitySchemaDoesNotMutateCaller(t *testing.T) {
	var schema any
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"existing":{"type":"string"}},"allOf":[{"type":"object","properties":{"added":{"type":"boolean"}}}],"$defs":{"Shared":{"type":"string"}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(schema)
	if _, err := antigravitySubscriptionSchema(schema); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(schema)
	if !bytes.Equal(before, after) {
		t.Fatal("schema projection mutated the caller's request")
	}
}

func TestGoogleSubscriptionAntigravitySchemaMergesRequiredProperties(t *testing.T) {
	var schema any
	if err := json.Unmarshal([]byte(`{"allOf":[{"type":"object","properties":{"first":{"type":"string"}},"required":["first"]},{"type":"object","properties":{"first":{"type":"string"},"second":{"type":"boolean"}},"required":["first","second"]}]}`), &schema); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(schema)
	result, err := antigravitySubscriptionSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	projected := result.(map[string]any)
	if !reflect.DeepEqual(projected["required"], []any{"first", "second"}) || len(projected["properties"].(map[string]any)) != 2 {
		t.Fatalf("allOf lost required properties: %v", projected)
	}
	after, _ := json.Marshal(schema)
	if !bytes.Equal(before, after) {
		t.Fatal("allOf projection mutated the caller's schema")
	}
}

func TestGoogleSubscriptionAntigravitySchemaPreservesCrossBranchRequirements(t *testing.T) {
	for name, raw := range map[string]string{
		"sibling":          `{"type":"object","properties":{"value":{"type":"string"}},"allOf":[{"required":["value"]}]}`,
		"branches":         `{"allOf":[{"type":"object","properties":{"value":{"type":"string"}}},{"required":["value"]}]}`,
		"nested reference": `{"allOf":[{"type":"object","properties":{"value":{"type":"string"}}},{"allOf":[{"$ref":"#/$defs/Required"}]}],"$defs":{"Required":{"required":["value"]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var schema any
			if err := json.Unmarshal([]byte(raw), &schema); err != nil {
				t.Fatal(err)
			}
			result, err := antigravitySubscriptionSchema(schema)
			if err != nil {
				t.Fatal(err)
			}
			projected := result.(map[string]any)
			if !reflect.DeepEqual(projected["required"], []any{"value"}) {
				t.Fatalf("allOf dropped a required property declared elsewhere: %v", projected)
			}
		})
	}
}
