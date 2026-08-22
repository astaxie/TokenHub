package server

import (
	"net/http"
	"strings"
)

const adminResourceSecretASCIIMask = "********"

var sensitiveAdminResourceFields = map[string]map[string]bool{
	"identity-providers": {
		"client_secret": true,
	},
	"notification-channels": {
		"access_token":          true,
		"bot_token":             true,
		"dingtalk_secret":       true,
		"password":              true,
		"secret":                true,
		"sign_secret":           true,
		"smtp_password":         true,
		"telegram_bot_token":    true,
		"token":                 true,
		"url":                   true,
		"webhook_url":           true,
		"whatsapp_access_token": true,
	},
	"settings": {
		"provider_proxy_password": true,
	},
}

type adminResourceSecretCodec interface {
	protectAdminResourceSecret(string) (string, error)
	revealAdminResourceSecret(string) string
}

func protectAdminResourceSecretsForStorage(store Store, kind string, fields map[string]any) (map[string]any, error) {
	sensitive := sensitiveAdminResourceFields[kind]
	if len(sensitive) == 0 || fields == nil {
		return fields, nil
	}
	codec, codecAvailable := store.(adminResourceSecretCodec)
	needsProtection := false
	for key, value := range fields {
		secret, ok := value.(string)
		if !sensitive[strings.ToLower(strings.TrimSpace(key))] || !ok || strings.TrimSpace(secret) == "" {
			continue
		}
		if !strings.HasPrefix(secret, "enc:v1:") || !codecAvailable || codec.revealAdminResourceSecret(secret) == "" {
			needsProtection = true
			break
		}
	}
	if !needsProtection {
		return fields, nil
	}
	if !codecAvailable {
		return nil, NewHTTPError(http.StatusInternalServerError, "admin_resource_secret_protection_unavailable", "Sensitive resource storage is unavailable")
	}
	protected := cloneAdminResourceFields(fields)
	for key, value := range fields {
		if !sensitive[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		secret, ok := value.(string)
		if !ok || strings.TrimSpace(secret) == "" {
			continue
		}
		if strings.HasPrefix(secret, "enc:v1:") && codec.revealAdminResourceSecret(secret) != "" {
			continue
		}
		protectedSecret, err := codec.protectAdminResourceSecret(secret)
		if err != nil {
			return nil, NewHTTPError(http.StatusInternalServerError, "admin_resource_secret_protection_failed", "Sensitive resource could not be protected")
		}
		protected[key] = protectedSecret
	}
	return protected, nil
}

func redactAdminResourcesForResponse(kind string, resources []AdminResource) []AdminResource {
	if len(sensitiveAdminResourceFields[kind]) == 0 {
		return resources
	}
	redacted := make([]AdminResource, len(resources))
	for index := range resources {
		redacted[index] = redactAdminResourceForResponse(kind, resources[index])
	}
	return redacted
}

func redactAdminResourceForResponse(kind string, resource AdminResource) AdminResource {
	sensitive := sensitiveAdminResourceFields[kind]
	if len(sensitive) == 0 || resource.Fields == nil {
		return resource
	}
	fields := cloneAdminResourceFields(resource.Fields)
	for key, value := range resource.Fields {
		if !sensitive[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		configured := adminResourceSecretConfigured(value)
		fields[key+"_configured"] = configured
		if configured {
			fields[key] = providerHeaderMask
		} else {
			fields[key] = ""
		}
	}
	resource.Fields = fields
	return resource
}

func preserveAdminResourceSecrets(kind string, existing map[string]any, patch map[string]any) map[string]any {
	sensitive := sensitiveAdminResourceFields[kind]
	if len(sensitive) == 0 || patch == nil {
		return patch
	}
	fields := cloneAdminResourceFields(patch)
	explicitlyCleared := map[string]bool{}
	for key, value := range patch {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if !sensitive[normalizedKey] || value != nil {
			continue
		}
		aliases := adminResourceSecretAliases(kind, key, existing, patch)
		if len(aliases) == 0 {
			aliases = []string{normalizedKey}
		}
		for _, alias := range aliases {
			explicitlyCleared[alias] = true
		}
	}
	for key, value := range patch {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if !sensitive[normalizedKey] {
			continue
		}
		if explicitlyCleared[normalizedKey] {
			delete(fields, key)
			continue
		}
		if !isAdminResourceSecretPlaceholder(value) {
			continue
		}
		if stored, ok := existingAdminResourceSecret(kind, key, existing, patch); ok {
			fields[key] = stored
		} else {
			delete(fields, key)
		}
	}
	for key := range fields {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), "_configured") {
			delete(fields, key)
		}
	}
	return fields
}

func existingAdminResourceSecret(kind, patchKey string, existing, patch map[string]any) (any, bool) {
	aliases := adminResourceSecretAliases(kind, patchKey, existing, patch)
	for _, alias := range aliases {
		for storedKey, storedValue := range existing {
			if !strings.EqualFold(strings.TrimSpace(storedKey), alias) || !adminResourceSecretConfigured(storedValue) {
				continue
			}
			return storedValue, true
		}
	}
	return nil, false
}

func adminResourceSecretAliases(kind, patchKey string, existing, patch map[string]any) []string {
	normalizedKey := strings.ToLower(strings.TrimSpace(patchKey))
	aliases := []string{normalizedKey}
	if kind != "notification-channels" {
		return aliases
	}

	existingType := normalizeNotificationChannelType(stringField(existing, "type"))
	patchType := existingType
	if _, supplied := patch["type"]; supplied {
		patchType = normalizeNotificationChannelType(stringField(patch, "type"))
	}
	if len(existing) > 0 && patchType != existingType {
		return nil
	}

	switch normalizedKey {
	case "webhook_url", "url":
		return appendUniqueStrings(aliases, "webhook_url", "url")
	case "smtp_password", "password":
		if patchType == "email" {
			return appendUniqueStrings(aliases, "smtp_password", "password")
		}
	case "telegram_bot_token", "bot_token":
		if patchType == "telegram" {
			return appendUniqueStrings(aliases, "telegram_bot_token", "bot_token", "token", "secret")
		}
	case "access_token", "whatsapp_access_token":
		if patchType == "whatsapp" {
			return appendUniqueStrings(aliases, "access_token", "whatsapp_access_token", "token", "secret")
		}
	case "token", "secret":
		switch patchType {
		case "telegram":
			return appendUniqueStrings(aliases, "telegram_bot_token", "bot_token", "token", "secret")
		case "whatsapp":
			return appendUniqueStrings(aliases, "access_token", "whatsapp_access_token", "token", "secret")
		case "dingtalk":
			return appendUniqueStrings(aliases, "secret", "sign_secret", "dingtalk_secret")
		}
	case "sign_secret", "dingtalk_secret":
		if patchType == "dingtalk" {
			return appendUniqueStrings(aliases, "secret", "sign_secret", "dingtalk_secret")
		}
	}
	return aliases
}

func appendUniqueStrings(values []string, candidates ...string) []string {
	seen := make(map[string]bool, len(values)+len(candidates))
	for _, value := range values {
		seen[value] = true
	}
	for _, candidate := range candidates {
		if !seen[candidate] {
			values = append(values, candidate)
			seen[candidate] = true
		}
	}
	return values
}

func cloneAdminResourceFields(fields map[string]any) map[string]any {
	cloned := make(map[string]any, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

func isAdminResourceSecretPlaceholder(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	text = strings.TrimSpace(text)
	return text == "" || text == adminResourceSecretASCIIMask || text == providerHeaderMask || text == "[redacted]"
}

func adminResourceSecretConfigured(value any) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	return true
}
