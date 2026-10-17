package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRerankRetainsUsageFromCompleteLeadingObjectOnDeliveryFailure(t *testing.T) {
	tests := []struct {
		name       string
		profile    string
		body       string
		wantTokens int64
		wantSearch int64
	}{
		{
			name:       "token usage with trailing bytes",
			profile:    "jina",
			body:       `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":7}} trailing garbage`,
			wantTokens: 7,
		},
		{
			name:       "native search usage with trailing bytes",
			profile:    "cohere",
			body:       `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"billed_units":{"search_units":3}}} trailing garbage`,
			wantSearch: 3,
		},
		{
			name:       "token usage over response limit",
			profile:    "jina",
			body:       `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":7}}` + strings.Repeat(" ", maxRerankResponseBytes),
			wantTokens: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			}))
			defer upstream.Close()
			_, usage, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).Rerank(
				context.Background(),
				Provider{BaseURL: upstream.URL, Options: map[string]string{"rerank_protocol": tt.profile}},
				"upstream",
				RerankRequest{Model: "m", Query: "q", Documents: []string{"d"}},
			)
			if err == nil {
				t.Fatal("malformed delivery unexpectedly succeeded")
			}
			if usage.TotalTokens != tt.wantTokens {
				t.Fatalf("token usage=%d want=%d (usage=%+v, error=%v)", usage.TotalTokens, tt.wantTokens, usage, err)
			}
			if usage.RetrievalEvidence == nil || usage.RetrievalEvidence.Quantity == nil {
				t.Fatalf("leading usage evidence was lost: usage=%+v error=%v", usage, err)
			}
			if tt.wantSearch > 0 {
				if *usage.RetrievalEvidence.Quantity != tt.wantSearch || usage.RetrievalEvidence.Unit != "search_unit" {
					t.Fatalf("search usage=%+v want=%d search units", usage.RetrievalEvidence, tt.wantSearch)
				}
			} else if *usage.RetrievalEvidence.Quantity != tt.wantTokens {
				t.Fatalf("token evidence=%+v want=%d", usage.RetrievalEvidence, tt.wantTokens)
			}
		})
	}
}

func TestRerankDoesNotInferUsageFromIncompleteObject(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":7}`)
	}))
	defer upstream.Close()
	_, usage, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).Rerank(
		context.Background(),
		Provider{BaseURL: upstream.URL, Options: map[string]string{"rerank_protocol": "jina"}},
		"upstream",
		RerankRequest{Model: "m", Query: "q", Documents: []string{"d"}},
	)
	if err == nil {
		t.Fatal("incomplete response unexpectedly succeeded")
	}
	if usage.TotalTokens != 0 || usage.RetrievalEvidence != nil {
		t.Fatalf("inferred usage from incomplete object: usage=%+v error=%v", usage, err)
	}
}

func TestRerankCompleteUsageReachesFailedAttemptAndRequestQuotaAccounting(t *testing.T) {
	app, store, key := newRerankCompletionFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":7}} trailing garbage`)
	})
	if err := store.db.Model(&APIKey{}).Where("name = ?", "rerank completion").Update("token_limit_tpm", 1000).Error; err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{
		"model": "rank-complete", "query": "q", "documents": []string{"d"},
	}, key)
	if response.Code < http.StatusBadRequest {
		t.Fatalf("malformed response unexpectedly succeeded: %d %s", response.Code, response.Body)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].TotalTokens != 7 {
		t.Fatalf("complete provider usage was lost from attempt accounting: %+v", attempts)
	}
	var entry meteringEntry
	if err := store.db.First(&entry, "id = ?", attempts[0].RequestID+":shadow").Error; err != nil {
		t.Fatal(err)
	}
	var settlement struct {
		Attempts []meteringAttemptCharge `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(entry.Payload), &settlement); err != nil {
		t.Fatal(err)
	}
	if len(settlement.Attempts) != 1 || settlement.Attempts[0].Charge.Charge == nil || settlement.Attempts[0].Charge.Charge.USD != "0.000007000000" {
		t.Fatalf("complete provider usage was lost from settlement: %+v", settlement)
	}
	var counters []struct {
		TotalTokens int64
	}
	if err := store.db.Table("quota_buckets").Where("scope = ?", "minute").Find(&counters).Error; err != nil {
		t.Fatal(err)
	}
	if len(counters) == 0 || counters[0].TotalTokens != 7 {
		t.Fatalf("complete provider usage was lost from request quota accounting: %+v", counters)
	}
}

func TestRerankReadFailurePreservesUsageAndErrorClassification(t *testing.T) {
	const body = `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":7}}`
	for _, failure := range []error{io.ErrUnexpectedEOF, context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: &rerankFailureBody{Reader: strings.NewReader(body), failure: fmt.Errorf("private-rerank-payload: %w", failure)}}, nil
			})}
			_, usage, err := (OpenAICompatibleAdapter{Client: client}).Rerank(context.Background(), Provider{BaseURL: "http://127.0.0.1", Options: map[string]string{"rerank_protocol": "jina"}}, "upstream", RerankRequest{Model: "m", Query: "q", Documents: []string{"d"}})
			if err == nil || usage.TotalTokens != 7 || strings.Contains(err.Error(), "private-rerank-payload") {
				t.Fatalf("read failure lost evidence or exposed response data: usage=%+v error=%v", usage, err)
			}
			if failure == context.Canceled && (!errors.Is(err, context.Canceled) || providerErrorDisposition(err) != ProviderErrorClient) {
				t.Fatalf("cancellation classification changed: %v", err)
			}
			if failure == context.DeadlineExceeded && (AsHTTPError(err).Status != http.StatusGatewayTimeout || providerErrorDisposition(err) != ProviderErrorTransientSame) {
				t.Fatalf("timeout classification changed: %v", err)
			}
		})
	}
}

type rerankFailureBody struct {
	*strings.Reader
	failure error
}

func (body *rerankFailureBody) Read(data []byte) (int, error) {
	n, err := body.Reader.Read(data)
	if err == io.EOF {
		return n, body.failure
	}
	return n, err
}

func (*rerankFailureBody) Close() error { return nil }
