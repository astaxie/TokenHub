package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeminiStoreReachesNativeRequests(t *testing.T) {
	for _, mode := range []string{"chat", "stream", "responses"} {
		for _, value := range []string{"false", "true", "null", "omitted"} {
			t.Run(mode+"/"+value, func(t *testing.T) {
				called := false
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					got, present := body["store"]
					if value == "omitted" || value == "null" {
						if present {
							t.Errorf("unexpected store: %v", got)
						}
					} else if !present || got != (value == "true") {
						t.Errorf("store = %v, present = %t", got, present)
					}
					response := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
					if mode == "stream" {
						_, _ = fmt.Fprintf(w, "data: %s\n\n", response)
					} else {
						_, _ = w.Write([]byte(response))
					}
				}))
				defer upstream.Close()
				adapter := GeminiAdapter{Client: upstream.Client()}
				provider := Provider{BaseURL: upstream.URL + "/v1beta"}
				field := ""
				if value != "omitted" {
					field = `,"store":` + value
				}
				var err error
				if mode == "responses" {
					var req ResponsesRequest
					if err = json.Unmarshal([]byte(`{"model":"test","input":"hello"`+field+`}`), &req); err != nil {
						t.Fatal(err)
					}
					_, _, err = adapter.Responses(context.Background(), provider, "gemini-test", req)
				} else {
					var req ChatCompletionRequest
					if err = json.Unmarshal([]byte(`{"model":"test","messages":[{"role":"user","content":"hello"}]`+field+`}`), &req); err != nil {
						t.Fatal(err)
					}
					if mode == "stream" {
						var out bytes.Buffer
						_, err = adapter.ChatStream(context.Background(), provider, "gemini-test", req, &out)
					} else {
						_, _, err = adapter.Chat(context.Background(), provider, "gemini-test", req)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if !called {
					t.Fatal("no upstream request")
				}
			})
		}
	}
}

func TestGeminiRejectsInvalidStore(t *testing.T) {
	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hello"}],"store":"false"}`), &req); err != nil {
		t.Fatal(err)
	}
	if _, err := buildGeminiRequest("gemini-test", req); err == nil {
		t.Fatal("invalid store accepted")
	}
}
