package server

import pluginmeta "tokenhub/backend/internal/plugin"

func registerBuiltinSubscriptionWorkerUI(registry *pluginmeta.Registry, adminUI *pluginmeta.AdminUIRegistry) {
	for _, definition := range subscriptionWorkerDefinitions() {
		contributions := []pluginmeta.AdminUIContribution{}
		panel := func(id, title, capability string) pluginmeta.AdminUIContribution {
			return pluginmeta.AdminUIContribution{PluginID: definition.pluginID(), ID: id, Slot: pluginmeta.SlotProviderResourcePanel, Title: title, ProviderTypes: []string{definition.providerType()}, ResourceTypes: []string{definition.resourceType()}, Action: definition.providerType() + "." + capability}
		}
		if subscriptionWorkerHasQuota(definition.ID) {
			quota := panel("subscription-quota", "Subscription account quota", "quota.read")
			quota.Schema = map[string]any{"description": "Account allowance, balance, and reset windows reported by the subscription provider."}
			contributions = append(contributions, quota)
		}
		if subscriptionWorkerHasCheckin(definition.ID) {
			contributions = append(contributions, panel("subscription-checkin", "Daily check-in", "checkin.run"))
		}
		if field := subscriptionWorkerOptionUI(definition.ID); field != nil {
			contributions = append(contributions, pluginmeta.AdminUIContribution{PluginID: definition.pluginID(), ID: "subscription-options", Slot: pluginmeta.SlotProviderFormSection, Title: "Subscription account options", ProviderTypes: []string{definition.providerType()}, Schema: map[string]any{"placement": "advanced", "fields": []any{field}}})
		}
		if len(contributions) == 0 {
			continue
		}
		mustRegisterPlugin(registry, builtinAdminUIDescriptor(pluginmeta.Descriptor{ID: definition.pluginID(), Name: definition.Name, Version: pluginmeta.BuiltInVersion, Source: pluginmeta.SourceBuiltIn, Kinds: []pluginmeta.Kind{pluginmeta.KindAdminUI}, Placements: []pluginmeta.Placement{pluginmeta.PlacementPresentation}}, contributions))
		for _, contribution := range contributions {
			mustRegisterAdminUIContribution(adminUI, contribution)
		}
	}
}

func subscriptionWorkerOptionUI(id string) map[string]any {
	key, label, help := "", "", ""
	switch id {
	case "grok":
		key, label, help = "x_search", "Enable X search", "Allow Grok to search X during responses. The provider may charge additional credits."
	case "factory":
		key, label, help = "standard_only", "Use Standard allowance only", "Stop using this account when any Standard allowance window is exhausted. Extra usage will not keep the account in routing."
	case "commandcode-plan":
		key, label, help = "zdr", "Zero Data Retention", "Use only upstreams that retain no prompts or replies. Requests may cost more and fail when no eligible upstream exists."
	default:
		return nil
	}
	return map[string]any{"name": key, "label": label, "help": help, "type": "switch", "default": false}
}
