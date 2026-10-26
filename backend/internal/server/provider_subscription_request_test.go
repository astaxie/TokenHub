package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCopilotSubscriptionResponsesPreservesSamplingParameters(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			adapter, provider := copilotSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					_, _ = fmt.Fprintf(w, `{"token":"session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
					return
				case "/models":
					_, _ = fmt.Fprint(w, `{"data":[{"id":"test-model","supported_endpoints":["/responses"],"capabilities":{"type":"chat"}}]}`)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["temperature"] != float64(0) || body["top_p"] != 0.8 || body["user"] != "test-user" {
					t.Errorf("Responses conversion lost caller parameters: %#v", body)
				}
				response := `{"id":"resp-test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
				} else {
					_, _ = fmt.Fprint(w, response)
				}
			})
			var request ChatCompletionRequest
			if err := json.Unmarshal([]byte(`{"model":"public","messages":[{"role":"user","content":"Hello"}],"temperature":0,"top_p":0.8,"user":"test-user"}`), &request); err != nil {
				t.Fatal(err)
			}
			var err error
			if stream {
				var output bytes.Buffer
				_, err = adapter.ChatStream(context.Background(), provider, "test-model", request, &output)
			} else {
				_, _, err = adapter.Chat(context.Background(), provider, "test-model", request)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
