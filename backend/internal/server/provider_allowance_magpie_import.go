package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

// These fields retain Magpie's providers.json/settings.json spellings. Inference
// keys identify quota policies and plans only; they are never imported or returned.
type magpieQuotaProvider struct {
	Preset  string `json:"preset"`
	KeyPlan string `json:"keyPlan"`
	Key     string `json:"key"`
	Keys    []struct {
		Key  string `json:"key"`
		Plan string `json:"plan"`
	} `json:"keys"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
	Chat            string `json:"chat"`
	Responses       string `json:"responses"`
	Anthropic       string `json:"anthropic"`

	ID                   string                        `json:"id"`
	BalanceURL           string                        `json:"balanceURL"`
	BalancePath          string                        `json:"balancePath"`
	BalanceMethod        string                        `json:"balanceMethod"`
	BalanceToken         string                        `json:"balanceToken"`
	NoBalanceProbe       bool                          `json:"noBalanceProbe"`
	Headers              map[string]string             `json:"headers"`
	AccountCaps          map[string]int                `json:"accountCaps"`
	AccountWindowCaps    map[string]map[string]int     `json:"accountWindowCaps"`
	AccountWindowAmounts map[string]map[string]float64 `json:"accountWindowAmounts"`
	KeySpendCaps         map[string]allowance.SpendCap `json:"keySpendCaps"`
	KeyOwnBalances       map[string]magpieOwnBalance   `json:"keyOwnBalances"`
	CapStop              bool                          `json:"capStop"`
	CapCareful           bool                          `json:"capCareful"`
}

type magpieOwnBalance struct {
	USD       float64                   `json:"usd"`
	CNY       *float64                  `json:"cny"`
	At        time.Time                 `json:"at"`
	Refill    *allowance.CalendarRefill `json:"refill"`
	Expirings []magpieExpiring          `json:"expirings"`
	Expiring  *magpieExpiring           `json:"expiring"`
}
type magpieExpiring struct {
	Amount float64   `json:"amount"`
	Until  string    `json:"until"`
	At     time.Time `json:"at"`
}
type magpieQuotaSettings struct {
	CodexAutoReset     []string          `json:"codexAutoReset"`
	CodexAutoResetOnly map[string]string `json:"codexAutoResetOnly"`
	CodexNoCredits     []string          `json:"codexNoCredits"`
	CodexCreditsFirst  []string          `json:"codexCreditsFirst"`
	CodexCreditsLast   []string          `json:"codexCreditsLast"`
	CodexResetLead     int               `json:"codexResetLead"`
	UsageAlert         int               `json:"usageAlert"`
	BalanceAlert       float64           `json:"balanceAlert"`
	ResetReminder      int               `json:"resetReminder"`
}
type magpieQuotaImport struct {
	Providers  json.RawMessage     `json:"providers"`
	Settings   magpieQuotaSettings `json:"settings"`
	ProviderID string              `json:"provider_id"`
	AccountRef string              `json:"account_ref"`
	Timezone   string              `json:"timezone"`
	Revision   string              `json:"revision"`
}

func decodeMagpieQuotaProvider(raw json.RawMessage, id string) (magpieQuotaProvider, error) {
	raw = bytes.TrimSpace(raw)
	var file struct {
		Providers []magpieQuotaProvider `json:"providers"`
	}
	var providers []magpieQuotaProvider
	if len(raw) == 0 {
		return magpieQuotaProvider{}, errors.New("magpie provider JSON is required")
	}
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &providers); err != nil {
			return magpieQuotaProvider{}, errors.New("invalid Magpie provider JSON")
		}
	} else {
		if err := json.Unmarshal(raw, &file); err != nil {
			return magpieQuotaProvider{}, errors.New("invalid Magpie provider JSON or masked credentials")
		}
		providers = file.Providers
		if providers == nil {
			var provider magpieQuotaProvider
			if err := json.Unmarshal(raw, &provider); err != nil {
				return provider, errors.New("invalid Magpie provider JSON or masked credentials")
			}
			providers = []magpieQuotaProvider{provider}
		}
	}
	valid := func(p magpieQuotaProvider) bool {
		return p.ID != "" || p.Preset != "" || p.BalanceURL != "" || p.BalancePath != "" || p.NoBalanceProbe || p.CapStop || p.CapCareful || len(p.AccountCaps)+len(p.AccountWindowCaps)+len(p.AccountWindowAmounts)+len(p.KeySpendCaps)+len(p.KeyOwnBalances) > 0
	}
	if len(providers) == 1 && (id == "" || providers[0].ID == id) && valid(providers[0]) {
		return providers[0], nil
	}
	for _, p := range providers {
		if id != "" && p.ID == id && valid(p) {
			return p, nil
		}
	}
	return magpieQuotaProvider{}, errors.New("select one provider from the Magpie configuration")
}

func magpieImportPolicy(p magpieQuotaProvider, s magpieQuotaSettings, ref, zone string) (allowance.Policy, error) {
	contains := func(list []string) bool {
		for _, v := range list {
			if strings.EqualFold(strings.TrimSpace(v), ref) {
				return true
			}
		}
		return false
	}
	accountCap, _ := magpieAccountValue(p.AccountCaps, ref)
	windowCaps, _ := magpieAccountValue(p.AccountWindowCaps, ref)
	windowAmounts, _ := magpieAccountValue(p.AccountWindowAmounts, ref)
	out := allowance.Policy{Cap: allowance.Cap{Percent: float64(accountCap)}, Windows: map[string]allowance.Cap{}, Credits: "allow", CapStop: p.CapStop, CapCareful: p.CapCareful}
	for name, v := range windowCaps {
		out.Windows[strings.ToLower(strings.TrimSpace(name))] = allowance.Cap{Percent: float64(v)}
	}
	for name, v := range windowAmounts {
		out.Windows[strings.ToLower(strings.TrimSpace(name))] = allowance.Cap{Amount: v}
	}
	if cap, ok := magpieAccountValue(p.KeySpendCaps, ref); ok {
		cap.Timezone = zone
		out.SpendCap = &cap
	}
	if b, ok := magpieAccountValue(p.KeyOwnBalances, ref); ok {
		out.Wallet = allowance.ManualWallet{Enabled: true, Balance: b.USD, Unit: "USD", AsOf: b.At, Refill: b.Refill, Timezone: zone}
		if b.CNY != nil {
			out.Wallet.Balance = *b.CNY
			out.Wallet.Unit = "CNY"
		}
		if out.Wallet.Balance > 1_000_000 || out.Wallet.Balance < -1_000_000 || b.CNY != nil && b.USD != 0 {
			return out, errors.New("magpie manual balance exceeds its limit")
		}
		grants := b.Expirings
		if b.Expiring != nil {
			found := false
			for _, g := range grants {
				if g == *b.Expiring {
					found = true
				}
			}
			if !found {
				grants = append([]magpieExpiring{*b.Expiring}, grants...)
			}
		}
		for i, g := range grants {
			if g.Until == "" || g.Amount <= 0 || g.Amount > 1_000_000 {
				return out, errors.New("invalid Magpie expiring balance")
			}
			out.Wallet.Grants = append(out.Wallet.Grants, allowance.Grant{ID: fmt.Sprintf("magpie-grant-%d", i), Amount: g.Amount, Until: g.Until, At: g.At})
		}
		if len(grants) > 20 {
			return out, errors.New("magpie supports at most 20 expiring grants")
		}
	}
	if strings.Contains(strings.ToLower(p.ID+" "+p.Preset), "codex") {
		switch {
		case contains(s.CodexNoCredits):
			out.Credits = "never"
		case contains(s.CodexCreditsLast):
			out.Credits = "last"
		case contains(s.CodexCreditsFirst):
			out.Credits = "first"
		}
		if contains(s.CodexAutoReset) {
			only, _ := magpieAccountValue(s.CodexAutoResetOnly, ref)
			out.Reset.OnExhausted = only != "expiring"
			out.Reset.BeforeExpiry = only != "week"
		}
		lead := s.CodexResetLead
		if lead < 3 || lead > 1440 {
			lead = 30
		}
		out.Reset.LeadSeconds = int64(lead) * 60
		out.Reset.WeeklyLimit = 1
	}
	if s.BalanceAlert < 0 || s.UsageAlert < 0 || s.UsageAlert > 100 || s.ResetReminder < 0 || s.ResetReminder > 168 {
		return out, errors.New("invalid Magpie alert settings")
	}
	out.Alerts = allowance.AlertPolicy{Enabled: s.UsageAlert > 0 || s.BalanceAlert > 0 || s.ResetReminder > 0, UsedPercent: float64(s.UsageAlert), UnusedBeforeResetSeconds: int64(s.ResetReminder) * 3600}
	if s.BalanceAlert > 0 {
		out.Alerts.LowBalance = &s.BalanceAlert
	}
	return out, out.Validate()
}

func (s *Server) handleProviderAllowanceMagpieImport(w http.ResponseWriter, r *http.Request) {
	store, id, ok := s.allowanceHTTPStore(w, r)
	if !ok {
		return
	}
	var req magpieQuotaImport
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := decodeMagpieQuotaProvider(req.Providers, req.ProviderID)
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_magpie_configuration", err.Error()))
		return
	}
	current, err := store.providerAllowanceConfig(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if req.Revision != current.Revision {
		writeError(w, r, NewHTTPError(409, "allowance_config_changed", "Allowance configuration changed; reload before importing"))
		return
	}
	resource, _ := s.store.GetProviderResource(id)
	provider, _ := s.store.GetProvider(resource.ProviderID)
	key := firstNonEmpty(resource.APIKey, provider.APIKey)
	refs := magpieAccountRefs(p, req.Settings)
	ref := strings.ToLower(strings.TrimSpace(req.AccountRef))
	if ref != "" && !refs[ref] {
		writeError(w, r, NewHTTPError(400, "magpie_account_required", "The selected Magpie account or key ID does not exist"))
		return
	}
	if ref == "" {
		keyID := magpieImportKeyID(key)
		switch {
		case key != "" && refs[keyID]:
			ref = keyID
		case refs[strings.ToLower(strings.TrimSpace(resource.Name))]:
			ref = strings.ToLower(strings.TrimSpace(resource.Name))
		case len(refs) == 1:
			for k := range refs {
				ref = k
			}
		case len(refs) > 1:
			writeError(w, r, NewHTTPError(400, "magpie_account_required", "Select the Magpie account or key ID to migrate"))
			return
		}
	}
	for _, k := range p.Keys {
		if magpieImportKeyID(k.Key) == ref {
			p.KeyPlan = k.Plan
			break
		}
	}
	policy, err := magpieImportPolicy(p, req.Settings, ref, req.Timezone)
	if err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_magpie_configuration", err.Error()))
		return
	}
	policy.Alerts.ChannelIDs = current.Policy.Alerts.ChannelIDs
	source := quotasource.Config{Enabled: true, Auth: "magpie", URL: p.BalanceURL, Expression: p.BalancePath, Method: strings.ToUpper(strings.TrimSpace(p.BalanceMethod)), Headers: p.Headers, NoBalanceProbe: p.NoBalanceProbe, RefreshSeconds: 60}
	if p.BalanceURL == "" && resource.ResourceType != ProviderResourceAPIKey {
		source.Enabled = false
	}
	importedToken := p.BalanceToken
	if cloud, ok := magpieCloudSource(p); ok && p.BalanceURL == "" {
		source = cloud
		importedToken = p.SecretAccessKey
	}

	if err := source.Validate(); err != nil {
		writeError(w, r, NewHTTPError(400, "invalid_magpie_configuration", err.Error()))
		return
	}
	if importedToken == "[redacted]" || source.AccessKeyID == "[redacted]" {
		writeError(w, r, NewHTTPError(400, "invalid_magpie_configuration", "Masked Magpie credentials cannot be imported"))
		return
	}
	for _, v := range source.Headers {
		if v == "[redacted]" {
			writeError(w, r, NewHTTPError(400, "invalid_magpie_configuration", "Masked Magpie headers cannot be imported"))
			return
		}
	}
	// The imported plaintext is the caller's input, never a decrypted saved token.
	writeJSON(w, 200, providerAllowanceConfig{ResourceID: id, Revision: current.Revision, Source: source, Policy: policy, Token: importedToken, ClearToken: importedToken == "", HasToken: current.HasToken})
}

func magpieImportKeyID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:5])
}

func magpieAccountValue[T any](values map[string]T, ref string) (T, bool) {
	for name, value := range values {
		if strings.EqualFold(strings.TrimSpace(name), ref) {
			return value, true
		}
	}
	var zero T
	return zero, false
}

func magpieAccountRefs(p magpieQuotaProvider, s magpieQuotaSettings) map[string]bool {
	refs := map[string]bool{}
	add := func(ref string) {
		if ref = strings.ToLower(strings.TrimSpace(ref)); ref != "" {
			refs[ref] = true
		}
	}
	for ref := range p.AccountCaps {
		add(ref)
	}
	for ref := range p.AccountWindowCaps {
		add(ref)
	}
	for ref := range p.AccountWindowAmounts {
		add(ref)
	}
	for ref := range p.KeySpendCaps {
		add(ref)
	}
	for ref := range p.KeyOwnBalances {
		add(ref)
	}
	if p.Key != "" {
		add(magpieImportKeyID(p.Key))
	}
	for _, k := range p.Keys {
		if k.Key != "" {
			add(magpieImportKeyID(k.Key))
		}
	}
	if strings.Contains(strings.ToLower(p.ID+" "+p.Preset), "codex") {
		for _, list := range [][]string{s.CodexAutoReset, s.CodexNoCredits, s.CodexCreditsFirst, s.CodexCreditsLast} {
			for _, ref := range list {
				add(ref)
			}
		}
	}
	return refs
}
