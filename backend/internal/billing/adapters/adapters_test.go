package adapters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"tokenhub/backend/internal/billing"
)

func TestOneAPIFetchPreservesRequestAndRateLimitFailure(t *testing.T) {
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/log/self" || r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected OneAPI request: path=%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("p") != "2" || r.URL.Query().Get("page_size") != "50" || r.URL.Query().Get("start_timestamp") != strconv.FormatInt(from.Unix(), 10) || r.URL.Query().Get("end_timestamp") != strconv.FormatInt(to.Unix(), 10) {
			t.Fatalf("OneAPI request lost pagination or range: %s", r.URL.RawQuery)
		}
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	_, err := (OneAPIBillingAdapter{Client: upstream.Client()}).Fetch(context.Background(), billing.Connector{BaseURL: upstream.URL, Credentials: map[string]string{"api_token": "token"}}, billing.FetchRequest{From: from, To: to, Cursor: "2", PageSize: 50})
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	var retryable interface {
		Retryable() bool
		RetryAfter() time.Duration
	}
	if !errors.As(err, &retryable) || !retryable.Retryable() || retryable.RetryAfter() != 7*time.Second {
		t.Fatalf("rate-limit retry semantics changed: %v", err)
	}
}

func TestNewAPIAndAliyunFetchNormalizePublicRecords(t *testing.T) {
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	newAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/data/self" || r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("New-Api-User") != "42" {
			t.Fatalf("unexpected NewAPI request: %s headers=%v", r.URL.Path, r.Header)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":[{"created_at":1782864000,"user_id":42,"username":"finance","model_name":"model-a","token_used":3,"count":1,"quota":500000}]}`))
	}))
	defer newAPI.Close()
	newPage, err := (NewAPIBillingAdapter{Client: newAPI.Client()}).Fetch(context.Background(), billing.Connector{BaseURL: newAPI.URL, Credentials: map[string]string{"api_token": "token"}, Config: map[string]string{"user_id": "42"}}, billing.FetchRequest{From: from, To: from.Add(time.Hour), PageSize: 10})
	if err != nil || len(newPage.Records) != 1 || newPage.Records[0].SourceType != billing.ConnectorNewAPI || newPage.Records[0].NetAmount != "1" {
		t.Fatalf("NewAPI record normalization changed: page=%#v err=%v", newPage, err)
	}

	aliyun := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("Aliyun request method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("Action") != "QueryInstanceBill" || r.Form.Get("AccessKeyId") != "key-id" || r.Form.Get("Signature") == "" {
			t.Fatalf("Aliyun signed request changed: %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"Code":"Success","Data":{"TotalCount":1,"Items":{"Item":[{"UsageStartTime":"2026-07-01 08:00:00","UsageEndTime":"2026-07-01 09:00:00","PretaxGrossAmount":"1","PaymentAmount":"1","BillingCycle":"2026-07","ProductCode":"ai"}]}}}`))
	}))
	defer aliyun.Close()
	aliyunPage, err := (AliyunBillingAdapter{Client: aliyun.Client(), Now: func() time.Time { return from }}).Fetch(context.Background(), billing.Connector{BaseURL: aliyun.URL, Credentials: map[string]string{"access_key_id": "key-id", "access_key_secret": "key-secret"}}, billing.FetchRequest{From: from, To: from.Add(24 * time.Hour), PageSize: 10})
	if err != nil || len(aliyunPage.Records) != 1 || aliyunPage.Records[0].SourceType != billing.ConnectorAliyun || aliyunPage.Records[0].NetAmount != "1" {
		t.Fatalf("Aliyun record normalization changed: page=%#v err=%v", aliyunPage, err)
	}
}
