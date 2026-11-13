package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/quotasource"
)

// Only explicit quota refreshes and the background worker perform network I/O.
// Gateway planning and the allowance read API use persisted observations.
func (s *Server) refreshConfiguredAllowance(ctx context.Context, resource ProviderResource, force bool) (quotasource.Result, bool, error) {
	store, ok := s.store.(*GormStore)
	if !ok {
		return quotasource.Result{}, false, nil
	}
	cfg, err := store.providerAllowanceConfig(ctx, resource.ID)
	if err != nil {
		return quotasource.Result{}, true, err
	}
	if !cfg.Source.Enabled {
		return quotasource.Result{}, false, nil
	}
	var out quotasource.Result
	err = s.store.RunClusterOperation(ctx, "provider-quota:"+resource.ID, func(ctx context.Context) error {
		// Load destination and credentials together after waiting for the refresh
		// lease. Account edits and collection-rule edits use the mutation lease.
		var current ProviderResource
		var provider Provider
		err := store.withClusterLease(ctx, providerResourceMutationLeaseName(resource.ID), func(ctx context.Context) error {
			var found bool
			current, found = s.store.GetProviderResource(resource.ID)
			if !found {
				return NewHTTPError(404, "provider_resource_not_found", "Provider resource not found")
			}
			provider, found = s.store.GetProvider(current.ProviderID)
			if !found {
				return NewHTTPError(404, "provider_not_found", "Provider not found")
			}
			var loadErr error
			cfg, loadErr = store.providerAllowanceConfig(ctx, current.ID)
			return loadErr
		})
		if err != nil {
			return err
		}
		if !cfg.Source.Enabled {
			return nil
		}
		if !force {
			if o, found := s.store.GetProviderResourceObservation(resource.ID); found {
				if json.Unmarshal([]byte(o.QuotaSnapshot), &out) == nil {
					seconds := cfg.Source.RefreshSeconds
					if seconds == 0 {
						seconds = 60
					}
					if !out.Allowance.CheckedAt.IsZero() && time.Since(out.Allowance.CheckedAt) < time.Duration(seconds)*time.Second {
						return nil
					}
				}
			}
		}
		source, err := quotasource.Resolve(cfg.Source, firstNonEmpty(current.BaseURL, provider.BaseURL))
		if err != nil {
			return NewHTTPError(400, "invalid_allowance_source", "Quota source configuration is invalid")
		}
		started := time.Now().UTC()
		token := cfg.Token
		if token == "" {
			token = current.APIKey
			if token == "" {
				token = store.providerResourceCredentialsForRuntime(current).AccessToken
			}
		}
		if token == "" {
			token = provider.APIKey
		}
		body, err := s.fetchConfiguredAllowanceBody(ctx, source, token, resource.ID, cfg.Revision)
		if err == nil {
			if source.Preset == "mistral" {
				who := source
				who.URL = "https://console.mistral.ai/api/vibe/whoami"
				who.Method = "GET"
				var identity []byte
				identity, err = s.fetchAllowanceBody(ctx, who, token)
				if err == nil {
					var v any
					v, err = quotasource.ReadValue(identity, "plan_type")
					kind, _ := v.(string)
					if err == nil {
						out, err = quotasource.ParseMistral(body, kind, time.Now().UTC())
					}
				}
			} else {
				out, err = quotasource.ParsePlan(body, source, time.Now().UTC())
			}
		}
		if err != nil {
			s.store.RecordProviderObservation(ProviderObservation{ProviderID: provider.ID, ResourceID: resource.ID, AdapterType: provider.Type, Source: "quota", Operation: "quota", Success: false, LatencyMS: time.Since(started).Milliseconds(), ErrorCode: "allowance_query_failed"})
			failure := NewHTTPError(502, "allowance_query_failed", "Quota query failed; the last successful reading is retained")
			_ = store.saveProviderQuotaFailureWithRevision(ctx, current, failure.Code, started, &cfg.Revision)
			return failure
		}
		return store.saveCollectedAllowance(ctx, current, cfg.Revision, out)
	})
	return out, true, err
}

func (s *Server) fetchAllowanceBody(ctx context.Context, source quotasource.Config, token string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	endpoint := strings.ReplaceAll(source.URL, "{key}", url.QueryEscape(token))
	method := source.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(source.Body))
	if err != nil {
		return nil, errors.New("invalid quota endpoint")
	}
	req.Header.Set("Accept", "application/json")
	switch source.Auth {
	case "none":
	case "raw":
		req.Header.Set("Authorization", token)
	case "api_key":
		req.Header.Set("x-api-key", token)
	default:
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	for name, value := range source.Headers {
		req.Header.Set(name, strings.ReplaceAll(value, "{key}", token))
	}
	if source.Auth == "volcengine" || source.Auth == "tencent" || source.Auth == "alibaba" {
		req, err = quotasource.CloudRequest(ctx, source, token, time.Now().UTC())
		if err != nil {
			return nil, err
		}
	}
	return s.doAllowanceRequest(req)
}

func (s *Server) doAllowanceRequest(req *http.Request) ([]byte, error) {
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
	defer cancel()
	req = req.Clone(ctx)
	client := ssrfGuardedProviderClient(s.upstreamClient)
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("quota endpoint request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("quota endpoint refused the query")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, quotasource.MaxResponseBytes+1))
	if err != nil || len(body) > quotasource.MaxResponseBytes {
		return nil, errors.New("quota endpoint response exceeds size limit")
	}
	// Many providers put a refusal in an HTTP 200 envelope. Never preserve its
	// arbitrary message or interpret an error envelope as a zero balance.
	var envelope map[string]any
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, errors.New("quota response is not a JSON object")
	}
	if envelope["success"] == false || envelope["status"] == false || envelope["error"] != nil {
		return nil, errors.New("quota endpoint returned an error")
	}
	return body, nil
}

func (s *GormStore) saveCollectedAllowance(ctx context.Context, expected ProviderResource, revision string, result quotasource.Result) error {
	return s.withClusterLease(ctx, providerResourceMutationLeaseName(expected.ID), func(ctx context.Context) error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current ProviderResource
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", expected.ID).Error; err != nil {
				return err
			}
			if !sameProviderAccountSnapshot(current, expected) {
				return NewHTTPError(409, "provider_resource_changed", "Provider resource changed during quota collection")
			}
			actual, err := allowanceSettingRevision(tx, expected.ID)
			if err != nil {
				return err
			}
			if actual != revision {
				return NewHTTPError(409, "allowance_config_changed", "Quota collection rules changed during refresh")
			}
			data, err := json.Marshal(result)
			if err != nil {
				return err
			}
			return saveProviderResourceQuota(tx, expected.ID, "configured", string(data), result.Allowance.FetchedAt)
		})
	})
}
