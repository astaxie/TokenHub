package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/dbschema"
)

// Response bindings retain opaque upstream IDs without changing the public API.
// Keys include the API key and public model, so one caller cannot reuse another
// caller's continuation to discover or access a route.
type jevResponseBinding struct {
	KeyHash       string `gorm:"primaryKey"`
	RouteID       string
	ProviderID    string
	ProviderModel string
	ResourceID    string
	UpstreamID    string
	ExpiresAt     int64
}

func jevResponseBindingMigration() dbschema.Migration {
	return dbschema.Migration{Version: 6, Name: "add-jev-response-bindings", Statements: []string{
		`CREATE TABLE IF NOT EXISTS jev_response_bindings (key_hash text PRIMARY KEY, route_id text NOT NULL, provider_id text NOT NULL, provider_model text NOT NULL, resource_id text NOT NULL, upstream_id text NOT NULL, expires_at bigint NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_jev_response_bindings_expiry ON jev_response_bindings (expires_at)`,
	}, StatementBudget: 10}
}

type jevResponseBindingStore interface {
	LoadJevResponseBinding(context.Context, string) (jevResponseBinding, bool, error)
	SaveJevResponseBinding(context.Context, jevResponseBinding, []string) error
}

func (s *GormStore) LoadJevResponseBinding(ctx context.Context, key string) (jevResponseBinding, bool, error) {
	var binding jevResponseBinding
	err := s.db.WithContext(ctx).Where("key_hash = ? AND expires_at > ?", key, time.Now().Unix()).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return binding, false, nil
	}
	return binding, err == nil, err
}

func (s *GormStore) SaveJevResponseBinding(ctx context.Context, binding jevResponseBinding, protectedIDs []string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return saveJevResponseBinding(tx, binding, protectedIDs)
	})
}

func saveJevResponseBinding(tx *gorm.DB, binding jevResponseBinding, protectedIDs []string) error {
	// Bound each cleanup batch; expiration never requires an unbounded scan.
	if err := tx.Exec("DELETE FROM jev_response_bindings WHERE key_hash IN (SELECT key_hash FROM jev_response_bindings WHERE expires_at <= ? LIMIT 100)", time.Now().Unix()).Error; err != nil {
		return err
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return err
	}
	var current jevResponseBinding
	if err := tx.First(&current, "key_hash = ?", binding.KeyHash).Error; err != nil {
		return err
	}
	if current.RouteID != binding.RouteID || current.ProviderID != binding.ProviderID || current.ProviderModel != binding.ProviderModel || current.ResourceID != binding.ResourceID || current.UpstreamID != binding.UpstreamID {
		return errors.New("response binding conflict")
	}
	// Keep only a keyed hash after the detailed binding expires. Otherwise a
	// foreign key could replay an old opaque ID through an ordinary alias.
	for _, key := range protectedIDs {
		guard := jevResponseBinding{KeyHash: key, ExpiresAt: int64(1<<63 - 1)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&guard).Error; err != nil {
			return err
		}
	}
	return nil
}

type pendingJevResponseBinding struct {
	binding      jevResponseBinding
	protectedIDs []string
}

func (s *Server) jevResponseKey(call CallContext, id string) string {
	return deriveSessionAffinityKey(s.config.SecretKey, call.Key.ID, "jev-response\x00"+call.Model.Name+"\x00"+id)
}

func (s *Server) pendingJevResponseBinding(call CallContext, route RouteSelection, response any, alias string) (*pendingJevResponseBinding, error) {
	if routeStrategy(route.Route) != RouteStrategyJev && !call.JevResponseBound {
		return nil, nil
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, nil
	}
	upstreamID := envelope.ID
	if upstreamID == "" {
		return nil, nil
	}
	if alias == "" {
		alias = upstreamID
	}
	return &pendingJevResponseBinding{
		binding:      jevResponseBinding{KeyHash: s.jevResponseKey(call, alias), RouteID: route.Route.ID, ProviderID: route.Provider.ID, ProviderModel: route.ProviderModel, ResourceID: routeResourceID(route), UpstreamID: upstreamID, ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix()},
		protectedIDs: []string{s.jevProtectedResponseKey(alias), s.jevProtectedResponseKey(upstreamID)},
	}, nil
}

func (s *Server) bindJevResponse(ctx context.Context, call CallContext, route RouteSelection, response any, alias string) error {
	pending, err := s.pendingJevResponseBinding(call, route, response, alias)
	if err != nil {
		return err
	}
	if pending == nil {
		return nil
	}
	store, ok := s.store.(jevResponseBindingStore)
	if !ok {
		return NewHTTPError(503, "jev_binding_unavailable", "Response route binding is unavailable")
	}
	if err := store.SaveJevResponseBinding(ctx, pending.binding, pending.protectedIDs); err != nil {
		return NewHTTPError(503, "jev_binding_unavailable", "Response route binding could not be saved")
	}
	return nil
}

func (s *Server) applyJevResponsesRouting(ctx context.Context, routed *RoutedCall, req *ResponsesRequest, headers http.Header) error {
	jev := usesJevStrategy(routed.Call, routed.Routes)
	protected := jev || modelRequiresResponseBindings(routed.Call.Model)
	if previous := responsesRawStringField(*req, "previous_response_id"); previous != "" {
		store, ok := s.store.(jevResponseBindingStore)
		if !ok {
			if !protected {
				return nil
			}
			return NewHTTPError(503, "jev_binding_unavailable", "Response route binding is unavailable")
		}
		binding, found, err := store.LoadJevResponseBinding(ctx, s.jevResponseKey(routed.Call, previous))
		if err != nil {
			return NewHTTPError(503, "jev_binding_unavailable", "Response route binding could not be read")
		}
		if !found {
			if !protected {
				_, known, guardErr := store.LoadJevResponseBinding(ctx, s.jevProtectedResponseKey(previous))
				if guardErr != nil {
					return NewHTTPError(503, "jev_binding_unavailable", "Response ownership marker could not be read")
				}
				if !known {
					return nil
				}
			}
			return NewHTTPError(409, "jev_previous_response_unavailable", "Previous response route is unknown or expired; resend the full input without previous_response_id")
		}
		policy := modelSemanticRoutingPolicy(routed.Call.Model)
		permitted := !jev
		for _, candidate := range policy.Candidates {
			permitted = permitted || (candidate.ProviderID == binding.ProviderID && candidate.ProviderModel == binding.ProviderModel)
		}
		if permitted {
			for _, route := range routed.Routes {
				if route.Route.ID == binding.RouteID && route.Provider.ID == binding.ProviderID && route.ProviderModel == binding.ProviderModel && routeResourceID(route) == binding.ResourceID {
					if err := s.validateJevBoundModel(ctx, route, *req); err != nil {
						return err
					}
					routed.Routes = []RouteSelection{route}
					routed.Call.JevResponseBound = true
					setRawJSONField(req.raw, "previous_response_id", binding.UpstreamID, true)
					return nil
				}
			}
		}
		return NewHTTPError(409, "jev_previous_route_unavailable", "Previous response route is no longer available or allowed")
	}
	routed.Call.JevResponseBound = protected
	if !jev {
		return nil
	}
	text, eligible := jevResponsesText(*req)
	_, scope := providerResponsesSessionIdentifier(headers, *req)
	if _, ok := compatibilitySessionIdentifier(headers, *req); ok {
		eligible = false
	}
	return s.applyJevRouting(ctx, routed, text, eligible && scope == sessionScopeNone, func(model ProviderModel) bool {
		return jevResponsesModelFits(model, *req)
	})
}

func (s *Server) validateJevBoundModel(ctx context.Context, route RouteSelection, req ResponsesRequest) error {
	reader, ok := s.store.(semanticModelReader)
	if !ok {
		return NewHTTPError(503, "jev_candidates_unavailable", "Jev candidate metadata is unavailable")
	}
	timeout := s.config.SemanticRoutingTimeoutMS
	if timeout <= 0 {
		timeout = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	models, err := reader.SemanticProviderModels(ctx, []RouteSelection{route})
	if err != nil {
		return NewHTTPError(503, "jev_candidates_unavailable", "Jev candidate metadata is unavailable")
	}
	for _, model := range models {
		if model.ProviderID == route.Provider.ID && model.UpstreamModel == route.ProviderModel && jevResponsesModelFits(model, req) {
			return nil
		}
	}
	return NewHTTPError(409, "jev_previous_route_unavailable", "Previous response model is no longer available or compatible")
}

func jevResponsesModelFits(model ProviderModel, req ResponsesRequest) bool {
	data, _ := json.Marshal(req)
	parameters := map[string]bool{"max_output_tokens": req.MaxTokens != 0, "temperature": req.Temperature != nil, "reasoning": req.Reasoning != nil}
	for _, key := range []string{"tools", "tool_choice", "parallel_tool_calls", "text", "top_p", "truncation"} {
		raw, ok := req.raw[key]
		parameters[key] = ok && string(raw) != "null"
	}
	parameters = jevRequestParameters(req.raw, parameters, []string{"model", "input", "instructions", "stream", "background", "store", "user", "metadata", "prompt_cache_key", "previous_response_id", "client_metadata"})
	return jevModelFits(model, len(data), req.MaxTokens, parameters) && jevInputModalitiesFit(model, req.Input)
}

func jevResponsesText(req ResponsesRequest) (string, bool) {
	if text, ok := req.Input.(string); ok {
		return jevTextContent(text)
	}
	data, err := json.Marshal(req.Input)
	if err != nil {
		return "", false
	}
	var items []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	if json.Unmarshal(data, &items) != nil {
		return "", false
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Role == "user" {
			return jevTextContent(items[i].Content)
		}
	}
	return "", false
}

// Cache entries do not carry verified route/resource provenance for opaque IDs.
// Stateful continuations must pass normal admission and binding validation.
func responsesNeedRouteBinding(call CallContext, payload any) bool {
	req, ok := payload.(ResponsesRequest)
	return ok && (responsesRawStringField(req, "previous_response_id") != "" || modelRequiresResponseBindings(call.Model))
}

func jevResponseID(response any) string {
	data, err := json.Marshal(response)
	if err != nil {
		return ""
	}
	var envelope struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return ""
	}
	return envelope.ID
}

func modelRequiresResponseBindings(model Model) bool {
	policy := modelSemanticRoutingPolicy(model)
	return policy.ResponseBindingRequired || len(policy.Candidates) > 0
}

func (s *Server) jevProtectedResponseKey(id string) string {
	return deriveSessionAffinityKey(s.config.SecretKey, "", "jev-protected-response\x00"+id)
}
