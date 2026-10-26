package server

import (
	"encoding/json"
	"strconv"
	"strings"
)

func subscriptionWorkerDefinitionForResourceType(resourceType string) (subscriptionWorkerDefinition, bool) {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	for _, definition := range subscriptionWorkerDefinitions() {
		if definition.resourceType() == resourceType {
			return definition, true
		}
	}
	return subscriptionWorkerDefinition{}, false
}

// Some upstream adapters use the refresh field for identity metadata instead of
// renewal. Only adapters with a refresh hook may expose the token-renewal UI.
func subscriptionWorkerSessionHasRefreshToken(resourceType, session string) bool {
	definition, ok := subscriptionWorkerDefinitionForResourceType(resourceType)
	if !ok || !subscriptionWorkerHasRefresh(definition.ID) {
		return false
	}
	var auth struct {
		Type    string `json:"type"`
		Refresh string `json:"refresh"`
	}
	return json.Unmarshal([]byte(session), &auth) == nil && auth.Type == "oauth" && strings.TrimSpace(auth.Refresh) != ""
}

func applySubscriptionWorkerRefreshSummary(options map[string]string, resourceType, session string) bool {
	if _, ok := subscriptionWorkerDefinitionForResourceType(resourceType); !ok {
		return false
	}
	options["has_refresh_token"] = strconv.FormatBool(subscriptionWorkerSessionHasRefreshToken(resourceType, session))
	return true
}
