package server

import (
	"context"
	"net/http"
	"time"

	"tokenhub/backend/internal/quotasource"
)

func (s *Server) queryConfiguredAllowance(ctx context.Context, cfg providerAllowanceConfig, resource ProviderResource, provider Provider, preview bool) (quotasource.Result, error) {
	source, err := quotasource.Resolve(cfg.Source, firstNonEmpty(resource.BaseURL, provider.BaseURL))
	if err != nil {
		return quotasource.Result{}, NewHTTPError(400, "invalid_allowance_source", "Quota source configuration is invalid")
	}
	store := s.store.(*GormStore)
	key := resource.APIKey
	if key == "" {
		key = store.providerResourceCredentialsForRuntime(resource).AccessToken
	}
	key = firstNonEmpty(key, provider.APIKey)
	token := firstNonEmpty(cfg.Token, key)
	if source.Auth == "magpie" {
		source, err = quotasource.PrepareMagpie(source, key, cfg.Token)
		if err != nil {
			return quotasource.Result{}, NewHTTPError(400, "invalid_allowance_source", err.Error())
		}
		token = key
	}
	var body []byte
	if preview {
		if source.Auth == "alibaba" {
			return quotasource.Result{}, NewHTTPError(400, "allowance_preview_unsupported", "Save Alibaba settings before refreshing quota")
		}
		body, err = s.fetchAllowanceBody(ctx, source, token)
	} else {
		body, err = s.fetchConfiguredAllowanceBody(ctx, source, token, resource.ID, cfg.Revision)
	}
	if err != nil {
		return quotasource.Result{}, err
	}
	if source.Preset == "mistral" {
		who := source
		who.URL, who.Method = "https://console.mistral.ai/api/vibe/whoami", "GET"
		identity, err := s.fetchAllowanceBody(ctx, who, token)
		if err != nil {
			return quotasource.Result{}, err
		}
		value, err := quotasource.ReadValue(identity, "plan_type")
		if err != nil {
			return quotasource.Result{}, err
		}
		kind, _ := value.(string)
		return quotasource.ParseMistral(body, kind, time.Now().UTC())
	}
	return quotasource.ParsePlan(body, source, time.Now().UTC())
}

// Preview uses the current form and saved encrypted credentials, without changing
// configuration, observations, history, routing state, or entitlements.
func (s *Server) handleProviderAllowancePreviewPost(w http.ResponseWriter, r *http.Request) {
	store, id, ok := s.allowanceHTTPStore(w, r)
	if !ok {
		return
	}
	var req providerAllowanceConfig
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	var current providerAllowanceConfig
	var resource ProviderResource
	var provider Provider
	err := store.withClusterLease(r.Context(), providerResourceMutationLeaseName(id), func(ctx context.Context) error {
		var found bool
		resource, found = s.store.GetProviderResource(id)
		if !found {
			return NewHTTPError(404, "provider_resource_not_found", "Provider resource not found")
		}
		provider, found = s.store.GetProvider(resource.ProviderID)
		if !found {
			return NewHTTPError(404, "provider_not_found", "Provider not found")
		}
		var err error
		current, err = store.providerAllowanceConfig(ctx, id)
		return err
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if req.Revision != current.Revision {
		writeError(w, r, NewHTTPError(409, "allowance_config_changed", "Allowance configuration changed; reload before querying"))
		return
	}
	if req.ClearToken {
		req.Token = ""
	} else if req.Token == "" {
		req.Token = current.Token
	}
	for name, value := range req.Source.Headers {
		if value == "[redacted]" {
			value, found := current.Source.Headers[name]
			if !found {
				writeError(w, r, NewHTTPError(400, "invalid_allowance_source", "Re-enter the value after renaming a saved header"))
				return
			}
			req.Source.Headers[name] = value
		}
	}
	// Checking a form does not require enabling scheduled collection.
	req.Source.Enabled = true
	result, err := s.queryConfiguredAllowance(r.Context(), req, resource, provider, true)
	if err != nil {
		if AsHTTPError(err).Status == 400 {
			writeError(w, r, err)
		} else {
			writeError(w, r, NewHTTPError(502, "allowance_query_failed", "Balance query failed; check the URL, field and credentials"))
		}
		return
	}
	writeJSON(w, 200, result)
}
