package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Tests use the stored channel, including masked credentials, without changing
// its enabled state or creating an operational alert.
func (s *Server) handleAdminNotificationChannelTestPost(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "alert", http.MethodPost)
	if !ok {
		return
	}
	channelID := r.PathValue("channel_id")
	if channelID == "" || strings.Contains(channelID, "/") {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "notification_channel_not_found", "Notification channel not found"))
		return
	}
	channel, err := s.findResource("notification-channels", channelID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	alert := AlertEvent{
		ID: NewID("notification_test"), ScopeType: "notification_channel", ScopeID: channel.ID,
		Severity: "info", Code: "notification_channel_test",
		Message: "This is a TokenHub test notification. No action is required.", CreatedAt: time.Now().UTC(),
	}
	delivery := s.deliverNotification(r.Context(), alert, channel)
	s.recordAdminAudit(r, user, "test", "notification_channel", channel.ID, "", delivery)
	writeJSON(w, http.StatusOK, delivery)
}

const maxNotificationResponseBytes = 4096

func (s *Server) deliverNotification(ctx context.Context, alert AlertEvent, channel AdminResource) AlertDelivery {
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
		return s.recordAlertDelivery(channel, delivery)
	}
	if delivery.Channel == "email" {
		if err := sendEmailAlert(ctx, channel, alert, s.smtpRootCAs); err != nil {
			delivery.Status = "failed"
			delivery.Error = err.Error()
		}
		return s.recordAlertDelivery(channel, delivery)
	}
	target, err := notificationChannelRequestTarget(channel)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery)
	}
	bodyPayload, headers, err := notificationChannelPayloadForChannel(channel, payload, alert)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery)
	}
	body, _ := json.Marshal(bodyPayload)
	if delivery.Channel == "dingtalk" {
		target, err = signedDingTalkWebhookURL(target, firstStringField(channel.Fields, "secret", "sign_secret", "dingtalk_secret"))
		if err != nil {
			delivery.Status = "failed"
			delivery.Error = err.Error()
			return s.recordAlertDelivery(channel, delivery)
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery)
	}
	req.Header.Set("content-type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		// Send to the configured destination; redirects can turn POST into GET or
		// forward the notification to a different endpoint.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
		return s.recordAlertDelivery(channel, delivery)
	}
	defer resp.Body.Close()
	delivery.StatusCode = resp.StatusCode
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxNotificationResponseBytes+1))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		delivery.Status = "failed"
		delivery.Error = resp.Status
	} else if readErr != nil {
		delivery.Status = "failed"
		delivery.Error = "read notification response: " + readErr.Error()
	} else if len(respBody) > maxNotificationResponseBytes && notificationChannelChecksResponseBody(delivery.Channel) {
		delivery.Status = "failed"
		delivery.Error = "notification response exceeds 4096 bytes"
	} else if err := notificationChannelResponseError(delivery.Channel, respBody); err != nil {
		delivery.Status = "failed"
		delivery.Error = err.Error()
	}
	return s.recordAlertDelivery(channel, delivery)
}
