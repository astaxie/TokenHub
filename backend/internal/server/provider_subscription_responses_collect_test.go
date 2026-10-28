package server

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestSubscriptionResponsesAggregateLimitPreservesUsageAndAcceptedFrames(t *testing.T) {
	const usageData = `{"type":"response.in_progress","response":{"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`
	const deltaData = `{"type":"response.output_text.delta","delta":"accepted"}`
	const overflowData = `{"type":"response.output_text.delta","delta":"over-limit"}`
	accepted := ": keepalive\r\ndata: " + usageData + "\r\n\r\ndata: " + deltaData + "\n\n"
	source := accepted + "data: " + overflowData + "\n\n"
	var output bytes.Buffer
	_, text, usage, err := consumeSubscriptionResponsesStreamWithLimit(strings.NewReader(source), &output, len(usageData)+len(deltaData))
	if err == nil || AsHTTPError(err).Status != 502 || text != "accepted" || usage.TotalTokens != 6 {
		t.Fatalf("aggregate rejection lost accepted text or usage: text=%q usage=%+v error=%v", text, usage, err)
	}
	if output.String() != accepted {
		t.Fatal("collector changed accepted framing or forwarded the over-limit frame")
	}
}

func TestSubscriptionResponsesAcceptsExactAggregateLimit(t *testing.T) {
	const terminal = `{"type":"response.completed","response":{"output":[],"usage":{"total_tokens":6}}}`
	const source = "event: response.completed\r\ndata: " + terminal + "\r\n\r\n"
	var output bytes.Buffer
	_, _, usage, err := consumeSubscriptionResponsesStreamWithLimit(strings.NewReader(source), &output, len(terminal))
	if err != nil || usage.TotalTokens != 6 || output.String() != source {
		t.Fatalf("valid boundary completion changed: usage=%+v error=%v", usage, err)
	}
}

func TestSubscriptionResponsesDefaultLimitBoundsManySmallEvents(t *testing.T) {
	chunk := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + strings.Repeat("x", 1<<20) + "\"}\n\n"
	readers := make([]io.Reader, 0, 34)
	for range 33 {
		readers = append(readers, strings.NewReader(chunk))
	}
	readers = append(readers, strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
	_, text, _, err := consumeSubscriptionResponsesStream(io.MultiReader(readers...), nil)
	if err == nil || len(text) >= 32<<20 {
		t.Fatalf("small frames bypassed the 32 MiB aggregate limit: retained=%d error=%v", len(text), err)
	}
}

func TestSubscriptionResponsesHydratesIndexedItemsInOrder(t *testing.T) {
	item := func(text string) map[string]any {
		return map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": text}}}
	}
	for _, test := range []struct {
		name      string
		indexes   []int
		texts     []string
		terminal  []any
		want      string
		wantCount int
	}{
		{name: "parallel output", indexes: []int{1, 0}, texts: []string{"second", "first"}, want: "firstsecond", wantCount: 2},
		{name: "updated index", indexes: []int{1, 0, 1}, texts: []string{"stale", "first", "updated"}, want: "firstupdated", wantCount: 2},
		{name: "unindexed compatibility", indexes: []int{-1, -1}, texts: []string{"first", "second"}, want: "firstsecond", wantCount: 2},
		{name: "mixed slots", indexes: []int{1, -1, 0, -1}, texts: []string{"second", "legacy-one", "first", "legacy-two"}, want: "firstlegacy-onesecondlegacy-two", wantCount: 4},
		{name: "complete terminal wins", indexes: []int{1, 0}, texts: []string{"second", "first"}, terminal: []any{item("authoritative")}, want: "authoritative", wantCount: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var source strings.Builder
			emit := func(event map[string]any) {
				encoded, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				source.WriteString("data: " + string(encoded) + "\n\n")
			}
			for position, index := range test.indexes {
				event := map[string]any{"type": "response.output_item.done", "item": item(test.texts[position])}
				if index >= 0 {
					event["output_index"] = index
				}
				emit(event)
			}
			emit(map[string]any{"type": "response.completed", "response": map[string]any{"output": test.terminal}})
			var output bytes.Buffer
			response, text, _, err := consumeSubscriptionResponsesStream(strings.NewReader(source.String()), &output)
			items, _ := response["output"].([]any)
			if err != nil || text != test.want || codexResponseOutputText(response) != test.want || len(items) != test.wantCount {
				t.Fatalf("hydrated output changed: text=%q count=%d error=%v", text, len(items), err)
			}
			if output.String() != source.String() {
				t.Fatal("hydration reordered the provider's SSE frames")
			}
		})
	}
}
