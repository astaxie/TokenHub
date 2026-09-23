package server

import (
	"context"
	"os"
	"testing"
	"time"
)

// This explicitly opted-in contract smoke sends only synthetic text to TypeSafe.
// It never calls a generation Provider or claims to measure routing accuracy.
func TestJevRoutingClientLive(t *testing.T) {
	key := os.Getenv("TOKENHUB_LIVE_TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("set TOKENHUB_LIVE_TYPESAFE_API_KEY to run the TypeSafe contract smoke")
	}
	client := newJevRoutingClient(Config{TypeSafeAPIKey: key, TypeSafeModel: "jev-1.13.0", SemanticRoutingTimeoutMS: 10000})
	started := time.Now()
	decision, err := client.Evaluate(context.Background(), "Translate the greeting Good morning into French.", []semanticCandidate{
		{ID: "candidate_1", Model: "synthetic-translator", Description: "Synthetic test candidate supporting short text translations.", Capabilities: []string{"translation"}},
		{ID: "candidate_2", Model: "synthetic-coder", Description: "Synthetic test candidate supporting code completion.", Capabilities: []string{"coding"}},
	}, "Choose the model matching the task.")
	if err != nil {
		t.Fatalf("TypeSafe contract smoke failed: %v", err)
	}
	if decision.Choice != "candidate_1" && decision.Choice != "candidate_2" && decision.Choice != "no_preference" {
		t.Fatal("TypeSafe returned an unbounded choice")
	}
	t.Logf("model=%s choice=%s confidence=%.3f input_tokens=%d output_tokens=%d elapsed_ms=%d", decision.Model, decision.Choice, decision.Confidence, decision.InputTokens, decision.OutputTokens, time.Since(started).Milliseconds())
}
