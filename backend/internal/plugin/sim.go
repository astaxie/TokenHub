package plugin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ManifestSIM struct {
	ThemeTokens           []ManifestSIMThemeTokens          `yaml:"theme_tokens"`
	ShellLayouts          []ManifestSIMShellLayout          `yaml:"shell_layouts"`
	PageTemplates         []ManifestSIMPageTemplate         `yaml:"page_templates"`
	DashboardCompositions []ManifestSIMDashboardComposition `yaml:"dashboard_compositions"`
}

type ManifestSIMThemeTokens struct {
	ID      string            `json:"id" yaml:"id"`
	Mode    string            `json:"mode,omitempty" yaml:"mode"`
	Default bool              `json:"default,omitempty" yaml:"default"`
	Tokens  map[string]string `json:"tokens,omitempty" yaml:"tokens"`
}

type ManifestSIMShellLayout struct {
	ID           string `json:"id" yaml:"id"`
	Navigation   string `json:"navigation,omitempty" yaml:"navigation"`
	Density      string `json:"density,omitempty" yaml:"density"`
	ContentWidth string `json:"content_width,omitempty" yaml:"content_width"`
	Default      bool   `json:"default,omitempty" yaml:"default"`
}

type ManifestSIMPageTemplate struct {
	ID      string   `json:"id" yaml:"id"`
	Target  string   `json:"target" yaml:"target"`
	Layout  string   `json:"layout,omitempty" yaml:"layout"`
	Regions []string `json:"regions,omitempty" yaml:"regions"`
	Default bool     `json:"default,omitempty" yaml:"default"`
}

type ManifestSIMDashboardComposition struct {
	ID      string                         `json:"id" yaml:"id"`
	Layout  string                         `json:"layout,omitempty" yaml:"layout"`
	Cards   []ManifestSIMDashboardCardSlot `json:"cards,omitempty" yaml:"cards"`
	Default bool                           `json:"default,omitempty" yaml:"default"`
}

type ManifestSIMDashboardCardSlot struct {
	ContributionID string `json:"contribution_id" yaml:"contribution_id"`
	Region         string `json:"region,omitempty" yaml:"region"`
	Size           string `json:"size,omitempty" yaml:"size"`
	Order          int    `json:"order,omitempty" yaml:"order"`
}

func (sim ManifestSIM) Configured() bool {
	return len(sim.ThemeTokens) > 0 || len(sim.ShellLayouts) > 0 || len(sim.PageTemplates) > 0 || len(sim.DashboardCompositions) > 0
}

func (sim ManifestSIM) Validate(kinds []Kind, placements []Placement) error {
	if !sim.Configured() {
		return nil
	}
	if !manifestHasKind(kinds, KindSIM) {
		return fmt.Errorf("SIM capabilities require sim kind")
	}
	if !manifestHasPlacement(placements, PlacementPresentation) {
		return fmt.Errorf("SIM capabilities require presentation placement")
	}
	for index, theme := range sim.ThemeTokens {
		if err := theme.Validate(); err != nil {
			return fmt.Errorf("SIM theme_tokens %d: %w", index, err)
		}
	}
	for index, layout := range sim.ShellLayouts {
		if err := layout.Validate(); err != nil {
			return fmt.Errorf("SIM shell_layouts %d: %w", index, err)
		}
	}
	for index, template := range sim.PageTemplates {
		if err := template.Validate(); err != nil {
			return fmt.Errorf("SIM page_templates %d: %w", index, err)
		}
	}
	for index, composition := range sim.DashboardCompositions {
		if err := composition.Validate(); err != nil {
			return fmt.Errorf("SIM dashboard_compositions %d: %w", index, err)
		}
	}
	return nil
}

func (theme ManifestSIMThemeTokens) Validate() error {
	if strings.TrimSpace(theme.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !safeSIMIdentifier(theme.ID) {
		return fmt.Errorf("id %q is unsupported", theme.ID)
	}
	if err := validateAdminUIThemeMode(theme.Mode); err != nil {
		return err
	}
	if len(theme.Tokens) == 0 {
		return fmt.Errorf("tokens are required")
	}
	values := make(map[string]any, len(theme.Tokens))
	for key, value := range theme.Tokens {
		values[key] = value
	}
	return validateAdminUIThemeTokens(values)
}

func (layout ManifestSIMShellLayout) Validate() error {
	if strings.TrimSpace(layout.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !safeSIMIdentifier(layout.ID) {
		return fmt.Errorf("id %q is unsupported", layout.ID)
	}
	if layout.Navigation != "" && !oneOfSchemaStrings(layout.Navigation, "sidebar") {
		return fmt.Errorf("navigation is unsupported")
	}
	if layout.Density != "" && !oneOfSchemaStrings(layout.Density, "compact", "comfortable", "spacious") {
		return fmt.Errorf("density is unsupported")
	}
	if layout.ContentWidth != "" && !oneOfSchemaStrings(layout.ContentWidth, "fluid", "comfortable") {
		return fmt.Errorf("content_width is unsupported")
	}
	return nil
}

func (template ManifestSIMPageTemplate) Validate() error {
	if strings.TrimSpace(template.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !safeSIMIdentifier(template.ID) {
		return fmt.Errorf("id %q is unsupported", template.ID)
	}
	if strings.TrimSpace(template.Target) == "" {
		return fmt.Errorf("target is required")
	}
	if !safeSIMIdentifier(template.Target) {
		return fmt.Errorf("target %q is unsupported", template.Target)
	}
	if template.Layout != "" && !oneOfSchemaStrings(template.Layout, "single_column", "two_column", "grid", "detail") {
		return fmt.Errorf("layout is unsupported")
	}
	for _, region := range template.Regions {
		if !safeSIMIdentifier(region) {
			return fmt.Errorf("region %q is unsupported", region)
		}
	}
	return nil
}

func (composition ManifestSIMDashboardComposition) Validate() error {
	if strings.TrimSpace(composition.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !safeSIMIdentifier(composition.ID) {
		return fmt.Errorf("id %q is unsupported", composition.ID)
	}
	if composition.Layout != "" && !oneOfSchemaStrings(composition.Layout, "grid", "operations", "compact_grid") {
		return fmt.Errorf("layout is unsupported")
	}
	if len(composition.Cards) == 0 {
		return fmt.Errorf("cards are required")
	}
	for index, card := range composition.Cards {
		if err := card.Validate(); err != nil {
			return fmt.Errorf("card %d: %w", index, err)
		}
	}
	return nil
}

func (card ManifestSIMDashboardCardSlot) Validate() error {
	if strings.TrimSpace(card.ContributionID) == "" {
		return fmt.Errorf("contribution_id is required")
	}
	if !safeSIMIdentifier(card.ContributionID) {
		return fmt.Errorf("contribution_id %q is unsupported", card.ContributionID)
	}
	if card.Region != "" && !safeSIMIdentifier(card.Region) {
		return fmt.Errorf("region %q is unsupported", card.Region)
	}
	if card.Size != "" && !oneOfSchemaStrings(card.Size, "small", "medium", "large", "wide") {
		return fmt.Errorf("size is unsupported")
	}
	if card.Order < 0 {
		return fmt.Errorf("order cannot be negative")
	}
	return nil
}

func (sim ManifestSIM) DescriptorCapabilities() []CapabilityDescriptor {
	if !sim.Configured() {
		return nil
	}
	capabilities := make([]CapabilityDescriptor, 0, len(sim.ThemeTokens)+len(sim.ShellLayouts)+len(sim.PageTemplates)+len(sim.DashboardCompositions))
	for _, theme := range sim.ThemeTokens {
		if value, ok := capabilityJSON(theme.Normalized()); ok {
			capabilities = append(capabilities, CapabilityDescriptor{Kind: "sim", Name: "theme_tokens", Subject: strings.TrimSpace(theme.ID), Value: value})
		}
	}
	for _, layout := range sim.ShellLayouts {
		if value, ok := capabilityJSON(layout.Normalized()); ok {
			capabilities = append(capabilities, CapabilityDescriptor{Kind: "sim", Name: "shell_layout", Subject: strings.TrimSpace(layout.ID), Value: value})
		}
	}
	for _, template := range sim.PageTemplates {
		if value, ok := capabilityJSON(template.Normalized()); ok {
			capabilities = append(capabilities, CapabilityDescriptor{Kind: "sim", Name: "page_template", Subject: strings.TrimSpace(template.ID), Value: value})
		}
	}
	for _, composition := range sim.DashboardCompositions {
		if value, ok := capabilityJSON(composition.Normalized()); ok {
			capabilities = append(capabilities, CapabilityDescriptor{Kind: "sim", Name: "dashboard_composition", Subject: strings.TrimSpace(composition.ID), Value: value})
		}
	}
	return capabilities
}

func (theme ManifestSIMThemeTokens) Normalized() ManifestSIMThemeTokens {
	theme.ID = strings.TrimSpace(theme.ID)
	theme.Mode = strings.TrimSpace(theme.Mode)
	theme.Tokens = normalizeStringMap(theme.Tokens)
	return theme
}

func (layout ManifestSIMShellLayout) Normalized() ManifestSIMShellLayout {
	layout.ID = strings.TrimSpace(layout.ID)
	layout.Navigation = strings.TrimSpace(layout.Navigation)
	layout.Density = strings.TrimSpace(layout.Density)
	layout.ContentWidth = strings.TrimSpace(layout.ContentWidth)
	return layout
}

func (template ManifestSIMPageTemplate) Normalized() ManifestSIMPageTemplate {
	template.ID = strings.TrimSpace(template.ID)
	template.Target = strings.TrimSpace(template.Target)
	template.Layout = strings.TrimSpace(template.Layout)
	template.Regions = normalizeStrings(template.Regions)
	return template
}

func (composition ManifestSIMDashboardComposition) Normalized() ManifestSIMDashboardComposition {
	composition.ID = strings.TrimSpace(composition.ID)
	composition.Layout = strings.TrimSpace(composition.Layout)
	for index := range composition.Cards {
		composition.Cards[index] = composition.Cards[index].Normalized()
	}
	sort.Slice(composition.Cards, func(i, j int) bool {
		if composition.Cards[i].Order != composition.Cards[j].Order {
			return composition.Cards[i].Order < composition.Cards[j].Order
		}
		if composition.Cards[i].Region != composition.Cards[j].Region {
			return composition.Cards[i].Region < composition.Cards[j].Region
		}
		return composition.Cards[i].ContributionID < composition.Cards[j].ContributionID
	})
	return composition
}

func (card ManifestSIMDashboardCardSlot) Normalized() ManifestSIMDashboardCardSlot {
	card.ContributionID = strings.TrimSpace(card.ContributionID)
	card.Region = strings.TrimSpace(card.Region)
	card.Size = strings.TrimSpace(card.Size)
	return card
}

func capabilityJSON(value any) (string, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func safeSIMIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "..") || strings.Contains(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}
