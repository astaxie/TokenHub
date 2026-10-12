package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddingRejectsAmbiguousInputsAndOptions(t *testing.T) {
	for _, input := range []any{nil, "", []any{}, []any{"a", 1.0}, map[string]any{"image": "https://example.com/x"}, []any{-1.0}, []any{1.5}} {
		if validateEmbeddingRequest(EmbeddingsRequest{Model: "m", Input: input}) == nil {
			t.Fatalf("accepted invalid input %#v", input)
		}
	}
	for _, field := range []string{"output_type", "embedding_types", "unexpected"} {
		var r EmbeddingsRequest
		if json.Unmarshal([]byte(`{"model":"m","input":"text","`+field+`":"sparse"}`), &r) == nil {
			t.Fatalf("accepted unsupported field %s", field)
		}
	}
}
func TestEmbeddingResponseRestoresIndicesAndEncoding(t *testing.T) {
	req := EmbeddingsRequest{Model: "public", Input: []any{"first", "second"}, EncodingFormat: "base64"}
	body, err := normalizeEmbeddingResponse(map[string]any{"data": []any{map[string]any{"index": 1, "embedding": []float64{3, 4}}, map[string]any{"index": 0, "embedding": []float64{1, 2}}}}, req)
	if err != nil {
		t.Fatal(err)
	}
	items := body["data"].([]any)
	v, err := embeddingVector(items[0].(map[string]any)["embedding"])
	if err != nil || len(v) != 2 || v[0] != 1 {
		t.Fatalf("invalid vector %v: %v", v, err)
	}
	for _, data := range []any{[]any{}, []any{map[string]any{"index": 0, "embedding": []float64{1}}, map[string]any{"index": 0, "embedding": []float64{2}}}, []any{map[string]any{"index": 0, "embedding": []float64{1}}, map[string]any{"index": 1, "embedding": []float64{2, 3}}}} {
		if _, err := normalizeEmbeddingResponse(map[string]any{"data": data}, req); err == nil {
			t.Fatalf("accepted invalid response %#v", data)
		}
	}
}
func TestEmbeddingProtocolsPreserveBatchAndTask(t *testing.T) {
	tests := []struct{ name, path, response, field string }{
		{"openai", "/embeddings", `{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[3,4]}],"usage":{"prompt_tokens":5,"total_tokens":5}}`, "input"},
		{"voyage", "/embeddings", `{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[3,4]}],"usage":{"total_tokens":5}}`, "input"},
		{"jina", "/embeddings", `{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[3,4]}],"usage":{"total_tokens":5}}`, "input"},
		{"cohere", "/embed", `{"embeddings":{"float":[[1,2],[3,4]]},"meta":{"billed_units":{"input_tokens":5}}}`, "texts"},
		{"dashscope", "/services/embeddings/text-embedding/text-embedding", `{"output":{"embeddings":[{"text_index":0,"embedding":[1,2]},{"text_index":1,"embedding":[3,4]}]},"usage":{"total_tokens":5}}`, "input"},
		{"tei", "/embed", `[[1,2],[3,4]]`, "inputs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("path=%s", r.URL.Path)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload[tc.field] == nil {
					t.Errorf("missing %s", tc.field)
				}
				if tc.name == "cohere" && payload["input_type"] != "search_document" {
					t.Errorf("task lost: %v", payload)
				}
				if tc.name == "jina" && payload["task"] != "retrieval.passage" {
					t.Errorf("task lost: %v", payload)
				}
				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Error(err)
				}
			}))
			defer upstream.Close()
			dim := 2
			req := EmbeddingsRequest{Model: "public", Input: []any{"first", "second"}, Dimensions: &dim, EncodingFormat: "base64"}
			if tc.name != "openai" && tc.name != "tei" {
				req.InputType = "document"
			}
			resp, _, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).Embeddings(context.Background(), Provider{BaseURL: upstream.URL, Options: map[string]string{"embedding_protocol": tc.name}}, "upstream", req)
			if err != nil {
				t.Fatal(err)
			}
			body := resp.(map[string]any)
			if body["model"] != "public" || len(body["data"].([]any)) != 2 {
				t.Fatalf("invalid result %v", body)
			}
		})
	}
}
func TestGeminiEmbeddingsUsesIndependentBatchRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":batchEmbedContents") {
			t.Errorf("not a batch request: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests, ok := body["requests"].([]any)
		if !ok || len(requests) != 2 {
			t.Fatalf("invalid batch: %v", body)
		}
		if _, err := w.Write([]byte(`{"embeddings":[{"values":[1,2]},{"values":[3,4]}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	resp, usage, err := (GeminiAdapter{Client: upstream.Client()}).Embeddings(context.Background(), Provider{BaseURL: upstream.URL}, "gemini-embedding-001", EmbeddingsRequest{Model: "public", Input: []any{"first", "second"}, InputType: "query"})
	if err != nil {
		t.Fatal(err)
	}
	body := resp.(map[string]any)
	if len(body["data"].([]any)) != 2 {
		t.Fatal("batch collapsed")
	}
	if body["usage"] != nil || usage.TotalTokens != 0 {
		t.Fatal("invented upstream usage")
	}
}

func TestEmbeddingFailoverRequiresVerifiedSpace(t *testing.T) {
	first := RouteSelection{Provider: Provider{ID: "p1"}, ProviderModel: "m"}
	other := RouteSelection{Provider: Provider{ID: "p2"}, ProviderModel: "m"}
	if got := compatibleEmbeddingRoutes([]RouteSelection{first, other}, embeddingSpaceKey(first)); len(got) != 1 {
		t.Fatal("same model name incorrectly authorized failover")
	}
	first.Provider.Options = map[string]string{"embedding_spaces": `{"m":"space-1"}`}
	other.Provider.Options = map[string]string{"embedding_spaces": `{"m":"space-1"}`}
	if got := compatibleEmbeddingRoutes([]RouteSelection{first, other}, embeddingSpaceKey(first)); len(got) != 2 {
		t.Fatal("verified failover missing")
	}
	other.ProviderModel = "different"
	if got := compatibleEmbeddingRoutes([]RouteSelection{first, other}, embeddingSpaceKey(first)); len(got) != 1 {
		t.Fatal("space assertion leaked to another model")
	}
}

func TestEmbeddingGatewayPreservesIndependentBatch(t *testing.T) {
	server, store, secret := newBackgroundResponseTestServer(t)
	configureEmbeddingTestModel(t, store, "gpt-background")
	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{
		"model": "gpt-background", "input": []string{"first document", "second document"}, "dimensions": 3, "encoding_format": "base64",
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		Data []struct {
			Index     int    `json:"index"`
			Embedding string `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("batch collapsed: %s", response.Body)
	}
	for i, item := range body.Data {
		vector, err := embeddingVector(item.Embedding)
		if err != nil || item.Index != i || len(vector) != 3 {
			t.Fatalf("invalid vector: %+v %v", item, err)
		}
	}
	rejected := doJSON(t, server.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{"model": "gpt-background", "input": []string{"valid", ""}}, secret)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("invalid input status=%d", rejected.Code)
	}
}
