package plugin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type AdminUISlot string

const (
	SlotNavigationSection     AdminUISlot = "nav.section"
	SlotDashboardCard         AdminUISlot = "dashboard.card"
	SlotProviderFormSection   AdminUISlot = "provider.form.section"
	SlotProviderResourcePanel AdminUISlot = "provider.resource.panel"
	SlotRouteDetailPanel      AdminUISlot = "route.detail.panel"
	SlotSettingsPanel         AdminUISlot = "settings.panel"
	SlotReportTemplate        AdminUISlot = "report.template"
)

type AdminUIManifest struct {
	SchemaVersion int                   `json:"schema_version"`
	Contributions []AdminUIContribution `json:"contributions"`
}

type AdminUIContribution struct {
	PluginID      string         `json:"plugin_id"`
	ID            string         `json:"id"`
	Slot          AdminUISlot    `json:"slot"`
	Title         string         `json:"title,omitempty"`
	ProviderTypes []string       `json:"provider_types,omitempty"`
	Action        string         `json:"action,omitempty"`
	Schema        map[string]any `json:"schema,omitempty"`
}

type AdminUIRegistry struct {
	contributions map[string]AdminUIContribution
}

func NewAdminUIRegistry() *AdminUIRegistry {
	return &AdminUIRegistry{contributions: map[string]AdminUIContribution{}}
}

func ParseAdminUIManifest(pluginID string, data []byte) (AdminUIManifest, error) {
	var manifest AdminUIManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return AdminUIManifest{}, err
	}
	manifest = normalizeAdminUIManifest(pluginID, manifest)
	return manifest, manifest.Validate(pluginID)
}

func (m AdminUIManifest) Validate(pluginID string) error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported admin UI manifest schema_version %d", m.SchemaVersion)
	}
	for _, contribution := range m.Contributions {
		if strings.TrimSpace(contribution.ID) == "" {
			return fmt.Errorf("admin UI contribution id is required")
		}
		if !validAdminUISlot(contribution.Slot) {
			return fmt.Errorf("unsupported admin UI slot %q", contribution.Slot)
		}
		if strings.TrimSpace(contribution.PluginID) != strings.TrimSpace(pluginID) {
			return fmt.Errorf("admin UI contribution %s has plugin_id %q, want %q", contribution.ID, contribution.PluginID, pluginID)
		}
	}
	return nil
}

func (r *AdminUIRegistry) Register(contribution AdminUIContribution) error {
	if r == nil {
		return fmt.Errorf("admin UI registry is not configured")
	}
	contribution = NormalizeAdminUIContribution(contribution)
	if contribution.PluginID == "" {
		return fmt.Errorf("admin UI contribution plugin id is required")
	}
	if contribution.ID == "" {
		return fmt.Errorf("admin UI contribution id is required")
	}
	if !validAdminUISlot(contribution.Slot) {
		return fmt.Errorf("unsupported admin UI slot %q", contribution.Slot)
	}
	r.contributions[adminUIContributionKey(contribution)] = contribution
	return nil
}

func (r *AdminUIRegistry) RegisterManifest(manifest AdminUIManifest) error {
	if r == nil {
		return fmt.Errorf("admin UI registry is not configured")
	}
	for _, contribution := range manifest.Contributions {
		if err := r.Register(contribution); err != nil {
			return err
		}
	}
	return nil
}

func (r *AdminUIRegistry) List() []AdminUIContribution {
	if r == nil {
		return nil
	}
	items := make([]AdminUIContribution, 0, len(r.contributions))
	for _, contribution := range r.contributions {
		items = append(items, contribution)
	}
	sortAdminUIContributions(items)
	return items
}

func NormalizeAdminUIContribution(contribution AdminUIContribution) AdminUIContribution {
	contribution.PluginID = strings.TrimSpace(contribution.PluginID)
	contribution.ID = strings.TrimSpace(contribution.ID)
	contribution.Title = strings.TrimSpace(contribution.Title)
	contribution.Action = strings.TrimSpace(contribution.Action)
	contribution.ProviderTypes = normalizeStrings(contribution.ProviderTypes)
	return contribution
}

func normalizeAdminUIManifest(pluginID string, manifest AdminUIManifest) AdminUIManifest {
	for index := range manifest.Contributions {
		if strings.TrimSpace(manifest.Contributions[index].PluginID) == "" {
			manifest.Contributions[index].PluginID = pluginID
		}
		manifest.Contributions[index] = NormalizeAdminUIContribution(manifest.Contributions[index])
	}
	sortAdminUIContributions(manifest.Contributions)
	return manifest
}

func validAdminUISlot(slot AdminUISlot) bool {
	switch slot {
	case SlotNavigationSection, SlotDashboardCard, SlotProviderFormSection, SlotProviderResourcePanel, SlotRouteDetailPanel, SlotSettingsPanel, SlotReportTemplate:
		return true
	default:
		return false
	}
}

func sortAdminUIContributions(items []AdminUIContribution) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Slot != items[j].Slot {
			return items[i].Slot < items[j].Slot
		}
		if items[i].PluginID != items[j].PluginID {
			return items[i].PluginID < items[j].PluginID
		}
		return items[i].ID < items[j].ID
	})
}

func adminUIContributionKey(contribution AdminUIContribution) string {
	return contribution.PluginID + "\x00" + string(contribution.Slot) + "\x00" + contribution.ID
}

func normalizeStrings(items []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}
	sort.Strings(normalized)
	return normalized
}
