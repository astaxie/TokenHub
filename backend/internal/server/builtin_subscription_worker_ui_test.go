package server

import (
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestSubscriptionWorkerUIExposesOnlyRegisteredAccountCapabilities(t *testing.T) {
	server := New(NewMemoryStore())
	for _, definition := range subscriptionWorkerDefinitions() {
		quota, checkin, options := false, false, false
		for _, contribution := range server.adminUI.List() {
			if contribution.PluginID != definition.pluginID() {
				continue
			}
			switch contribution.ID {
			case "subscription-quota":
				quota = true
			case "subscription-checkin":
				checkin = true
			case "subscription-options":
				options = true
				if contribution.Slot != pluginmeta.SlotProviderFormSection || contribution.Schema["placement"] != "advanced" {
					t.Fatal("subscription options must remain editable at provider scope")
				}
			}
			if contribution.Action != "" {
				action, found := server.pluginActions.Describe(definition.pluginID(), contribution.Action)
				if !found {
					t.Fatalf("UI action is unregistered: %s", contribution.Action)
				}
				if action.Capability == "checkin.run" {
					properties := action.InputSchema["properties"].(map[string]any)
					if len(properties) != 1 || properties["resource_id"] == nil {
						t.Fatal("check-in action exposes unrelated inputs")
					}
				}
			}
		}
		if quota != subscriptionWorkerHasQuota(definition.ID) || checkin != subscriptionWorkerHasCheckin(definition.ID) || options != (subscriptionWorkerOptionUI(definition.ID) != nil) {
			t.Fatalf("missing subscription UI for %s: quota=%t checkin=%t options=%t", definition.ID, quota, checkin, options)
		}
	}
}
