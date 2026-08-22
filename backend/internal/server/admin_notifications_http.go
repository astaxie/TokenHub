package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAdminAlerts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "alert", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.store.ListAlerts()})
}

func (s *Server) handleAdminAlertItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "alert", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/alerts/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "deliver" {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	s.serveAdminAlertDelivery(w, r, user, parts[0])
}

func (s *Server) serveAdminAlertDelivery(w http.ResponseWriter, r *http.Request, user AdminUser, alertID string) {
	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	delivery, err := s.deliverAlert(r.Context(), alertID, req.ChannelID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "deliver", "alert", alertID, "", delivery)
	writeJSON(w, http.StatusOK, delivery)
}

func (s *Server) handleAdminAlertDeliveries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "alert", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": redactAlertDeliveriesForResponse(s.store.ListAlertDeliveries())})
}

func (s *Server) handleAdminApprovals(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "approval", r.Method)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.filterApprovalRequestsForUser(user, s.store.ListApprovalRequests())})
}

func (s *Server) handleAdminApprovalItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "approval", r.Method)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/approvals/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "approve" && parts[1] != "reject") {
		writeError(w, r, NewHTTPError(404, "not_found", "Not found"))
		return
	}
	if r.Method != http.MethodPost {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	s.serveAdminApprovalAction(w, r, user, parts[0], parts[1])
}

func (s *Server) serveAdminApprovalAction(w http.ResponseWriter, r *http.Request, user AdminUser, approvalID string, action string) {
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, &req); err != nil {
			writeError(w, r, err)
			return
		}
	}
	status := "approved"
	if action == "reject" {
		status = "rejected"
	}
	pending, err := s.store.GetApprovalRequest(approvalID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.requireApprovalRole(pending, user); err != nil {
		writeError(w, r, err)
		return
	}
	var result any
	if status == "approved" {
		result, err = s.applyApprovalRequest(pending, user)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "apply_approval", pending.ResourceType, pending.ResourceID, pending, result)
	}
	item, err := s.store.UpdateApprovalRequestStatus(approvalID, status, user.ID, req.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, status, "approval", item.ID, "", item)
	writeJSON(w, http.StatusOK, map[string]any{"approval": item, "result": result})
}

func (s *Server) requireApprovalRole(request ApprovalRequest, user AdminUser) error {
	role := normalizeAdminRole(user.Role)
	if projectID := approvalProjectID(request); projectID != "" && !isPlatformAdminRole(role) {
		project, ok := s.store.GetProject(projectID)
		if role != "team_leader" || !s.activeTeamIDSet()[user.TeamID] || !ok || strings.TrimSpace(user.TeamID) == "" || user.TeamID != project.TeamID {
			return NewHTTPError(http.StatusForbidden, "approval_primary_team_forbidden", "Only the project's primary team leader can decide this approval")
		}
	}
	if strings.TrimSpace(request.FlowID) == "" {
		return nil
	}
	flow, err := s.findResource("approval-flows", request.FlowID)
	if err != nil {
		return err
	}
	required := strings.TrimSpace(stringField(flow.Fields, "approver_role"))
	if required == "" {
		return nil
	}
	if !adminRoleMatches(user.Role, required) {
		return NewHTTPError(http.StatusForbidden, "approval_role_forbidden", "Admin role is not allowed to decide this approval")
	}
	return nil
}

func approvalProjectID(request ApprovalRequest) string {
	payload := map[string]any{}
	if strings.TrimSpace(request.Payload) == "" || json.Unmarshal([]byte(request.Payload), &payload) != nil {
		return ""
	}
	if projectID := strings.TrimSpace(stringFromPayload(payload, "project_id")); projectID != "" {
		return projectID
	}
	fields := fieldsFromPayload(payload["fields"])
	if strings.ToLower(strings.TrimSpace(firstStringField(fields, "scope", "scope_type"))) != "project" {
		return ""
	}
	return strings.TrimSpace(firstStringField(fields, "scope_id", "project_id"))
}

func (s *Server) approvalRequired(user AdminUser, trigger string, resourceType string, resourceID string, payload any) (ApprovalRequest, bool) {
	flow, ok := s.matchApprovalFlow(trigger, payload)
	if !ok {
		return ApprovalRequest{}, false
	}
	return s.createApprovalRequest(user, flow.ID, trigger, resourceType, resourceID, payload), true
}

func (s *Server) createApprovalRequest(user AdminUser, flowID string, trigger string, resourceType string, resourceID string, payload any) ApprovalRequest {
	request := ApprovalRequest{
		FlowID:       flowID,
		Trigger:      trigger,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		RequesterID:  user.ID,
		Requester:    user.Name,
		Status:       "pending",
		Payload:      snapshotJSON(payload),
	}
	return s.store.CreateApprovalRequest(request)
}

func (s *Server) matchApprovalFlow(trigger string, payload any) (AdminResource, bool) {
	flows := s.store.ListResources("approval-flows")
	payloadCost := approvalPayloadCost(payload)
	for _, flow := range flows {
		if flow.Status != StatusActive {
			continue
		}
		if strings.TrimSpace(stringField(flow.Fields, "trigger")) != trigger {
			continue
		}
		threshold := float64Field(flow.Fields, "threshold_usd")
		if threshold > 0 && payloadCost > 0 && payloadCost < threshold {
			continue
		}
		return flow, true
	}
	return AdminResource{}, false
}

func (s *Server) apiKeyUpdateApproval(user AdminUser, key APIKey, patch APIKey) (ApprovalRequest, bool) {
	trigger := ""
	quotaLimitsSet := patch.LimitsSet || patch.Limits != (QuotaLimits{})
	minuteLimitsSet := patch.RateLimitSet || patch.TokenLimitSet
	if quotaLimitsSet || minuteLimitsSet {
		trigger = "quota_increase"
	}
	if trigger == "" && (patch.ModelAccessMode != "" || patch.Allowed != nil) {
		trigger = "model_access"
	}
	if trigger == "" {
		return ApprovalRequest{}, false
	}
	payload := map[string]any{
		"api_key_id":        key.ID,
		"project_id":        key.ProjectID,
		"requested_action":  trigger,
		"owner_user_id":     patch.OwnerUserID,
		"allowed_models":    patch.Allowed,
		"model_access_mode": patch.ModelAccessMode,
		"status":            patch.Status,
	}
	if quotaLimitsSet {
		payload["limits"] = patch.Limits
	}
	if patch.RateLimitSet {
		payload["rate_limit_rpm"] = patch.RateLimitRPM
	}
	if patch.TokenLimitSet {
		payload["token_limit_tpm"] = patch.TokenLimitTPM
	}
	return s.approvalRequired(user, trigger, "api_key", key.ID, payload)
}

func (s *Server) adminResourceApproval(user AdminUser, kind string, resourceID string, resource AdminResource) (ApprovalRequest, bool) {
	trigger := ""
	switch kind {
	case "budgets":
		trigger = "budget_change"
	case "quota-policies":
		trigger = "quota_increase"
	default:
		return ApprovalRequest{}, false
	}
	if resourceID != "" {
		if existing, err := s.findResource(kind, resourceID); err == nil {
			resource = mergedAdminResource(existing, resource)
		}
	}
	payload := map[string]any{
		"kind":             kind,
		"resource_id":      resourceID,
		"name":             resource.Name,
		"description":      resource.Description,
		"status":           resource.Status,
		"fields":           resource.Fields,
		"requested_action": trigger,
	}
	if projectID := s.approvalResourceProjectID(kind, resource); projectID != "" {
		payload["project_id"] = projectID
	}
	return s.approvalRequired(user, trigger, kind, resourceID, payload)
}

func (s *Server) approvalResourceProjectID(kind string, resource AdminResource) string {
	if projectID := projectScopedResourceProjectID(resource); projectID != "" {
		return projectID
	}
	if kind != "quota-policies" || strings.TrimSpace(resource.ID) == "" {
		return ""
	}
	projectID := ""
	for _, project := range s.store.ListProjects() {
		if strings.TrimSpace(project.DefaultQuotaRef) != resource.ID {
			continue
		}
		if projectID != "" && projectID != project.ID {
			return ""
		}
		projectID = project.ID
	}
	return projectID
}

func mergedAdminResource(existing AdminResource, patch AdminResource) AdminResource {
	if patch.Name != "" {
		existing.Name = patch.Name
	}
	existing.Description = patch.Description
	if patch.Status != "" {
		existing.Status = patch.Status
	}
	if patch.Fields != nil {
		existing.Fields = patch.Fields
	}
	return existing
}

func approvalPayloadCost(payload any) float64 {
	switch typed := payload.(type) {
	case map[string]any:
		if fields, ok := typed["fields"].(map[string]any); ok {
			if amount := float64Field(fields, "amount_usd"); amount > 0 {
				return amount
			}
		}
		if limits, ok := typed["limits"].(QuotaLimits); ok {
			return limits.MonthlyCostUSD
		}
		if limits, ok := typed["limits"].(map[string]any); ok {
			if amount := float64Field(limits, "monthly_cost_usd"); amount > 0 {
				return amount
			}
			return float64Field(limits, "daily_cost_usd")
		}
	}
	return 0
}

func (s *Server) deliverAlert(ctx context.Context, alertID string, channelID string) (AlertDelivery, error) {
	alert, err := s.store.GetAlert(alertID)
	if err != nil {
		return AlertDelivery{}, err
	}
	channel, err := s.resolveNotificationChannel(channelID)
	if err != nil {
		return AlertDelivery{}, err
	}
	payload := map[string]any{
		"source":     "tokenhub",
		"alert":      alert,
		"channel":    channel.Name,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
		"severity":   alert.Severity,
		"scope":      alert.ScopeType,
		"scope_id":   alert.ScopeID,
		"message":    alert.Message,
		"event_code": alert.Code,
	}
	delivery := AlertDelivery{
		AlertID:   alert.ID,
		ChannelID: channel.ID,
		Channel:   normalizeNotificationChannelType(stringField(channel.Fields, "type")),
		Target:    notificationChannelTarget(channel),
		Status:    "success",
		Payload:   snapshotJSON(payload),
	}
	if delivery.Channel == "" {
		delivery.Channel = "webhook"
	}
	if !supportedNotificationChannel(delivery.Channel) {
		delivery.Status = "failed"
		delivery.Error = "unsupported notification channel"
		return s.recordAlertDelivery(channel, delivery), nil
	}
	if delivery.Channel == "email" {
		if err := sendEmailAlert(ctx, channel, alert); err != nil {
			delivery.Status = "failed"
			delivery.Error = err.Error()
		}
		return s.recordAlertDelivery(channel, delivery), nil
	}
	target, err := notificationChannelRequestTarget(channel)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery), nil
	}
	bodyPayload, headers, err := notificationChannelPayloadForChannel(channel, payload, alert)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery), nil
	}
	body, _ := json.Marshal(bodyPayload)
	if delivery.Channel == "dingtalk" {
		target, err = signedDingTalkWebhookURL(target, firstStringField(channel.Fields, "secret", "sign_secret", "dingtalk_secret"))
		if err != nil {
			delivery.Status = "failed"
			delivery.Error = err.Error()
			return s.recordAlertDelivery(channel, delivery), nil
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery), nil
	}
	req.Header.Set("content-type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery), nil
	}
	defer resp.Body.Close()
	delivery.StatusCode = resp.StatusCode
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		delivery.Status = "failed"
		delivery.Error = resp.Status
	} else if err := notificationChannelResponseError(delivery.Channel, resp.Header.Get("content-type"), respBody); err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
	}
	return s.recordAlertDelivery(channel, delivery), nil
}

func signedDingTalkWebhookURL(rawURL string, secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return rawURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	query := parsed.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", sign)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func notificationChannelResponseError(channelType string, contentType string, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	switch normalizeNotificationChannelType(channelType) {
	case "dingtalk":
		if code := int64Field(payload, "errcode"); code != 0 {
			return fmt.Errorf("dingtalk response error: errcode=%d errmsg=%s", code, stringField(payload, "errmsg"))
		}
	case "feishu":
		if code := int64Field(payload, "code"); code != 0 {
			return fmt.Errorf("feishu response error: code=%d msg=%s", code, firstStringField(payload, "msg", "message"))
		}
	}
	return nil
}

func normalizeNotificationChannelType(channelType string) string {
	normalized := strings.ToLower(strings.TrimSpace(channelType))
	switch normalized {
	case "", "webhook":
		return "webhook"
	case "feishu", "lark":
		return "feishu"
	case "dingtalk", "dingding", "ding_talk":
		return "dingtalk"
	case "wecom", "wechat_work", "weixin_work", "enterprise_wechat":
		return "wecom"
	case "slack":
		return "slack"
	case "discord":
		return "discord"
	case "telegram", "tg":
		return "telegram"
	case "whatsapp", "whatsapp_cloud", "whatsapp_business", "wa":
		return "whatsapp"
	case "email", "mail", "smtp":
		return "email"
	default:
		return normalized
	}
}

func supportedNotificationChannel(channelType string) bool {
	switch normalizeNotificationChannelType(channelType) {
	case "webhook", "feishu", "dingtalk", "wecom", "slack", "discord", "telegram", "whatsapp", "email":
		return true
	default:
		return false
	}
}

func notificationChannelPayload(channelType string, payload map[string]any, alert AlertEvent) any {
	text := notificationChannelText(alert)
	switch normalizeNotificationChannelType(channelType) {
	case "feishu":
		return map[string]any{
			"msg_type": "text",
			"content":  map[string]any{"text": text},
		}
	case "dingtalk", "wecom":
		return map[string]any{
			"msgtype": "text",
			"text":    map[string]any{"content": text},
		}
	case "slack":
		return map[string]any{
			"text": text,
		}
	case "discord":
		return map[string]any{
			"content":          text,
			"allowed_mentions": map[string]any{"parse": []string{}},
		}
	default:
		return payload
	}
}

func notificationChannelPayloadForChannel(channel AdminResource, payload map[string]any, alert AlertEvent) (any, map[string]string, error) {
	fields := channel.Fields
	text := notificationChannelText(alert)
	switch normalizeNotificationChannelType(stringField(fields, "type")) {
	case "telegram":
		chatID := strings.TrimSpace(firstStringField(fields, "telegram_chat_id", "chat_id", "recipient", "to"))
		if chatID == "" {
			return nil, nil, fmt.Errorf("telegram_chat_id is required")
		}
		body := map[string]any{
			"chat_id":                  chatID,
			"text":                     text,
			"disable_web_page_preview": true,
		}
		if threadID := strings.TrimSpace(firstStringField(fields, "telegram_thread_id", "message_thread_id", "thread_id")); threadID != "" {
			body["message_thread_id"] = threadID
		}
		return body, nil, nil
	case "whatsapp":
		recipient := strings.TrimSpace(firstStringField(fields, "whatsapp_to", "recipient", "to"))
		if recipient == "" {
			return nil, nil, fmt.Errorf("whatsapp_to is required")
		}
		accessToken := strings.TrimSpace(firstStringField(fields, "access_token", "whatsapp_access_token", "token", "secret"))
		if accessToken == "" {
			return nil, nil, fmt.Errorf("access_token is required")
		}
		return map[string]any{
				"messaging_product": "whatsapp",
				"to":                recipient,
				"type":              "text",
				"text": map[string]any{
					"preview_url": false,
					"body":        text,
				},
			}, map[string]string{
				"authorization": "Bearer " + accessToken,
			}, nil
	default:
		return notificationChannelPayload(stringField(fields, "type"), payload, alert), nil, nil
	}
}

func notificationChannelText(alert AlertEvent) string {
	return fmt.Sprintf("[TokenHub] %s\n%s\n对象：%s/%s", alert.Code, alert.Message, alert.ScopeType, alert.ScopeID)
}

func notificationChannelTarget(channel AdminResource) string {
	channelType := normalizeNotificationChannelType(stringField(channel.Fields, "type"))
	if channelType == "email" {
		return firstStringField(channel.Fields, "email_to", "recipients", "to")
	}
	if channelType == "telegram" {
		if chatID := strings.TrimSpace(firstStringField(channel.Fields, "telegram_chat_id", "chat_id", "recipient", "to")); chatID != "" {
			return "telegram:" + chatID
		}
		return "telegram"
	}
	if channelType == "whatsapp" {
		if recipient := strings.TrimSpace(firstStringField(channel.Fields, "whatsapp_to", "recipient", "to")); recipient != "" {
			return "whatsapp:" + recipient
		}
		return "whatsapp"
	}
	return redactNotificationDeliveryURL(firstStringField(channel.Fields, "webhook_url", "url"))
}

func notificationChannelRequestTarget(channel AdminResource) (string, error) {
	fields := channel.Fields
	channelType := normalizeNotificationChannelType(stringField(fields, "type"))
	if target := strings.TrimSpace(firstStringField(fields, "webhook_url", "url")); target != "" {
		return target, nil
	}
	switch channelType {
	case "telegram":
		botToken := strings.TrimSpace(firstStringField(fields, "telegram_bot_token", "bot_token", "token", "secret"))
		if botToken == "" {
			return "", fmt.Errorf("telegram_bot_token is required")
		}
		return "https://api.telegram.org/bot" + botToken + "/sendMessage", nil
	case "whatsapp":
		phoneNumberID := strings.TrimSpace(firstStringField(fields, "whatsapp_phone_number_id", "phone_number_id"))
		if phoneNumberID == "" {
			return "", fmt.Errorf("whatsapp_phone_number_id is required")
		}
		apiVersion := strings.Trim(strings.TrimSpace(firstStringField(fields, "whatsapp_api_version", "api_version")), "/")
		if apiVersion == "" {
			apiVersion = "v20.0"
		}
		return "https://graph.facebook.com/" + apiVersion + "/" + phoneNumberID + "/messages", nil
	default:
		return "", fmt.Errorf("webhook_url is required")
	}
}

func sendEmailAlert(ctx context.Context, channel AdminResource, alert AlertEvent) error {
	fields := channel.Fields
	from := strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from"))
	recipients := splitNotificationRecipients(firstStringField(fields, "email_to", "recipients", "to"))
	if len(recipients) == 0 {
		return fmt.Errorf("email_to is required")
	}
	return sendEmail(ctx, fields, recipients, emailAlertMessage(from, recipients, alert))
}

func sendEmail(ctx context.Context, fields map[string]any, recipients []string, message []byte) error {
	host := strings.TrimSpace(stringField(fields, "smtp_host"))
	if host == "" {
		return fmt.Errorf("smtp_host is required")
	}
	port := int64Field(fields, "smtp_port")
	if port <= 0 {
		port = 587
	}
	from := strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from"))
	if from == "" {
		return fmt.Errorf("smtp_from is required")
	}
	addr := net.JoinHostPort(host, strconv.FormatInt(port, 10))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	// Close force-drops the connection as a safety net. Delivery is decided by Quit
	// below, whose error is returned; a Close failure after that adds no information.
	// The static type is *smtp.Client, so the io.Closer exemption does not apply.
	defer client.Close() //nolint:errcheck // delivery result comes from Quit

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	username := strings.TrimSpace(firstStringField(fields, "smtp_username", "username"))
	password := firstStringField(fields, "smtp_password", "password")
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func splitNotificationRecipients(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
	recipients := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			recipients = append(recipients, field)
		}
	}
	return recipients
}

func (s *Server) resolvePasswordResetMailChannel() (AdminResource, error) {
	channels := s.store.ListResources("notification-channels")
	for _, channel := range channels {
		if channel.Status != StatusActive || normalizeNotificationChannelType(stringField(channel.Fields, "type")) != "email" {
			continue
		}
		if err := validatePasswordResetMailChannel(channel); err == nil {
			return channel, nil
		}
	}
	return AdminResource{}, NewHTTPError(400, "email_notification_required", "Active email notification channel with SMTP host, port and sender is required")
}

func validatePasswordResetMailChannel(channel AdminResource) error {
	fields := channel.Fields
	if normalizeNotificationChannelType(stringField(fields, "type")) != "email" {
		return fmt.Errorf("email notification channel is required")
	}
	if strings.TrimSpace(stringField(fields, "smtp_host")) == "" {
		return fmt.Errorf("smtp_host is required")
	}
	if int64Field(fields, "smtp_port") <= 0 {
		return fmt.Errorf("smtp_port is required")
	}
	if strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from")) == "" {
		return fmt.Errorf("smtp_from is required")
	}
	return nil
}

func (s *Server) sendAdminPasswordResetEmail(r *http.Request, channel AdminResource, user AdminUser, createdBy string) error {
	if strings.TrimSpace(user.Email) == "" {
		return NewHTTPError(400, "missing_user_email", "User email is required")
	}
	plainToken, token, err := s.store.CreateAdminPasswordResetToken(user.ID, createdBy, 24*time.Hour)
	if err != nil {
		return err
	}
	resetLink := s.adminPasswordResetLink(r, plainToken)
	return sendEmail(r.Context(), channel.Fields, []string{user.Email}, passwordResetEmailMessage(channel.Fields, []string{user.Email}, user, resetLink, token.ExpiresAt))
}

func (s *Server) adminPasswordResetLink(r *http.Request, token string) string {
	returnURL := canonicalOAuthReturnURL(s.config, r)
	origin, ok := normalizedOAuthOrigin(returnURL, false)
	if !ok {
		origin = "http://localhost:3000"
	}
	values := url.Values{}
	values.Set("reset_token", token)
	return oauthRedirectWithFragment(origin+"/", values)
}

func passwordResetEmailMessage(fields map[string]any, recipients []string, user AdminUser, resetLink string, expiresAt time.Time) []byte {
	from := strings.TrimSpace(firstStringField(fields, "smtp_from", "from_email", "from"))
	subject := sanitizeEmailHeader("[TokenHub] 重置控制台登录密码")
	body := fmt.Sprintf("您好 %s，\n\n管理员已为您的 TokenHub 控制台账号发起密码重置。\n账号：%s\n邮箱：%s\n\n请在 24 小时内打开以下链接设置新密码：\n%s\n\n过期时间：%s\n如非本人操作，请联系管理员。\n",
		defaultString(user.Name, user.Username),
		user.Username,
		user.Email,
		resetLink,
		expiresAt.Format(time.RFC3339),
	)
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		sanitizeEmailHeader(from),
		sanitizeEmailHeader(strings.Join(recipients, ", ")),
		subject,
		body,
	)
	return []byte(message)
}

func emailAlertMessage(from string, recipients []string, alert AlertEvent) []byte {
	subject := sanitizeEmailHeader("[TokenHub] " + alert.Code)
	body := fmt.Sprintf("告警事件：%s\n级别：%s\n对象：%s/%s\n说明：%s\n时间：%s\n",
		alert.Code,
		alert.Severity,
		alert.ScopeType,
		alert.ScopeID,
		alert.Message,
		alert.CreatedAt.Format(time.RFC3339),
	)
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		sanitizeEmailHeader(from),
		sanitizeEmailHeader(strings.Join(recipients, ", ")),
		subject,
		body,
	)
	return []byte(message)
}

func sanitizeEmailHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func (s *Server) resolveNotificationChannel(channelID string) (AdminResource, error) {
	channels := s.store.ListResources("notification-channels")
	var fallback *AdminResource
	for i := range channels {
		channel := channels[i]
		if channel.Status != StatusActive {
			continue
		}
		if channelID != "" && channel.ID == channelID {
			return channel, nil
		}
		if fallback == nil {
			copy := channel
			fallback = &copy
		}
	}
	if channelID != "" {
		return AdminResource{}, NewHTTPError(404, "notification_channel_not_found", "Notification channel not found")
	}
	if fallback == nil {
		return AdminResource{}, NewHTTPError(404, "notification_channel_not_found", "No active notification channel")
	}
	return *fallback, nil
}

func (s *Server) applyApprovalRequest(request ApprovalRequest, actor AdminUser) (any, error) {
	if request.Status != "pending" {
		return nil, NewHTTPError(http.StatusConflict, "approval_already_decided", "Approval request has already been decided")
	}
	payload := map[string]any{}
	if request.Payload != "" {
		if err := json.Unmarshal([]byte(request.Payload), &payload); err != nil {
			return nil, NewHTTPError(http.StatusBadRequest, "invalid_approval_payload", "Approval payload is invalid")
		}
	}
	switch {
	case request.Trigger == "api_key_create":
		projectID := stringFromPayload(payload, "project_id")
		ownerUserID := stringFromPayload(payload, "owner_user_id")
		if ownerUserID == "" {
			ownerUserID = request.RequesterID
		}
		owner, ok := s.findAdminUser(ownerUserID)
		syntheticAdminOwner := !ok && ownerUserID == request.RequesterID && actor.ID == request.RequesterID && isPlatformAdminRole(actor.Role)
		if !ok && !syntheticAdminOwner {
			return nil, NewHTTPError(404, "api_key_owner_not_found", "API key owner not found")
		}
		if ok && owner.Status != "" && owner.Status != StatusActive {
			return nil, NewHTTPError(400, "api_key_owner_inactive", "API key owner must be active")
		}
		requesterRole := ""
		if requester, ok := s.findAdminUser(request.RequesterID); ok {
			requesterRole = normalizeAdminRole(requester.Role)
		} else if actor.ID == request.RequesterID {
			requesterRole = normalizeAdminRole(actor.Role)
		}
		key, secret, err := s.store.CreateAPIKey(projectID, APIKey{
			Name:            stringFromPayload(payload, "name"),
			Group:           stringFromPayload(payload, "group"),
			OwnerUserID:     ownerUserID,
			Allowed:         stringSliceFromPayload(payload["allowed_models"]),
			ModelAccessMode: stringFromPayload(payload, "model_access_mode"),
			IPAllowlist:     stringSliceFromPayload(payload["ip_allowlist"]),
			Limits:          quotaLimitsFromPayload(payload["limits"]),
			RateLimitRPM:    optionalInt64Value(payload, "rate_limit_rpm"),
			TokenLimitTPM:   optionalInt64Value(payload, "token_limit_tpm"),
			Status:          StatusActive,
			Metadata: map[string]string{
				"created_by":      request.RequesterID,
				"created_by_role": requesterRole,
			},
		}, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":                      key.ID,
			"api_key":                 secret,
			"name":                    key.Name,
			"project_id":              key.ProjectID,
			"owner_user_id":           key.OwnerUserID,
			"plain_text_visible_once": true,
		}, nil
	case request.ResourceType == "api_key":
		limits, limitsSet := payload["limits"]
		rateLimitRPM, rateLimitSet := optionalInt64Payload(payload, "rate_limit_rpm")
		tokenLimitTPM, tokenLimitSet := optionalInt64Payload(payload, "token_limit_tpm")
		key, err := s.store.UpdateAPIKey(request.ResourceID, APIKey{
			OwnerUserID:     stringFromPayload(payload, "owner_user_id"),
			Allowed:         stringSliceFromPayload(payload["allowed_models"]),
			ModelAccessMode: stringFromPayload(payload, "model_access_mode"),
			IPAllowlist:     stringSliceFromPayload(payload["ip_allowlist"]),
			Limits:          quotaLimitsFromPayload(limits),
			LimitsSet:       limitsSet,
			RateLimitRPM:    rateLimitRPM,
			RateLimitSet:    rateLimitSet,
			TokenLimitTPM:   tokenLimitTPM,
			TokenLimitSet:   tokenLimitSet,
			Status:          stringFromPayload(payload, "status"),
		})
		return key, err
	case request.ResourceType == "budgets" || request.ResourceType == "quota-policies":
		resource := AdminResource{
			Name:        stringFromPayload(payload, "name"),
			Description: stringFromPayload(payload, "description"),
			Status:      stringFromPayload(payload, "status"),
			Fields:      fieldsFromPayload(payload["fields"]),
		}
		if request.ResourceType == "quota-policies" {
			if err := validateQuotaPolicyMinuteLimits(resource.Fields); err != nil {
				return nil, err
			}
		}
		var saved AdminResource
		var err error
		if request.ResourceID == "" {
			saved = s.store.CreateResource(request.ResourceType, resource)
		} else {
			saved, err = s.store.UpdateResource(request.ResourceType, request.ResourceID, resource)
			if err != nil {
				return nil, err
			}
		}
		if request.ResourceType == "quota-policies" {
			if err := s.linkProjectQuotaPolicy(saved, payload); err != nil {
				return nil, err
			}
		}
		return saved, nil
	case request.ResourceType == "invoices" && (request.Trigger == "invoice_confirm" || request.Trigger == "invoice_reject"):
		invoice, err := s.findResource("invoices", request.ResourceID)
		if err != nil {
			return nil, err
		}
		action := strings.TrimPrefix(request.Trigger, "invoice_")
		return s.applyInvoiceDecision(invoice, action, actor, stringFromPayload(payload, "invoice_note"), stringFromPayload(payload, "reject_reason"))
	default:
		return map[string]any{"applied": false, "reason": "no runtime apply handler"}, nil
	}
}

type resourceExportColumn struct {
	Header string
	Field  string
	Source string
}

func (s *Server) writeResourceExport(writer *csv.Writer, user AdminUser, kind string, periodFilter string, columns []resourceExportColumn) {
	headers := make([]string, 0, len(columns))
	for _, column := range columns {
		headers = append(headers, column.Header)
	}
	_ = writer.Write(headers)
	for _, item := range s.filterResourcesForUser(user, kind, s.store.ListResources(kind)) {
		if periodFilter != "" && !resourceMatchesPeriod(item, periodFilter) {
			continue
		}
		row := make([]string, 0, len(columns))
		for _, column := range columns {
			row = append(row, resourceExportValue(item, column))
		}
		_ = writer.Write(row)
	}
}

func normalizeExportPeriod(period string) string {
	period = strings.TrimSpace(period)
	if period == "" {
		return ""
	}
	return normalizeBillingPeriod(period, time.Now().UTC())
}

func resourceMatchesPeriod(item AdminResource, period string) bool {
	if period == "" {
		return true
	}
	for _, key := range []string{"period", "period_ref", "last_calculated_period"} {
		if normalizeExportPeriod(stringField(item.Fields, key)) == period {
			return true
		}
	}
	return false
}

func resourceExportValue(item AdminResource, column resourceExportColumn) string {
	switch column.Source {
	case "id":
		return item.ID
	case "kind":
		return item.Kind
	case "name":
		return item.Name
	case "description":
		return item.Description
	case "status":
		return item.Status
	case "created_at":
		return item.CreatedAt.Format(time.RFC3339)
	case "updated_at":
		return item.UpdatedAt.Format(time.RFC3339)
	}
	return stringifyCSV(item.Fields[column.Field])
}

func (s *Server) findResource(kind string, id string) (AdminResource, error) {
	for _, item := range s.store.ListResources(kind) {
		if item.ID == id {
			return item, nil
		}
	}
	return AdminResource{}, NewHTTPError(404, "resource_not_found", "Resource not found")
}

func (s *Server) findProject(id string) (Project, error) {
	id = strings.TrimSpace(id)
	for _, project := range s.store.ListProjects() {
		if project.ID == id {
			return project, nil
		}
	}
	return Project{}, NewHTTPError(404, "project_not_found", "Project not found")
}

func invoiceDecisionPayload(invoice AdminResource, action string, invoiceNote string, rejectReason string) map[string]any {
	fields := map[string]any{}
	for key, value := range invoice.Fields {
		fields[key] = value
	}
	return map[string]any{
		"kind":             "invoices",
		"resource_id":      invoice.ID,
		"name":             invoice.Name,
		"status":           invoice.Status,
		"fields":           fields,
		"amount_usd":       float64Field(invoice.Fields, "amount_usd"),
		"invoice_note":     invoiceNote,
		"reject_reason":    rejectReason,
		"requested_action": "invoice_" + action,
	}
}

func (s *Server) applyInvoiceDecision(invoice AdminResource, action string, actor AdminUser, invoiceNote string, rejectReason string) (AdminResource, error) {
	if invoice.Kind != "invoices" {
		return AdminResource{}, NewHTTPError(400, "invalid_invoice", "Resource is not an invoice")
	}
	status := strings.ToLower(strings.TrimSpace(invoice.Status))
	if status == "confirmed" || status == "rejected" {
		return AdminResource{}, NewHTTPError(http.StatusConflict, "invoice_already_decided", "Invoice has already been decided")
	}
	fields := map[string]any{}
	for key, value := range invoice.Fields {
		fields[key] = value
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(invoiceNote) != "" {
		fields["invoice_note"] = strings.TrimSpace(invoiceNote)
	}
	switch action {
	case "confirm":
		fields["confirmed_by"] = actor.Name
		fields["confirmed_by_id"] = actor.ID
		fields["confirmed_at"] = now
		fields["reject_reason"] = ""
		invoice.Status = "confirmed"
	case "reject":
		fields["rejected_by"] = actor.Name
		fields["rejected_by_id"] = actor.ID
		fields["rejected_at"] = now
		fields["reject_reason"] = strings.TrimSpace(rejectReason)
		invoice.Status = "rejected"
	default:
		return AdminResource{}, NewHTTPError(400, "invalid_invoice_action", "Invalid invoice action")
	}
	invoice.Fields = fields
	return s.store.UpdateResource("invoices", invoice.ID, invoice)
}

func stringFromPayload(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return strings.TrimSpace(stringifyCSV(value))
}

func stringSliceFromPayload(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		return typed
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(stringifyCSV(item))
			if text != "" {
				items = append(items, text)
			}
		}
		return items
	case string:
		items := strings.Split(typed, ",")
		result := make([]string, 0, len(items))
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
		return result
	default:
		return nil
	}
}

func fieldsFromPayload(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if fields, ok := value.(map[string]any); ok {
		return fields
	}
	return map[string]any{}
}

func quotaLimitsFromPayload(value any) QuotaLimits {
	switch typed := value.(type) {
	case QuotaLimits:
		return typed
	case map[string]any:
		return QuotaLimits{
			RateLimitRPM:    int64Field(typed, "rate_limit_rpm"),
			TokenLimitTPM:   int64Field(typed, "token_limit_tpm"),
			DailyRequests:   int64Field(typed, "daily_requests"),
			MonthlyRequests: int64Field(typed, "monthly_requests"),
			DailyTokens:     int64Field(typed, "daily_tokens"),
			MonthlyTokens:   int64Field(typed, "monthly_tokens"),
			DailyCostUSD:    float64Field(typed, "daily_cost_usd"),
			MonthlyCostUSD:  float64Field(typed, "monthly_cost_usd"),
			MaxConcurrency:  int64Field(typed, "max_concurrency"),
		}
	default:
		return QuotaLimits{}
	}
}

func optionalInt64Value(payload map[string]any, key string) *int64 {
	value, _ := optionalInt64Payload(payload, key)
	return value
}

func optionalInt64Payload(payload map[string]any, key string) (*int64, bool) {
	raw, exists := payload[key]
	if !exists || raw == nil {
		return nil, exists
	}
	value := int64Field(payload, key)
	return &value, true
}
