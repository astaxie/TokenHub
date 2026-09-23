package server

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var notificationDeliveryURLPattern = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

func (s *Server) recordAlertDelivery(channel AdminResource, delivery AlertDelivery) AlertDelivery {
	delivery.Target = redactAlertDeliveryTarget(delivery.Channel, delivery.Target)
	delivery.Error = redactNotificationChannelSecrets(delivery.Error, channel)
	return s.store.RecordAlertDelivery(delivery)
}

func redactAlertDeliveriesForResponse(deliveries []AlertDelivery) []AlertDelivery {
	redacted := make([]AlertDelivery, len(deliveries))
	for index, delivery := range deliveries {
		redacted[index] = redactAlertDeliveryForResponse(delivery)
	}
	return redacted
}

func redactAlertDeliveryForResponse(delivery AlertDelivery) AlertDelivery {
	delivery.Target = redactAlertDeliveryTarget(delivery.Channel, delivery.Target)
	delivery.Error = redactNotificationDeliveryURLs(delivery.Error)
	return delivery
}

func redactAlertDeliveryTarget(channelType, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if notificationDeliveryURLPattern.MatchString(target) {
		return notificationDeliveryURLPattern.ReplaceAllStringFunc(target, redactNotificationDeliveryURL)
	}
	switch normalizeNotificationChannelType(channelType) {
	case "email", "telegram", "whatsapp":
		return target
	default:
		return providerHeaderMask
	}
}

func redactNotificationDeliveryURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return providerHeaderMask
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/..."}).String()
}

func redactNotificationDeliveryURLs(message string) string {
	if strings.TrimSpace(message) == "" {
		return message
	}
	return notificationDeliveryURLPattern.ReplaceAllStringFunc(message, redactNotificationDeliveryURL)
}

func redactNotificationChannelSecrets(message string, channel AdminResource) string {
	if strings.TrimSpace(message) == "" {
		return message
	}
	patterns := make([]string, 0, len(channel.Fields))
	for key, value := range channel.Fields {
		if !sensitiveAdminResourceFields["notification-channels"][strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		secret, ok := value.(string)
		secret = strings.TrimSpace(secret)
		if !ok || secret == "" || isAdminResourceSecretPlaceholder(secret) {
			continue
		}
		patterns = append(patterns, providerSecretRepresentations(secret)...)
	}
	sort.Slice(patterns, func(left, right int) bool { return len(patterns[left]) > len(patterns[right]) })
	for _, pattern := range patterns {
		message = strings.ReplaceAll(message, pattern, providerHeaderMask)
	}
	return redactNotificationDeliveryURLs(message)
}

func redactAlertDeliveryAuditSnapshot(snapshot string) string {
	if strings.TrimSpace(snapshot) == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(snapshot), &payload); err != nil {
		return ""
	}
	if target, ok := payload["target"].(string); ok {
		payload["target"] = redactAlertDeliveryTarget(stringField(payload, "channel"), target)
	}
	if message, ok := payload["error"].(string); ok {
		payload["error"] = redactNotificationDeliveryURLs(message)
	}
	return snapshotJSON(payload)
}

func isAlertDeliveryAuditEvent(event AuditEvent) bool {
	return event.Action == "deliver" && event.ResourceType == "alert"
}
