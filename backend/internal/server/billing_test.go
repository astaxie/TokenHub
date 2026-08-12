package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBillingConnectorLifecycleProtectsCredentials(t *testing.T) {
	store := NewMemoryStore()
	app := NewWithConfig(store, Config{
		AdminToken: "dev_admin_token",
		SecretKey:  "billing-test-secret",
	}).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":                      "Finance OneAPI",
		"type":                      "oneapi",
		"base_url":                  "https://billing.example.test",
		"status":                    StatusActive,
		"schedule_interval_minutes": 60,
		"config": map[string]string{
			"provider_id":          "provider-finance",
			"provider_resource_id": "resource-finance",
		},
		"credentials": map[string]string{
			"api_token": "oneapi-super-secret",
		},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create billing connector: %d %s", created.Code, created.Body)
	}
	if strings.Contains(created.Body, "oneapi-super-secret") {
		t.Fatalf("create response exposed credentials: %s", created.Body)
	}
	var connector BillingConnector
	if err := json.Unmarshal([]byte(created.Body), &connector); err != nil {
		t.Fatal(err)
	}
	if connector.ID == "" || !connector.CredentialsConfigured || connector.Config["provider_id"] != "provider-finance" || connector.Config["provider_resource_id"] != "resource-finance" {
		t.Fatalf("expected created connector and credential summary: %#v", connector)
	}

	listed := doJSON(t, app, http.MethodGet, "/api/admin/billing/connectors", nil, "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list billing connectors: %d %s", listed.Code, listed.Body)
	}
	if strings.Contains(listed.Body, "oneapi-super-secret") {
		t.Fatalf("list response exposed credentials: %s", listed.Body)
	}

	var stored BillingConnector
	if err := store.db.First(&stored, "id = ?", connector.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.CredentialCiphertext, "enc:v1:") {
		t.Fatalf("credential was not encrypted at rest: %q", stored.CredentialCiphertext)
	}
	if strings.Contains(stored.CredentialCiphertext, "oneapi-super-secret") {
		t.Fatalf("credential ciphertext contains plaintext secret")
	}
	leakyConfig := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name": "Unsafe", "type": BillingConnectorOneAPI, "base_url": "https://billing.example.test",
		"config": map[string]string{"private_key": "must-not-be-plain"},
	}, "")
	if leakyConfig.Code != http.StatusBadRequest {
		t.Fatalf("expected plaintext credential config to be rejected: %d %s", leakyConfig.Code, leakyConfig.Body)
	}
	leakyURL := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name": "Unsafe URL", "type": BillingConnectorOneAPI, "base_url": "https://billing.example.test?sas_token=must-not-be-plain",
		"credentials": map[string]string{"api_token": "encrypted-token"},
	}, "")
	if leakyURL.Code != http.StatusBadRequest {
		t.Fatalf("expected query credentials in base_url to be rejected: %d %s", leakyURL.Code, leakyURL.Body)
	}

	disabled := doJSON(t, app, http.MethodPatch, "/api/admin/billing/connectors/"+connector.ID, map[string]any{
		"status": StatusDisabled,
	}, "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body, `"status":"disabled"`) {
		t.Fatalf("disable billing connector: %d %s", disabled.Code, disabled.Body)
	}
}

func TestOneAPIBillingSyncRetriesPaginatesAndIsIdempotent(t *testing.T) {
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/log/self" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("authorization") != "Bearer oneapi-token" {
			t.Fatalf("missing OneAPI bearer credential: %q", r.Header.Get("authorization"))
		}
		if r.URL.Query().Get("start_timestamp") != strconv.FormatInt(from.Unix(), 10) ||
			r.URL.Query().Get("end_timestamp") != strconv.FormatInt(to.Unix(), 10) {
			t.Fatalf("sync range was not forwarded: %s", r.URL.RawQuery)
		}
		call := requests.Add(1)
		if call == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporary"}`))
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("p"))
		if page <= 0 {
			page = 1
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"items":[` +
			`{"id":"log-` + strconv.Itoa(page) + `","created_at":` + strconv.FormatInt(from.Add(time.Duration(page)*time.Hour).Unix(), 10) +
			`,"model_name":"deepseek-chat","quota":500000,"prompt_tokens":100,"completion_tokens":50,"token_name":"finance-key"}` +
			`],"page":` + strconv.Itoa(page) + `,"page_size":1,"total":2}}`))
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "billing-test-secret"})
	app := server.Handler()
	invalid := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Invalid NewAPI",
		"type":     BillingConnectorNewAPI,
		"base_url": upstream.URL,
		"config":   map[string]string{"user_id": "0"},
	}, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, "invalid_billing_config") {
		t.Fatalf("expected invalid NewAPI user id to be rejected: %d %s", invalid.Code, invalid.Body)
	}
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Finance OneAPI",
		"type":     BillingConnectorOneAPI,
		"base_url": upstream.URL,
		"config": map[string]string{
			"currency":       "USD",
			"quota_per_unit": "500000",
			"page_size":      "1",
			"max_retries":    "3",
			"retry_base_ms":  "1",
		},
		"credentials": map[string]string{"api_token": "oneapi-token"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create connector: %d %s", created.Code, created.Body)
	}
	var connector BillingConnector
	if err := json.Unmarshal([]byte(created.Body), &connector); err != nil {
		t.Fatal(err)
	}

	syncPath := "/api/admin/billing/connectors/" + connector.ID + "/sync"
	first := doJSON(t, app, http.MethodPost, syncPath, map[string]any{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}, "")
	if first.Code != http.StatusOK {
		t.Fatalf("sync OneAPI connector: %d %s", first.Code, first.Body)
	}
	var firstRun BillingSyncRun
	if err := json.Unmarshal([]byte(first.Body), &firstRun); err != nil {
		t.Fatal(err)
	}
	if firstRun.Status != BillingSyncSucceeded || firstRun.RecordsInserted != 2 || firstRun.PagesFetched != 2 || firstRun.Attempts != 3 {
		t.Fatalf("unexpected first sync result: %#v", firstRun)
	}

	recordsResponse := doJSON(t, app, http.MethodGet, "/api/admin/billing/records?connector_id="+connector.ID, nil, "")
	if recordsResponse.Code != http.StatusOK {
		t.Fatalf("list billing records: %d %s", recordsResponse.Code, recordsResponse.Body)
	}
	var recordsPayload struct {
		Data []BillingRecord `json:"data"`
	}
	if err := json.Unmarshal([]byte(recordsResponse.Body), &recordsPayload); err != nil {
		t.Fatal(err)
	}
	if len(recordsPayload.Data) != 2 {
		t.Fatalf("expected two normalized records: %s", recordsResponse.Body)
	}
	if recordsPayload.Data[0].Currency != "USD" || recordsPayload.Data[0].NetAmount != "1" || recordsPayload.Data[0].RawPayload != "" {
		t.Fatalf("record was not normalized/redacted: %#v", recordsPayload.Data[0])
	}

	second := doJSON(t, app, http.MethodPost, syncPath, map[string]any{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}, "")
	if second.Code != http.StatusOK {
		t.Fatalf("repeat sync: %d %s", second.Code, second.Body)
	}
	var secondRun BillingSyncRun
	if err := json.Unmarshal([]byte(second.Body), &secondRun); err != nil {
		t.Fatal(err)
	}
	if secondRun.RecordsInserted != 0 || secondRun.RecordsUpdated != 2 {
		t.Fatalf("repeat sync was not idempotent: %#v", secondRun)
	}
	var recordCount int64
	if err := store.db.Model(&BillingRecord{}).Where("connector_id = ?", connector.ID).Count(&recordCount).Error; err != nil {
		t.Fatal(err)
	}
	if recordCount != 2 {
		t.Fatalf("repeat sync created duplicate records: %d", recordCount)
	}
	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/billing/connectors/"+connector.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete connector: %d %s", deleted.Code, deleted.Body)
	}
	if len(store.ListBillingRecords(connector.ID, 10)) != 2 || len(store.ListBillingSyncRuns(connector.ID, 10)) != 2 {
		t.Fatalf("deleting connector removed billing audit history")
	}
	var snapshotCount int64
	if err := store.db.Model(&BillingRawSnapshot{}).Where("connector_id = ?", connector.ID).Count(&snapshotCount).Error; err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("deleting connector removed raw snapshots: %d", snapshotCount)
	}
}

func TestNewAPIBillingSyncUsesQuotaEndpointAndChunksRange(t *testing.T) {
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(45 * 24 * time.Hour)
	type requestedRange struct {
		from int64
		to   int64
	}
	var ranges []requestedRange
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/data/self" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("authorization") != "Bearer newapi-token" {
			t.Fatalf("missing NewAPI bearer credential: %q", r.Header.Get("authorization"))
		}
		if r.Header.Get("New-Api-User") != "42" {
			t.Fatalf("missing NewAPI user header: %q", r.Header.Get("New-Api-User"))
		}
		start, startErr := strconv.ParseInt(r.URL.Query().Get("start_timestamp"), 10, 64)
		end, endErr := strconv.ParseInt(r.URL.Query().Get("end_timestamp"), 10, 64)
		if startErr != nil || endErr != nil {
			t.Fatalf("invalid NewAPI time range: %s", r.URL.RawQuery)
		}
		if end-start > int64((30*24*time.Hour)/time.Second) {
			t.Fatalf("NewAPI request exceeded the 30-day limit: %d", end-start)
		}
		ranges = append(ranges, requestedRange{from: start, to: end})
		model := "newapi-first"
		if start != from.Unix() {
			model = "newapi-second"
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":[` +
			`{"user_id":42,"username":"finance","model_name":"` + model + `","created_at":` + strconv.FormatInt(start+3600, 10) +
			`,"token_used":150,"count":2,"quota":500000}]}`))
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "billing-test-secret"})
	app := server.Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Finance NewAPI",
		"type":     BillingConnectorNewAPI,
		"base_url": upstream.URL,
		"config": map[string]string{
			"currency":       "USD",
			"quota_per_unit": "500000",
			"user_id":        "42",
		},
		"credentials": map[string]string{"api_token": "newapi-token"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create NewAPI connector: %d %s", created.Code, created.Body)
	}
	var connector BillingConnector
	if err := json.Unmarshal([]byte(created.Body), &connector); err != nil {
		t.Fatal(err)
	}

	syncPath := "/api/admin/billing/connectors/" + connector.ID + "/sync"
	first := doJSON(t, app, http.MethodPost, syncPath, map[string]any{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}, "")
	if first.Code != http.StatusOK {
		t.Fatalf("sync NewAPI connector: %d %s", first.Code, first.Body)
	}
	var firstRun BillingSyncRun
	if err := json.Unmarshal([]byte(first.Body), &firstRun); err != nil {
		t.Fatal(err)
	}
	if firstRun.Status != BillingSyncSucceeded || firstRun.RecordsInserted != 2 || firstRun.PagesFetched != 2 {
		t.Fatalf("unexpected NewAPI sync result: %#v", firstRun)
	}
	if len(ranges) != 2 || ranges[0].from != from.Unix() || ranges[0].to != from.Add(30*24*time.Hour).Unix() ||
		ranges[1].from != ranges[0].to+1 || ranges[1].to != to.Unix() {
		t.Fatalf("unexpected NewAPI request ranges: %#v", ranges)
	}

	records := store.ListBillingRecords(connector.ID, 10)
	if len(records) != 2 {
		t.Fatalf("expected two normalized NewAPI records, got %#v", records)
	}
	for _, record := range records {
		if record.SourceType != BillingConnectorNewAPI || record.Service != "newapi" || record.AccountID != "42" ||
			record.Currency != "USD" || record.NetAmount != "1" || record.UsageQuantity != 150 || record.Metadata["request_count"] != "2" {
			t.Fatalf("NewAPI record was not normalized: %#v", record)
		}
	}

	second := doJSON(t, app, http.MethodPost, syncPath, map[string]any{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}, "")
	if second.Code != http.StatusOK {
		t.Fatalf("repeat NewAPI sync: %d %s", second.Code, second.Body)
	}
	var secondRun BillingSyncRun
	if err := json.Unmarshal([]byte(second.Body), &secondRun); err != nil {
		t.Fatal(err)
	}
	if secondRun.RecordsInserted != 0 || secondRun.RecordsUpdated != 2 || len(store.ListBillingRecords(connector.ID, 10)) != 2 {
		t.Fatalf("repeat NewAPI sync was not idempotent: %#v", secondRun)
	}
}

func TestNewAPIBillingRejectsContractDrift(t *testing.T) {
	for name, payload := range map[string]string{
		"missing success envelope":   `{"data":[]}`,
		"missing required row field": `{"success":true,"data":[{"user_id":42,"username":"finance","created_at":1767229200,"token_used":1,"count":1,"quota":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(payload))
			}))
			defer upstream.Close()
			adapter := NewAPIBillingAdapter{Client: upstream.Client()}
			_, err := adapter.Fetch(t.Context(), BillingConnector{
				BaseURL:     upstream.URL,
				Config:      map[string]string{"user_id": "42"},
				Credentials: map[string]string{"api_token": "newapi-token"},
			}, BillingFetchRequest{
				From: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
				To:   time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC),
			})
			if err == nil {
				t.Fatal("expected contract-incompatible NewAPI response to be rejected")
			}
		})
	}
}

func TestAliyunBillingSyncSignsRequestsAndAdvancesBillingCycles(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, time.January, 1, 0, 0, 0, 0, location)
	to := time.Date(2026, time.March, 1, 0, 0, 0, 0, location)
	var cycles []string
	var corrected atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected Aliyun RPC POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("Action") != "QueryInstanceBill" || r.Form.Get("Version") != "2017-12-14" || r.Form.Get("AccessKeyId") != "aliyun-ak" {
			t.Fatalf("missing Aliyun RPC parameters: %#v", r.Form)
		}
		signature := r.Form.Get("Signature")
		r.Form.Del("Signature")
		if expected := testAliyunRPCSignature(r.Form, "aliyun-sk"); !hmac.Equal([]byte(signature), []byte(expected)) {
			t.Fatalf("invalid Aliyun RPC signature: got %q want %q", signature, expected)
		}
		cycle := r.Form.Get("BillingCycle")
		cycles = append(cycles, cycle)
		usageStart := cycle + "-01 00:00:00"
		payment := "9.75"
		refund := "0"
		if cycle == "2026-02" {
			payment = "-2"
			refund = "2"
		} else if corrected.Load() {
			payment = "8.75"
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"Success":true,"Data":{"PageNum":1,"PageSize":1,"TotalCount":1,"Items":{"Item":[{` +
			`"InstanceID":"i-finance","ProductCode":"ecs","ProductName":"Elastic Compute Service",` +
			`"BillingItemCode":"ecs-hour","Currency":"CNY","PretaxGrossAmount":"10.5","InvoiceDiscount":"0.75",` +
			`"TaxAmount":"0","RefundAmount":"` + refund + `","PaymentAmount":"` + payment + `",` +
			`"UsageStartTime":"` + usageStart + `","UsageEndTime":"` + cycle + `-01 01:00:00","BillingCycle":"` + cycle + `"}]}}}`))
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "billing-test-secret"}).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Aliyun Finance",
		"type":     BillingConnectorAliyun,
		"base_url": upstream.URL,
		"config": map[string]string{
			"page_size":       "1",
			"source_timezone": "Asia/Shanghai",
		},
		"credentials": map[string]string{
			"access_key_id":     "aliyun-ak",
			"access_key_secret": "aliyun-sk",
		},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create Aliyun connector: %d %s", created.Code, created.Body)
	}
	var connector BillingConnector
	if err := json.Unmarshal([]byte(created.Body), &connector); err != nil {
		t.Fatal(err)
	}
	synced := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors/"+connector.ID+"/sync", map[string]any{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}, "")
	if synced.Code != http.StatusOK {
		t.Fatalf("sync Aliyun connector: %d %s", synced.Code, synced.Body)
	}
	if strings.Join(cycles, ",") != "2026-01,2026-02" {
		t.Fatalf("expected month-by-month billing requests, got %v", cycles)
	}
	records := store.ListBillingRecords(connector.ID, 10)
	if len(records) != 2 {
		t.Fatalf("expected two Aliyun billing records, got %#v", records)
	}
	var refundRecord BillingRecord
	for _, record := range records {
		if record.BillingPeriod == "2026-02" {
			refundRecord = record
		}
	}
	if refundRecord.SourceType != BillingConnectorAliyun || refundRecord.Currency != "CNY" || refundRecord.RefundAmount != "2" || refundRecord.NetAmount != "-2" {
		t.Fatalf("Aliyun refund was not normalized: %#v", refundRecord)
	}
	corrected.Store(true)
	repeated := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors/"+connector.ID+"/sync", map[string]any{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}, "")
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeat Aliyun sync: %d %s", repeated.Code, repeated.Body)
	}
	var repeatedRun BillingSyncRun
	if err := json.Unmarshal([]byte(repeated.Body), &repeatedRun); err != nil {
		t.Fatal(err)
	}
	if repeatedRun.RecordsInserted != 0 || repeatedRun.RecordsUpdated != 2 || len(store.ListBillingRecords(connector.ID, 10)) != 2 {
		t.Fatalf("corrected Aliyun amounts were not idempotently updated: %#v", repeatedRun)
	}
	for _, record := range store.ListBillingRecords(connector.ID, 10) {
		if record.BillingPeriod == "2026-01" && record.NetAmount != "8.75" {
			t.Fatalf("corrected Aliyun amount was not stored: %#v", record)
		}
	}
}

func TestAliyunBillingSyncFiltersSubMonthRange(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, time.January, 15, 0, 0, 0, 0, location)
	to := time.Date(2026, time.February, 1, 0, 0, 0, 0, location)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"Success":true,"Data":{"TotalCount":2,"Items":{"Item":[` +
			`{"InstanceID":"before-range","ProductCode":"ecs","BillingItemCode":"ecs-hour","PaymentAmount":"1","UsageStartTime":"2026-01-01 00:00:00","UsageEndTime":"2026-01-01 01:00:00","BillingCycle":"2026-01"},` +
			`{"InstanceID":"inside-range","ProductCode":"ecs","BillingItemCode":"ecs-hour","PaymentAmount":"2","UsageStartTime":"2026-01-20 00:00:00","UsageEndTime":"2026-01-20 01:00:00","BillingCycle":"2026-01"}` +
			`]}}}`))
	}))
	defer upstream.Close()

	page, err := (AliyunBillingAdapter{Client: upstream.Client()}).Fetch(t.Context(), BillingConnector{
		Type:    BillingConnectorAliyun,
		BaseURL: upstream.URL,
		Config:  map[string]string{"source_timezone": "Asia/Shanghai"},
		Credentials: map[string]string{
			"access_key_id":     "aliyun-ak",
			"access_key_secret": "aliyun-sk",
		},
	}, BillingFetchRequest{From: from, To: to, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0].Metadata["instance_id"] != "inside-range" {
		t.Fatalf("Aliyun sub-month range was not enforced: %#v", page.Records)
	}
}

func TestBillingSyncFailureKeepsCheckpointAndRetryResumes(t *testing.T) {
	from := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	var failSecondPage atomic.Bool
	failSecondPage.Store(true)
	var pageOneRequests atomic.Int32
	var pageTwoRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("p"))
		if page <= 1 {
			pageOneRequests.Add(1)
		} else {
			pageTwoRequests.Add(1)
			if failSecondPage.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"resume-` + strconv.Itoa(page) + `","created_at":` + strconv.FormatInt(from.Add(time.Duration(page)*time.Hour).Unix(), 10) + `,"quota":500000}],"total":2,"page_size":1}}`))
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "billing-test-secret"}).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":     "Checkpoint OneAPI",
		"type":     BillingConnectorOneAPI,
		"base_url": upstream.URL,
		"config": map[string]string{
			"page_size":     "1",
			"max_retries":   "2",
			"retry_base_ms": "1",
		},
		"credentials": map[string]string{"api_token": "checkpoint-secret"},
	}, "")
	var connector BillingConnector
	if err := json.Unmarshal([]byte(created.Body), &connector); err != nil {
		t.Fatal(err)
	}
	syncPath := "/api/admin/billing/connectors/" + connector.ID + "/sync"
	failed := doJSON(t, app, http.MethodPost, syncPath, map[string]any{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339)}, "")
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("expected failed upstream sync, got %d %s", failed.Code, failed.Body)
	}
	stored, err := store.GetBillingConnector(connector.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Checkpoint, `"cursor":"2"`) {
		t.Fatalf("failed sync did not preserve page checkpoint: %q", stored.Checkpoint)
	}
	runs := store.ListBillingSyncRuns(connector.ID, 10)
	if len(runs) != 1 || runs[0].Status != BillingSyncFailed || runs[0].ErrorCode != "billing_upstream_http_error" {
		t.Fatalf("failed sync run is not diagnosable: %#v", runs)
	}

	failSecondPage.Store(false)
	retried := doJSON(t, app, http.MethodPost, syncPath, map[string]any{}, "")
	if retried.Code != http.StatusOK {
		t.Fatalf("retry sync: %d %s", retried.Code, retried.Body)
	}
	if pageOneRequests.Load() != 1 {
		t.Fatalf("retry restarted from page one instead of checkpoint: %d requests", pageOneRequests.Load())
	}
	if len(store.ListBillingRecords(connector.ID, 10)) != 2 {
		t.Fatalf("checkpoint retry did not complete both records")
	}
	stored, err = store.GetBillingConnector(connector.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Checkpoint != "" || stored.LastSyncStatus != BillingSyncSucceeded || stored.LastSyncedThrough == nil {
		t.Fatalf("successful retry did not advance incremental state: %#v", stored)
	}

	for _, event := range store.ListAuditEvents() {
		serialized, _ := json.Marshal(event)
		if strings.Contains(string(serialized), "checkpoint-secret") {
			t.Fatalf("billing credential leaked into audit event: %s", serialized)
		}
	}
	var snapshots []BillingRawSnapshot
	if err := store.db.Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range snapshots {
		if !strings.HasPrefix(snapshot.PayloadCiphertext, "enc:v1:") || strings.Contains(snapshot.PayloadCiphertext, "resume-") {
			t.Fatalf("raw billing snapshot was not encrypted: %#v", snapshot)
		}
	}
}

func TestBillingConnectorTestAndScheduledSync(t *testing.T) {
	createdAt := time.Now().UTC().Add(-2 * time.Hour)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[{"id":"scheduled-1","created_at":` + strconv.FormatInt(createdAt.Unix(), 10) + `,"quota":250000}],"total":1}}`))
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "billing-test-secret"})
	app := server.Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors", map[string]any{
		"name":                      "Scheduled OneAPI",
		"type":                      BillingConnectorOneAPI,
		"base_url":                  upstream.URL,
		"schedule_interval_minutes": 15,
		"credentials":               map[string]string{"api_token": "scheduled-token"},
	}, "")
	var connector BillingConnector
	if err := json.Unmarshal([]byte(created.Body), &connector); err != nil {
		t.Fatal(err)
	}
	tested := doJSON(t, app, http.MethodPost, "/api/admin/billing/connectors/"+connector.ID+"/test", map[string]any{}, "")
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body, `"ok":true`) {
		t.Fatalf("test billing connector: %d %s", tested.Code, tested.Body)
	}
	dueAt := time.Now().UTC().Add(-time.Minute)
	if err := store.db.Model(&BillingConnector{}).Where("id = ?", connector.ID).Update("next_sync_at", dueAt).Error; err != nil {
		t.Fatal(err)
	}
	runs := server.billing.RunDue(t.Context(), time.Now().UTC())
	if len(runs) != 1 || runs[0].Trigger != "scheduled" || runs[0].Status != BillingSyncSucceeded {
		t.Fatalf("due connector was not synchronized: %#v", runs)
	}
	if repeated := server.billing.RunDue(t.Context(), time.Now().UTC()); len(repeated) != 0 {
		t.Fatalf("connector remained due immediately after scheduled sync: %#v", repeated)
	}
	var foundSystemAudit bool
	for _, event := range store.ListAuditEvents() {
		if event.ActorUserID == "system" && event.ResourceID == connector.ID && event.Action == "sync" {
			foundSystemAudit = true
		}
	}
	if !foundSystemAudit {
		t.Fatalf("scheduled sync did not create a system audit event")
	}
}

func TestBillingRecordStartsInRangeIncludesToBoundary(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	record := BillingRecord{UsageStartAt: base}
	if !billingRecordStartsInRange(record, base, base.Add(time.Hour)) {
		t.Fatal("record inside range should be included")
	}
	if !billingRecordStartsInRange(record, base, base) {
		t.Fatal("record exactly at the upper boundary should be included")
	}
	if billingRecordStartsInRange(record, base.Add(time.Minute), base.Add(time.Hour)) {
		t.Fatal("record before from should be excluded")
	}
}

func TestBillingDecimalValueRejectsInvalid(t *testing.T) {
	if _, err := billingDecimalValue("not-a-number"); err == nil {
		t.Fatal("expected error for invalid decimal")
	}
	if v, err := billingDecimalValue(""); err != nil || v != "0" {
		t.Fatalf("empty value should return 0, got %q, err=%v", v, err)
	}
	if v, err := billingDecimalValue("12.34"); err != nil || v == "" {
		t.Fatalf("valid decimal should parse, got %q, err=%v", v, err)
	}
}

// TestBillingDecimalAddRejectsInvalid guards the discount aggregation: an
// unparseable component must reject the record instead of silently
// understating the discount.
func TestBillingDecimalAddRejectsInvalid(t *testing.T) {
	if _, err := billingDecimalAdd("1.5", "not-a-number"); err == nil {
		t.Fatal("expected error for invalid discount component")
	}
	if v, err := billingDecimalAdd("1.5", "", nil, "2.5"); err != nil || v != "4" {
		t.Fatalf("expected 4, got %q err=%v", v, err)
	}
}

// TestOneAPIBillingRecordPreservesLargeIntegerFields guards the int64 parse
// path: 2^53+1 is not representable as a float64, so converting through
// float64 would silently drop one token and skew the derived amount.
func TestOneAPIBillingRecordPreservesLargeIntegerFields(t *testing.T) {
	large := int64(1<<53 + 1)
	record, err := normalizeOneAPIBillingRecord(map[string]any{
		"id":                "log-large",
		"created_at":        "2026-01-01 00:00:00",
		"quota":             json.Number(strconv.FormatInt(large, 10)),
		"prompt_tokens":     json.Number(strconv.FormatInt(large, 10)),
		"completion_tokens": json.Number("0"),
	}, BillingConnector{Config: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if record.UsageQuantity != large {
		t.Fatalf("expected exact token count %d, got %d", large, record.UsageQuantity)
	}
	expectedAmount := billingDecimalRatio(large, int64(500000))
	if record.GrossAmount != expectedAmount {
		t.Fatalf("expected exact quota amount %s, got %s", expectedAmount, record.GrossAmount)
	}
}

// TestBillingSchedulerRestartsAfterShutdown guards the run-state tracking
// that replaced sync.Once: StartScheduler must work again after Shutdown.
func TestBillingSchedulerRestartsAfterShutdown(t *testing.T) {
	service := newBillingService(NewMemoryStore())
	service.StartScheduler(time.Hour)
	if service.schedulerStop == nil {
		t.Fatal("expected scheduler started")
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	service.StartScheduler(time.Hour)
	if service.schedulerStop == nil {
		t.Fatal("expected scheduler to restart after shutdown")
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

func testAliyunRPCSignature(values map[string][]string, secret string) string {
	canonical := aliyunCanonicalQuery(values)
	stringToSign := "POST&%2F&" + aliyunPercentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
