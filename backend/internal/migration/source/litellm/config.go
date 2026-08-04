package litellm

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	LitellmSettings LiteLLMSettings   `yaml:"litellm_settings"`
	GeneralSettings map[string]any    `yaml:"general_settings"`
	RouterSettings  map[string]any    `yaml:"router_settings"`
	ModelList       []ModelConfig     `yaml:"model_list"`
	Environment     map[string]string `yaml:"environment_variables"`
	KeyManagement   KeyManagement     `yaml:"key_management_settings"`
	Raw             map[string]any    `yaml:",inline"`
}

type LiteLLMSettings struct {
	Version string `yaml:"version"`
}

type KeyManagement struct {
	VirtualKeys []VirtualKeyConfig `yaml:"virtual_keys"`
	Teams       []TeamConfig       `yaml:"teams"`
	Users       []UserConfig       `yaml:"users"`
	Budgets     []map[string]any   `yaml:"budgets"`
}

type VirtualKeyConfig struct {
	KeyAlias      string         `yaml:"key_alias"`
	Token         string         `yaml:"token"`
	Models        []string       `yaml:"models"`
	TeamID        string         `yaml:"team_id"`
	UserID        string         `yaml:"user_id"`
	MaxBudget     float64        `yaml:"max_budget"`
	TPM           int64          `yaml:"tpm_limit"`
	RPM           int64          `yaml:"rpm_limit"`
	SpendDuration string         `yaml:"budget_duration"`
	Expires       string         `yaml:"expires"`
	Metadata      map[string]any `yaml:"metadata"`
}

type TeamConfig struct {
	TeamID    string `yaml:"team_id"`
	TeamAlias string `yaml:"team_alias"`
}

type UserConfig struct {
	UserID    string `yaml:"user_id"`
	UserEmail string `yaml:"user_email"`
	UserAlias string `yaml:"user_alias"`
	TeamID    string `yaml:"team_id"`
}

type ModelConfig struct {
	ModelName     string         `yaml:"model_name"`
	LitellmParams ModelParams    `yaml:"litellm_params"`
	ModelInfo     map[string]any `yaml:"model_info"`
}

type ModelParams struct {
	Model      string         `yaml:"model"`
	APIBase    string         `yaml:"api_base"`
	APIKey     string         `yaml:"api_key"`
	APIVersion string         `yaml:"api_version"`
	RPM        int64          `yaml:"rpm"`
	TPM        int64          `yaml:"tpm"`
	RegionName string         `yaml:"region_name"`
	Weight     int            `yaml:"weight"`
	Extra      map[string]any `yaml:",inline"`
}

func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("litellm: parse yaml: %w", err)
	}
	if len(cfg.ModelList) == 0 {
		return nil, fmt.Errorf("litellm: model_list is required")
	}
	if strings.TrimSpace(cfg.LitellmSettings.Version) == "" {
		return nil, fmt.Errorf("litellm: litellm_settings.version is required")
	}
	return &cfg, nil
}
