package guardrails

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	CurrentConfigVersion  = 1
	maxDetectionItems     = 32
	maxPatternExpressions = 64
	maxPatternValueBytes  = 512

	StatusActive   = "active"
	StatusDisabled = "disabled"

	DetectorPattern       = "pattern"
	DetectorSensitiveData = "sensitive_data"
	DetectorModel         = "model"

	ActionAudit = "audit"
	ActionMask  = "mask"
	ActionBlock = "block"

	ScopeAllProjects = "all_projects"
	ScopeProject     = "project"

	CheckpointBeforeProvider = "before_provider"
	ProtocolAll              = "all"
)

type Policy struct {
	ID             string          `json:"id" gorm:"primaryKey"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	Status         string          `json:"status" gorm:"index"`
	ConfigVersion  int             `json:"config_version"`
	DetectionItems []DetectionItem `json:"detection_items" gorm:"foreignKey:PolicyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Bindings       []Binding       `json:"bindings" gorm:"foreignKey:PolicyID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func (Policy) TableName() string { return "guardrail_policies" }

type DetectionItem struct {
	ID            string         `json:"id" gorm:"primaryKey"`
	PolicyID      string         `json:"policy_id" gorm:"index;not null"`
	Name          string         `json:"name"`
	DetectorType  string         `json:"detector_type" gorm:"index"`
	Action        string         `json:"action" gorm:"index"`
	ConfigVersion int            `json:"config_version"`
	Config        map[string]any `json:"config" gorm:"serializer:json"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func (DetectionItem) TableName() string { return "guardrail_detection_items" }

type Binding struct {
	ID            string    `json:"id" gorm:"primaryKey"`
	PolicyID      string    `json:"policy_id" gorm:"uniqueIndex:idx_guardrail_policy_scope,priority:1;index;not null"`
	ScopeType     string    `json:"scope_type" gorm:"uniqueIndex:idx_guardrail_policy_scope,priority:2;index"`
	ScopeID       string    `json:"scope_id,omitempty" gorm:"uniqueIndex:idx_guardrail_policy_scope,priority:3;index"`
	Checkpoint    string    `json:"checkpoint" gorm:"uniqueIndex:idx_guardrail_policy_scope,priority:4;index"`
	Protocol      string    `json:"protocol" gorm:"uniqueIndex:idx_guardrail_policy_scope,priority:5;index"`
	ConfigVersion int       `json:"config_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (Binding) TableName() string { return "guardrail_policy_bindings" }

type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

type patternConfig struct {
	Keywords      []string `json:"keywords,omitempty"`
	Regex         []string `json:"regex,omitempty"`
	CaseSensitive bool     `json:"case_sensitive,omitempty"`
}

type sensitiveDataConfig struct {
	DataTypes []string `json:"data_types"`
}

type modelConfig struct {
	BlockOn       string `json:"block_on"`
	OnUnavailable string `json:"on_unavailable"`
}

func NormalizePolicy(policy Policy) (Policy, error) {
	policy.Name = strings.TrimSpace(policy.Name)
	policy.Description = strings.TrimSpace(policy.Description)
	if policy.Name == "" {
		return Policy{}, validationError("guardrail_policy_name_required", "Policy name is required")
	}
	policy.Status = strings.ToLower(strings.TrimSpace(policy.Status))
	if policy.Status == "" {
		policy.Status = StatusActive
	}
	if policy.Status != StatusActive && policy.Status != StatusDisabled {
		return Policy{}, validationError("invalid_guardrail_policy_status", "Policy status must be active or disabled")
	}
	if policy.ConfigVersion == 0 {
		policy.ConfigVersion = CurrentConfigVersion
	}
	if policy.ConfigVersion != CurrentConfigVersion {
		return Policy{}, unsupportedConfigVersion()
	}
	if len(policy.DetectionItems) == 0 {
		return Policy{}, validationError("guardrail_detection_items_required", "At least one detection item is required")
	}
	if len(policy.DetectionItems) > maxDetectionItems {
		return Policy{}, validationError("guardrail_detection_items_limit_exceeded", "A policy cannot contain more than 32 detection items")
	}
	if len(policy.Bindings) == 0 {
		return Policy{}, validationError("guardrail_bindings_required", "At least one policy binding is required")
	}

	itemIDs := map[string]bool{}
	for index := range policy.DetectionItems {
		item, err := normalizeDetectionItem(policy.DetectionItems[index])
		if err != nil {
			return Policy{}, err
		}
		if item.ID != "" && itemIDs[item.ID] {
			return Policy{}, validationError("guardrail_detection_item_conflict", "Detection item IDs must be unique within a policy")
		}
		itemIDs[item.ID] = item.ID != ""
		policy.DetectionItems[index] = item
	}

	bindingKeys := map[string]bool{}
	hasAllProjects := false
	for index := range policy.Bindings {
		binding, err := normalizeBinding(policy.Bindings[index])
		if err != nil {
			return Policy{}, err
		}
		key := binding.ScopeType + "\x00" + binding.ScopeID + "\x00" + binding.Checkpoint + "\x00" + binding.Protocol
		if bindingKeys[key] {
			return Policy{}, validationError("guardrail_binding_conflict", "Policy bindings must be unique")
		}
		bindingKeys[key] = true
		hasAllProjects = hasAllProjects || binding.ScopeType == ScopeAllProjects
		policy.Bindings[index] = binding
	}
	if hasAllProjects && len(policy.Bindings) != 1 {
		return Policy{}, validationError("guardrail_binding_conflict", "An all-projects binding cannot be combined with project bindings")
	}
	return policy, nil
}

func normalizeDetectionItem(item DetectionItem) (DetectionItem, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" {
		return DetectionItem{}, validationError("guardrail_detection_item_name_required", "Detection item name is required")
	}
	item.DetectorType = strings.ToLower(strings.TrimSpace(item.DetectorType))
	item.Action = strings.ToLower(strings.TrimSpace(item.Action))
	if item.ConfigVersion == 0 {
		item.ConfigVersion = CurrentConfigVersion
	}
	if item.ConfigVersion != CurrentConfigVersion {
		return DetectionItem{}, unsupportedConfigVersion()
	}
	if item.Config == nil {
		item.Config = map[string]any{}
	}

	var err error
	switch item.DetectorType {
	case DetectorPattern:
		if item.Action != ActionAudit && item.Action != ActionBlock {
			return DetectionItem{}, invalidAction(item.DetectorType)
		}
		item.Config, err = normalizePatternConfig(item.Config)
	case DetectorSensitiveData:
		if item.Action != ActionAudit && item.Action != ActionMask && item.Action != ActionBlock {
			return DetectionItem{}, invalidAction(item.DetectorType)
		}
		item.Config, err = normalizeSensitiveDataConfig(item.Config)
	case DetectorModel:
		if item.Action != ActionAudit && item.Action != ActionBlock {
			return DetectionItem{}, invalidAction(item.DetectorType)
		}
		item.Config, err = normalizeModelConfig(item.Config)
	default:
		return DetectionItem{}, validationError("unknown_guardrail_detector_type", "Detector type is not supported")
	}
	if err != nil {
		return DetectionItem{}, err
	}
	return item, nil
}

func normalizePatternConfig(config map[string]any) (map[string]any, error) {
	var value patternConfig
	if err := decodeConfig(config, &value); err != nil {
		return nil, err
	}
	value.Keywords = normalizeUniqueStrings(value.Keywords, false)
	value.Regex = normalizeUniqueStrings(value.Regex, false)
	if len(value.Keywords) == 0 && len(value.Regex) == 0 {
		return nil, validationError("invalid_guardrail_detector_config", "Pattern detector requires at least one keyword or regular expression")
	}
	if len(value.Keywords)+len(value.Regex) > maxPatternExpressions {
		return nil, validationError("guardrail_pattern_limit_exceeded", "A pattern detector cannot contain more than 64 keywords and regular expressions")
	}
	for _, pattern := range append(append([]string{}, value.Keywords...), value.Regex...) {
		if len(pattern) > maxPatternValueBytes {
			return nil, validationError("guardrail_pattern_limit_exceeded", "Keywords and regular expressions cannot exceed 512 bytes")
		}
	}
	for _, expression := range value.Regex {
		if _, err := regexp.Compile(expression); err != nil {
			return nil, validationError("invalid_guardrail_detector_config", "Pattern detector contains an invalid regular expression")
		}
	}
	return encodeConfig(value)
}

func normalizeSensitiveDataConfig(config map[string]any) (map[string]any, error) {
	var value sensitiveDataConfig
	if err := decodeConfig(config, &value); err != nil {
		return nil, err
	}
	value.DataTypes = normalizeUniqueStrings(value.DataTypes, true)
	allowed := map[string]bool{
		"credential": true, "email": true, "phone": true, "cn_id_card": true,
		"bank_card": true, "person_name": true, "address": true, "birth_date": true,
	}
	if len(value.DataTypes) == 0 {
		return nil, validationError("invalid_guardrail_detector_config", "Sensitive data detector requires at least one data type")
	}
	for _, dataType := range value.DataTypes {
		if !allowed[dataType] {
			return nil, validationError("invalid_guardrail_detector_config", "Sensitive data type is not supported")
		}
	}
	sort.Strings(value.DataTypes)
	return encodeConfig(value)
}

func normalizeModelConfig(config map[string]any) (map[string]any, error) {
	value := modelConfig{BlockOn: "unsafe", OnUnavailable: "allow_and_audit"}
	if err := decodeConfig(config, &value); err != nil {
		return nil, err
	}
	value.BlockOn = strings.ToLower(strings.TrimSpace(value.BlockOn))
	value.OnUnavailable = strings.ToLower(strings.TrimSpace(value.OnUnavailable))
	if value.BlockOn != "unsafe" && value.BlockOn != "controversial_or_unsafe" {
		return nil, validationError("invalid_guardrail_detector_config", "Model block_on must be unsafe or controversial_or_unsafe")
	}
	if value.OnUnavailable != "allow_and_audit" && value.OnUnavailable != "block" {
		return nil, validationError("invalid_guardrail_detector_config", "Model on_unavailable must be allow_and_audit or block")
	}
	return encodeConfig(value)
}

func normalizeBinding(binding Binding) (Binding, error) {
	binding.ScopeType = strings.ToLower(strings.TrimSpace(binding.ScopeType))
	binding.ScopeID = strings.TrimSpace(binding.ScopeID)
	binding.Checkpoint = strings.ToLower(strings.TrimSpace(binding.Checkpoint))
	if binding.Checkpoint == "" {
		binding.Checkpoint = CheckpointBeforeProvider
	}
	binding.Protocol = strings.ToLower(strings.TrimSpace(binding.Protocol))
	if binding.Protocol == "" {
		binding.Protocol = ProtocolAll
	}
	if binding.ConfigVersion == 0 {
		binding.ConfigVersion = CurrentConfigVersion
	}
	if binding.ConfigVersion != CurrentConfigVersion {
		return Binding{}, unsupportedConfigVersion()
	}
	switch binding.ScopeType {
	case ScopeAllProjects:
		if binding.ScopeID != "" {
			return Binding{}, validationError("invalid_guardrail_binding", "All-projects binding cannot have a scope ID")
		}
	case ScopeProject:
		if binding.ScopeID == "" {
			return Binding{}, validationError("invalid_guardrail_binding", "Project binding requires a scope ID")
		}
	default:
		return Binding{}, validationError("unsupported_guardrail_scope", "Binding scope is not supported")
	}
	if binding.Checkpoint != CheckpointBeforeProvider {
		return Binding{}, validationError("unsupported_guardrail_checkpoint", "Guardrail checkpoint is not supported")
	}
	if binding.Protocol != ProtocolAll {
		return Binding{}, validationError("unsupported_guardrail_protocol", "Guardrail protocol scope is not supported")
	}
	return binding, nil
}

func decodeConfig(config map[string]any, target any) error {
	data, err := json.Marshal(config)
	if err != nil {
		return validationError("invalid_guardrail_detector_config", "Detector config must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return validationError("invalid_guardrail_detector_config", "Detector config contains unsupported or invalid fields")
	}
	return nil
}

func encodeConfig(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, validationError("invalid_guardrail_detector_config", "Detector config could not be normalized")
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, validationError("invalid_guardrail_detector_config", "Detector config could not be normalized")
	}
	return result, nil
}

func normalizeUniqueStrings(values []string, lower bool) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func validationError(code string, message string) error {
	return &ValidationError{Code: code, Message: message}
}

func unsupportedConfigVersion() error {
	return validationError("unsupported_guardrail_config_version", "Guardrail config version is not supported")
}

func invalidAction(detectorType string) error {
	return validationError("invalid_guardrail_action", fmt.Sprintf("Action is not supported for %s detector", detectorType))
}
