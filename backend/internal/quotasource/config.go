package quotasource

import (
	"errors"
	"net/url"
	"strings"
)

// Config contains only public collection rules. Authentication material belongs
// in the gateway's encrypted credential store, never in an endpoint or rule.
type Config struct {
	AccessKeyID           string            `json:"access_key_id,omitempty"`
	Region                string            `json:"region,omitempty"`
	PlanID                string            `json:"plan_id,omitempty"`
	Body                  string            `json:"body,omitempty"`
	Enabled               bool              `json:"enabled"`
	Preset                string            `json:"preset,omitempty"`
	URL                   string            `json:"url,omitempty"`
	Method                string            `json:"method,omitempty"`
	Auth                  string            `json:"auth,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	Expression            string            `json:"expression,omitempty"`
	Unit                  string            `json:"unit,omitempty"`
	PlanPath              string            `json:"plan_path,omitempty"`
	ExpiresPath           string            `json:"expires_path,omitempty"`
	Windows               []WindowRule      `json:"windows,omitempty"`
	RefreshSeconds        int               `json:"refresh_seconds,omitempty"`
	SharedAccountRevision string            `json:"shared_account_revision,omitempty"`
	SharedAccountID       string            `json:"shared_account_id,omitempty"`
}

type WindowRule struct {
	Name           string   `json:"name"`
	Used           string   `json:"used,omitempty"`
	Amount         string   `json:"amount,omitempty"`
	Limit          string   `json:"limit,omitempty"`
	Balance        string   `json:"balance,omitempty"`
	Unit           string   `json:"unit,omitempty"`
	Period         string   `json:"period,omitempty"`
	PeriodSeconds  int64    `json:"period_seconds,omitempty"`
	ResetPath      string   `json:"reset_path,omitempty"`
	ExpiresPath    string   `json:"expires_path,omitempty"`
	Models         []string `json:"models,omitempty"`
	ExcludedModels []string `json:"excluded_models,omitempty"`
	Pool           string   `json:"pool,omitempty"`
	Informational  bool     `json:"informational,omitempty"`
	Credits        bool     `json:"credits,omitempty"`
	Optional       bool     `json:"optional,omitempty"`
}

func (c Config) Validate() error {
	if c.Enabled && c.Preset == "" && (c.URL == "" || c.Expression == "" && len(c.Windows) == 0) {
		return errors.New("custom quota collection requires an endpoint and reading rules")
	}
	for _, path := range []string{c.PlanPath, c.ExpiresPath} {
		if path != "" {
			if err := validateInput(nil, path); err != nil {
				return err
			}
		}
	}
	if c.RefreshSeconds != 0 && (c.RefreshSeconds < 30 || c.RefreshSeconds > 86400) {
		return errors.New("quota refresh interval must be between 30 and 86400 seconds")
	}
	if len(c.Body) > 65536 || len(c.URL) > 8192 || len(c.AccessKeyID) > 512 || len(c.Region) > 100 || len(c.PlanID) > 512 {
		return errors.New("quota request configuration exceeds size limit")
	}
	if len(c.Windows) > 64 || len(c.Headers) > 32 || len(c.Expression) > MaxExpressionBytes {
		return errors.New("quota configuration exceeds its size limit")
	}
	if c.Method != "" && c.Method != "GET" && c.Method != "POST" {
		return errors.New("quota method must be GET or POST")
	}
	switch c.Auth {
	case "", "provider", "bearer", "api_key", "raw", "none", "volcengine", "tencent", "alibaba":
	default:
		return errors.New("unknown quota authentication mode")
	}
	if c.URL != "" {
		u, err := url.Parse(strings.ReplaceAll(c.URL, "{key}", "placeholder"))
		if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Fragment != "" {
			return errors.New("quota URL must be absolute HTTP(S), without embedded credentials or a fragment")
		}
	}
	for name, value := range c.Headers {
		if !validHeaderName(name) || strings.ContainsAny(value, "\r\n") || len(value) > 8192 {
			return errors.New("invalid quota header")
		}
	}
	for _, w := range c.Windows {
		if strings.TrimSpace(w.Name) == "" || len(w.Name) > 200 || w.PeriodSeconds < 0 || w.PeriodSeconds > 366*86400 {
			return errors.New("invalid quota window name or period")
		}
		for _, expression := range []string{w.Used, w.Amount, w.Limit, w.Balance} {
			if expression != "" {
				if err := validateInput(nil, expression); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validHeaderName(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, r := range s {
		if r <= 32 || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return true
}
