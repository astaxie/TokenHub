package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAlertWebhookDeliveryIsRecorded(t *testing.T) {
	store := NewMemoryStore()
	var received bytes.Buffer
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST webhook, got %s", r.Method)
		}
		_, _ = io.Copy(&received, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer webhook.Close()

	store.CreateResource("notification-channels", AdminResource{
		Name:   "Webhook",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "webhook",
			"webhook_url": webhook.URL,
		},
	})
	alert := AlertEvent{
		ID:        "alt_test",
		ScopeType: "api_key",
		ScopeID:   "key_demo",
		Severity:  "warning",
		Code:      "monthly_cost_near_limit",
		Message:   "Monthly cost quota is near or above limit",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected delivery success, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"status":"success"`) || !strings.Contains(resp.Body, `"status_code":202`) {
		t.Fatalf("unexpected delivery response: %s", resp.Body)
	}
	if !strings.Contains(received.String(), "monthly_cost_near_limit") {
		t.Fatalf("webhook did not receive alert payload: %s", received.String())
	}
	deliveries := store.ListAlertDeliveries()
	if len(deliveries) < 1 || deliveries[0].AlertID != alert.ID || deliveries[0].Status != "success" {
		t.Fatalf("expected recorded delivery, got %+v", deliveries)
	}
}

func TestAlertBotDeliveryFormats(t *testing.T) {
	tests := []struct {
		channelType string
		bodyMarker  string
		fields      map[string]any
		headerKey   string
		headerValue string
	}{
		{channelType: "feishu", bodyMarker: `"msg_type":"text"`},
		{channelType: "dingtalk", bodyMarker: `"msgtype":"text"`},
		{channelType: "wecom", bodyMarker: `"msgtype":"text"`},
		{channelType: "slack", bodyMarker: `"text":"[TokenHub] monitor_check_failed`},
		{channelType: "discord", bodyMarker: `"content":"[TokenHub] monitor_check_failed`},
		{channelType: "telegram", bodyMarker: `"chat_id":"chat_123"`, fields: map[string]any{
			"telegram_bot_token": "telegram-token",
			"telegram_chat_id":   "chat_123",
		}},
		{channelType: "whatsapp", bodyMarker: `"messaging_product":"whatsapp"`, fields: map[string]any{
			"whatsapp_to":     "+15550001111",
			"access_token":    "wa-token",
			"phone_number_id": "phone-number-id",
		}, headerKey: "Authorization", headerValue: "Bearer wa-token"},
	}
	for _, tt := range tests {
		t.Run(tt.channelType, func(t *testing.T) {
			store := NewMemoryStore()
			var received bytes.Buffer
			webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("expected POST webhook, got %s", r.Method)
				}
				if tt.headerKey != "" && r.Header.Get(tt.headerKey) != tt.headerValue {
					t.Fatalf("expected %s header %q, got %q", tt.headerKey, tt.headerValue, r.Header.Get(tt.headerKey))
				}
				_, _ = io.Copy(&received, r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer webhook.Close()

			fields := map[string]any{
				"type":        tt.channelType,
				"webhook_url": webhook.URL,
			}
			for key, value := range tt.fields {
				fields[key] = value
			}
			store.CreateResource("notification-channels", AdminResource{
				Name:   tt.channelType,
				Status: StatusActive,
				Fields: fields,
			})
			alert := AlertEvent{
				ID:        "alt_" + tt.channelType,
				ScopeType: "provider",
				ScopeID:   "prv_test",
				Severity:  "warning",
				Code:      "monitor_check_failed",
				Message:   "Provider failed",
				CreatedAt: time.Now().UTC(),
			}
			if err := store.db.Create(&alert).Error; err != nil {
				t.Fatal(err)
			}
			app := New(store).Handler()

			resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
			if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"success"`) {
				t.Fatalf("expected delivery success, got %d: %s", resp.Code, resp.Body)
			}
			if !strings.Contains(received.String(), tt.bodyMarker) || !strings.Contains(received.String(), "monitor_check_failed") {
				t.Fatalf("unexpected %s payload: %s", tt.channelType, received.String())
			}
		})
	}
}

func TestDingTalkDeliverySignsWebhookWhenSecretConfigured(t *testing.T) {
	store := NewMemoryStore()
	const secret = "SECtestSecret"
	var gotTimestamp, gotSign string
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTimestamp = r.URL.Query().Get("timestamp")
		gotSign = r.URL.Query().Get("sign")
		if gotTimestamp == "" || gotSign == "" {
			t.Fatalf("expected signed dingtalk webhook URL, got %s", r.URL.RawQuery)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(gotTimestamp + "\n" + secret))
		expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if gotSign != expected {
			t.Fatalf("unexpected dingtalk sign: got %q want %q", gotSign, expected)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer webhook.Close()

	store.CreateResource("notification-channels", AdminResource{
		Name:   "DingTalk",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "dingtalk",
			"webhook_url": webhook.URL,
			"secret":      secret,
		},
	})
	alert := AlertEvent{ID: "alt_dingtalk_signed", ScopeType: "provider", ScopeID: "prv_test", Severity: "warning", Code: "monitor_check_failed", Message: "Provider failed", CreatedAt: time.Now().UTC()}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"success"`) {
		t.Fatalf("expected signed dingtalk delivery success, got %d: %s", resp.Code, resp.Body)
	}
	if gotTimestamp == "" || gotSign == "" {
		t.Fatal("dingtalk server was not called with a signature")
	}
}

func TestDingTalkDeliveryRecordsBusinessError(t *testing.T) {
	store := NewMemoryStore()
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"sign mismatch"}`))
	}))
	defer webhook.Close()

	store.CreateResource("notification-channels", AdminResource{
		Name:   "DingTalk",
		Status: StatusActive,
		Fields: map[string]any{
			"type":        "dingtalk",
			"webhook_url": webhook.URL,
		},
	})
	alert := AlertEvent{ID: "alt_dingtalk_error", ScopeType: "provider", ScopeID: "prv_test", Severity: "warning", Code: "monitor_check_failed", Message: "Provider failed", CreatedAt: time.Now().UTC()}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"failed"`) || !strings.Contains(resp.Body, "errcode=310000") {
		t.Fatalf("expected dingtalk business error to be recorded, got %d: %s", resp.Code, resp.Body)
	}
}

func TestAlertEmailDeliveryMissingConfigIsRecorded(t *testing.T) {
	store := NewMemoryStore()
	store.CreateResource("notification-channels", AdminResource{
		Name:   "Email",
		Status: StatusActive,
		Fields: map[string]any{
			"type":     "email",
			"email_to": "ops@example.com",
		},
	})
	alert := AlertEvent{
		ID:        "alt_email",
		ScopeType: "provider",
		ScopeID:   "prv_test",
		Severity:  "warning",
		Code:      "monitor_check_failed",
		Message:   "Provider failed",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&alert).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/alerts/"+alert.ID+"/deliver", map[string]any{}, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, `"status":"failed"`) || !strings.Contains(resp.Body, "smtp_host is required") {
		t.Fatalf("expected recorded email config failure, got %d: %s", resp.Code, resp.Body)
	}
	deliveries := store.ListAlertDeliveries()
	if len(deliveries) < 1 || deliveries[0].Channel != "email" || deliveries[0].Status != "failed" {
		t.Fatalf("expected failed email delivery record, got %+v", deliveries)
	}
}

func TestBillingGenerationUpdatesBudgetsAndInvoices(t *testing.T) {
	store := NewMemoryStore()
	period := time.Now().UTC().Format("2006-01")
	store.CreateResource("teams", AdminResource{
		ID:     "team_finance",
		Name:   "Finance",
		Status: StatusActive,
		Fields: map[string]any{
			"cost_center": "CC-FIN",
		},
	})
	project := store.CreateProject(Project{Name: "Finance App", TeamID: "team_finance"})
	directProject := store.CreateProject(Project{Name: "Direct Cost Center App", TeamID: "team_finance", CostCenter: "CC-DIRECT"})
	store.CreateResource("budgets", AdminResource{
		ID:     "bdg_finance",
		Name:   "Finance monthly budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":        "cost_center",
			"scope_id":     "CC-FIN",
			"period":       "monthly",
			"period_ref":   period,
			"amount_usd":   1,
			"warn_percent": 50,
		},
	})
	store.CreateResource("budgets", AdminResource{
		ID:     "bdg_project",
		Name:   "Project monthly budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":        "project",
			"scope_id":     project.ID,
			"period":       "monthly",
			"period_ref":   period,
			"amount_usd":   2,
			"warn_percent": 90,
		},
	})
	store.CreateResource("budgets", AdminResource{
		ID:     "bdg_direct",
		Name:   "Direct cost center monthly budget",
		Status: StatusActive,
		Fields: map[string]any{
			"scope":        "cost_center",
			"scope_id":     "CC-DIRECT",
			"period":       "monthly",
			"period_ref":   period,
			"amount_usd":   2,
			"warn_percent": 90,
		},
	})
	if err := store.db.Create(&UsageRecord{
		ID:           "use_finance_1",
		RequestID:    "req_finance_1",
		ProjectID:    project.ID,
		APIKeyID:     "key_finance",
		ModelName:    "gpt-4.1-mini",
		ProviderID:   "prv_mock",
		InputTokens:  1000,
		OutputTokens: 1000,
		TotalTokens:  2000,
		CostUSD:      0.75,
		CreatedAt:    time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&UsageRecord{
		ID:           "use_direct_1",
		RequestID:    "req_direct_1",
		ProjectID:    directProject.ID,
		APIKeyID:     "key_direct",
		ModelName:    "gpt-4.1-mini",
		ProviderID:   "prv_mock",
		InputTokens:  100,
		OutputTokens: 100,
		TotalTokens:  200,
		CostUSD:      0.25,
		CreatedAt:    time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/billing/generate", map[string]any{"period": period}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected billing generation, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"chargebacks":2`) || !strings.Contains(resp.Body, `"invoices":2`) {
		t.Fatalf("expected generated chargeback and invoice: %s", resp.Body)
	}
	chargebacks := store.ListResources("chargebacks")
	invoices := store.ListResources("invoices")
	if len(chargebacks) != 2 {
		t.Fatalf("unexpected chargebacks: %+v", chargebacks)
	}
	var hasFinanceChargeback, hasDirectChargeback bool
	for _, chargeback := range chargebacks {
		if stringField(chargeback.Fields, "cost_center") == "CC-FIN" {
			hasFinanceChargeback = true
		}
		if stringField(chargeback.Fields, "cost_center") == "CC-DIRECT" {
			hasDirectChargeback = true
		}
	}
	if !hasFinanceChargeback || !hasDirectChargeback {
		t.Fatalf("expected finance and direct cost center chargebacks: %+v", chargebacks)
	}
	if len(invoices) != 2 {
		t.Fatalf("unexpected invoices: %+v", invoices)
	}
	var hasDirectInvoice bool
	for _, invoice := range invoices {
		if strings.Contains(stringField(invoice.Fields, "invoice_note"), "CC-DIRECT") {
			hasDirectInvoice = true
		}
	}
	if !hasDirectInvoice {
		t.Fatalf("expected direct cost center invoice: %+v", invoices)
	}
	budgets := store.ListResources("budgets")
	if len(budgets) != 3 {
		t.Fatalf("expected two budgets, got %+v", budgets)
	}
	var costCenterBudget, projectBudget, directBudget AdminResource
	for _, budget := range budgets {
		switch budget.ID {
		case "bdg_finance":
			costCenterBudget = budget
		case "bdg_project":
			projectBudget = budget
		case "bdg_direct":
			directBudget = budget
		}
	}
	if float64Field(costCenterBudget.Fields, "used_usd") != 0.75 ||
		float64Field(projectBudget.Fields, "used_usd") != 0.75 ||
		float64Field(directBudget.Fields, "used_usd") != 0.25 {
		t.Fatalf("expected budget usage update: %+v", budgets)
	}
	alerts := store.ListAlerts()
	if len(alerts) != 1 || alerts[0].Code != "budget_warn_threshold" {
		t.Fatalf("expected budget threshold alert, got %+v", alerts)
	}
}

func TestInvoiceConfirmRejectAndStructuredExport(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "finance-admin",
		Name:     "Finance Admin",
		Email:    "finance-admin@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	invoice := store.CreateResource("invoices", AdminResource{
		ID:     "inv_confirm_me",
		Name:   "2026-06 CC-FIN internal invoice",
		Status: "pending",
		Fields: map[string]any{
			"period":       "2026-06",
			"cost_center":  "CC-FIN",
			"amount_usd":   12.34,
			"invoice_note": "Initial note",
		},
	})
	rejected := store.CreateResource("invoices", AdminResource{
		ID:     "inv_reject_me",
		Name:   "2026-06 CC-RND internal invoice",
		Status: "pending",
		Fields: map[string]any{
			"period":       "2026-06",
			"cost_center":  "CC-RND",
			"amount_usd":   2.5,
			"invoice_note": "Needs review",
		},
	})
	app := New(store).Handler()

	confirmed := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{
		"invoice_note": "PO-2026-06-FIN",
	}, "")
	if confirmed.Code != http.StatusOK {
		t.Fatalf("expected invoice confirm 200, got %d: %s", confirmed.Code, confirmed.Body)
	}
	if !strings.Contains(confirmed.Body, `"status":"confirmed"`) ||
		!strings.Contains(confirmed.Body, `"confirmed_by":"Finance Admin"`) ||
		!strings.Contains(confirmed.Body, `"invoice_note":"PO-2026-06-FIN"`) {
		t.Fatalf("unexpected confirm body: %s", confirmed.Body)
	}
	again := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{}, "")
	if again.Code != http.StatusConflict || !strings.Contains(again.Body, "invoice_already_decided") {
		t.Fatalf("expected already decided conflict, got %d: %s", again.Code, again.Body)
	}

	rejectResp := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+rejected.ID+"/reject", map[string]any{
		"reject_reason": "department disputed allocation",
	}, "")
	if rejectResp.Code != http.StatusOK {
		t.Fatalf("expected invoice reject 200, got %d: %s", rejectResp.Code, rejectResp.Body)
	}
	if !strings.Contains(rejectResp.Body, `"status":"rejected"`) ||
		!strings.Contains(rejectResp.Body, `"reject_reason":"department disputed allocation"`) {
		t.Fatalf("unexpected reject body: %s", rejectResp.Body)
	}

	exported := doJSON(t, app, http.MethodGet, "/api/admin/export/invoices", nil, "")
	if exported.Code != http.StatusOK {
		t.Fatalf("expected invoice export 200, got %d: %s", exported.Code, exported.Body)
	}
	if !strings.HasPrefix(exported.Body, "period,cost_center,amount_usd,invoice_note,confirmed_by,confirmed_at,reject_reason,status,updated_at") {
		t.Fatalf("expected structured invoice csv, got: %s", exported.Body)
	}
	if !strings.Contains(exported.Body, "2026-06,CC-FIN,12.34,PO-2026-06-FIN,Finance Admin") ||
		!strings.Contains(exported.Body, "2026-06,CC-RND,2.5,Needs review,,,department disputed allocation,rejected") {
		t.Fatalf("expected invoice rows in export: %s", exported.Body)
	}
	filtered := doJSON(t, app, http.MethodGet, "/api/admin/export/invoices?period=2026-05", nil, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("expected filtered invoice export 200, got %d: %s", filtered.Code, filtered.Body)
	}
	if strings.Contains(filtered.Body, "CC-FIN") || strings.Contains(filtered.Body, "CC-RND") {
		t.Fatalf("period filtered export should not include 2026-06 rows: %s", filtered.Body)
	}
	audit := store.ListAuditEvents()
	if len(audit) < 3 {
		t.Fatalf("expected audit events for invoice actions and export, got %+v", audit)
	}
}

func TestInvoiceConfirmCanRequireApproval(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAdminUser(AdminUser{
		Username: "approver",
		Name:     "Approver",
		Email:    "approver@tokenhub.local",
		Role:     "admin",
		Status:   StatusActive,
	}, "admin123456"); err != nil {
		t.Fatal(err)
	}
	projectApprover, err := store.CreateAdminUser(AdminUser{
		Username: "project-approver",
		Name:     "Project Approver",
		Email:    "project-approver@tokenhub.local",
		Role:     "project_admin",
		Status:   StatusActive,
	}, "admin123456")
	if err != nil {
		t.Fatal(err)
	}
	_, projectSession, err := store.AuthenticateAdminUser(projectApprover.Email, "admin123456", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	invoice := store.CreateResource("invoices", AdminResource{
		ID:     "inv_needs_approval",
		Name:   "2026-06 CC-AI internal invoice",
		Status: "pending",
		Fields: map[string]any{
			"period":       "2026-06",
			"cost_center":  "CC-AI",
			"amount_usd":   100,
			"invoice_note": "Pending approval",
		},
	})
	store.CreateResource("approval-flows", AdminResource{
		Name:   "Invoice confirmation approval",
		Status: StatusActive,
		Fields: map[string]any{
			"trigger":       "invoice_confirm",
			"approver_role": "admin",
			"threshold_usd": 50,
		},
	})
	app := New(store).Handler()

	confirm := doJSON(t, app, http.MethodPost, "/api/admin/resources/invoices/"+invoice.ID+"/confirm", map[string]any{
		"invoice_note": "Approve this invoice",
	}, "")
	if confirm.Code != http.StatusAccepted {
		t.Fatalf("expected invoice confirmation approval, got %d: %s", confirm.Code, confirm.Body)
	}
	if !strings.Contains(confirm.Body, `"approval_required":true`) || !strings.Contains(confirm.Body, `"trigger":"invoice_confirm"`) {
		t.Fatalf("expected invoice approval payload: %s", confirm.Body)
	}
	pendingInvoices := store.ListResources("invoices")
	if len(pendingInvoices) != 1 || pendingInvoices[0].Status != "pending" {
		t.Fatalf("invoice should remain pending before approval, got %+v", pendingInvoices)
	}
	approvals := store.ListApprovalRequests()
	if len(approvals) != 1 || approvals[0].ResourceID != invoice.ID {
		t.Fatalf("expected one invoice approval, got %+v", approvals)
	}
	forbidden := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, projectSession.Token)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body, "approval_role_forbidden") {
		t.Fatalf("expected approval role forbidden, got %d: %s", forbidden.Code, forbidden.Body)
	}
	approved := doJSON(t, app, http.MethodPost, "/api/admin/approvals/"+approvals[0].ID+"/approve", map[string]any{}, "")
	if approved.Code != http.StatusOK {
		t.Fatalf("expected invoice approval apply, got %d: %s", approved.Code, approved.Body)
	}
	items := store.ListResources("invoices")
	if len(items) != 1 || items[0].Status != "confirmed" || stringField(items[0].Fields, "confirmed_by") != "Approver" {
		t.Fatalf("expected approved invoice confirmation, got %+v", items)
	}
	var applied bool
	for _, event := range store.ListAuditEvents() {
		if event.Action == "apply_approval" && event.ResourceType == "invoices" && event.ResourceID == invoice.ID {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("expected apply_approval audit event, got %+v", store.ListAuditEvents())
	}
}

func TestSQLiteBackupCreateDownloadRestoreAndDelete(t *testing.T) {
	tmp := t.TempDir()
	store, err := NewSQLiteStoreWithConfig("sqlite:"+filepath.Join(tmp, "tokenhub.db"), Config{
		AdminToken:      "dev_admin_token",
		SQLiteBackupDir: filepath.Join(tmp, "backups"),
		SecretKey:       "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Backup Restore Project", Status: StatusActive})
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/sqlite/backups", map[string]any{"expire_days": 7}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create backup failed: %d %s", created.Code, created.Body)
	}
	var backup SQLiteBackupRecord
	if err := json.Unmarshal([]byte(created.Body), &backup); err != nil {
		t.Fatal(err)
	}
	if backup.ID == "" || backup.Status != "ready" || backup.SizeBytes <= 0 || backup.ChecksumSHA256 == "" {
		t.Fatalf("unexpected backup payload: %+v body=%s", backup, created.Body)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.GetProject(project.ID); ok {
		t.Fatal("project should be deleted before restore")
	}

	invalidRestore := doJSON(t, app, http.MethodPost, "/api/admin/sqlite/backups/"+backup.ID+"/restore", map[string]any{
		"confirmation": "RESTORE wrong",
	}, "")
	if invalidRestore.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid restore confirmation, got %d %s", invalidRestore.Code, invalidRestore.Body)
	}

	restored := doJSON(t, app, http.MethodPost, "/api/admin/sqlite/backups/"+backup.ID+"/restore", map[string]any{
		"confirmation": "RESTORE " + backup.ID,
	}, "")
	if restored.Code != http.StatusOK {
		t.Fatalf("restore failed: %d %s", restored.Code, restored.Body)
	}
	if _, ok := store.GetProject(project.ID); !ok {
		t.Fatalf("project %s should exist after restore", project.ID)
	}

	download := doJSON(t, app, http.MethodGet, "/api/admin/sqlite/backups/"+backup.ID+"/download", nil, "")
	if download.Code != http.StatusOK || !strings.Contains(download.Body, "SQLite format") {
		t.Fatalf("download failed: %d %q", download.Code, download.Body[:minInt(len(download.Body), 80)])
	}

	listed := doJSON(t, app, http.MethodGet, "/api/admin/sqlite/backups", nil, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body, backup.ID) {
		t.Fatalf("list backups failed: %d %s", listed.Code, listed.Body)
	}

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/sqlite/backups/"+backup.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete backup failed: %d %s", deleted.Code, deleted.Body)
	}
}
