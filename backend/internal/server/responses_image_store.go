package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/dbschema"
)

const (
	// responsesImageReplayBytes is how far /v1/responses will read so a Codex
	// replay can be shrunk before the configured forward limit is applied.
	responsesImageReplayBytes = 128 << 20
	// responsesImageStoreTTL matches the upstream Responses retention window.
	responsesImageStoreTTL = 30 * 24 * time.Hour
	// responsesInlineImageMinChars ignores tiny data URLs used by fixtures.
	responsesInlineImageMinChars = 1024
	// responsesOmittedImageURL replaces a historical inline image. The URL is
	// not fetched; it only keeps the item shape valid after the bytes are gone.
	responsesOmittedImageURL = "https://tokenhub.invalid/omitted-prior-image"
)

// responsesImageStoreBinding pins one Codex session to the upstream response
// that still holds its images. The key mixes the API key and public model, so
// one caller cannot continue another caller's stored response.
type responsesImageStoreBinding struct {
	KeyHash       string `gorm:"primaryKey"`
	RouteID       string
	ProviderID    string
	ProviderModel string
	ResourceID    string
	UpstreamID    string
	ExpiresAt     int64
}

func (responsesImageStoreBinding) TableName() string {
	return "responses_image_store_bindings"
}

func responsesImageStoreMigration() dbschema.Migration {
	return dbschema.Migration{Version: 11, Name: "add-responses-image-store-bindings", Statements: []string{
		`CREATE TABLE IF NOT EXISTS responses_image_store_bindings (key_hash text PRIMARY KEY, route_id text NOT NULL, provider_id text NOT NULL, provider_model text NOT NULL, resource_id text NOT NULL, upstream_id text NOT NULL, expires_at bigint NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_responses_image_store_bindings_expiry ON responses_image_store_bindings (expires_at)`,
	}, StatementBudget: 10}
}

type responsesImageStore interface {
	LoadResponsesImageStore(context.Context, string) (responsesImageStoreBinding, bool, error)
	SaveResponsesImageStore(context.Context, responsesImageStoreBinding) error
}

func (s *GormStore) LoadResponsesImageStore(ctx context.Context, key string) (responsesImageStoreBinding, bool, error) {
	var binding responsesImageStoreBinding
	err := s.db.WithContext(ctx).Where("key_hash = ? AND expires_at > ?", key, time.Now().Unix()).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return binding, false, nil
	}
	return binding, err == nil, err
}

func (s *GormStore) SaveResponsesImageStore(ctx context.Context, binding responsesImageStoreBinding) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bound each cleanup batch; expiration never requires an unbounded scan.
		if err := tx.Exec("DELETE FROM responses_image_store_bindings WHERE key_hash IN (SELECT key_hash FROM responses_image_store_bindings WHERE expires_at <= ? LIMIT 100)", time.Now().Unix()).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key_hash"}},
			DoUpdates: clause.AssignmentColumns([]string{"route_id", "provider_id", "provider_model", "resource_id", "upstream_id", "expires_at"}),
		}).Create(&binding).Error
	})
}

// responsesDecodeLimit admits a Responses replay that is larger than the body
// TokenHub is willing to forward. The configured multimodal limit still applies
// after historical image bytes are removed.
func (s *Server) responsesDecodeLimit() int64 {
	limit := max(s.configuredMultimodalLimit(), int64(responsesImageReplayBytes))
	if limit > maxConfigurableRequestBytes {
		return maxConfigurableRequestBytes
	}
	return limit
}

func (s *Server) configuredMultimodalLimit() int64 {
	if s.config.MaxMultimodalRequestBytes > 0 {
		return s.config.MaxMultimodalRequestBytes
	}
	return defaultMaxMultimodalRequestBytes
}

func (s *Server) applyResponsesImageStore(ctx context.Context, routed *RoutedCall, req *ResponsesRequest, headers http.Header) error {
	if err := s.prepareResponsesImageStore(ctx, routed, req, headers); err != nil {
		return err
	}
	return s.rejectOversizedResponsesRequest(*req)
}

func (s *Server) prepareResponsesImageStore(ctx context.Context, routed *RoutedCall, req *ResponsesRequest, headers http.Header) error {
	if req == nil || routed == nil {
		return nil
	}
	// A caller-supplied continuation already names an upstream response.
	if responsesRawStringField(*req, "previous_response_id") != "" {
		return nil
	}
	if !responsesRequestHasInlineImages(*req) || !routesAllowResponsesImageStore(routed.Routes) {
		return nil
	}
	key, ok := s.responsesImageStoreKey(routed.Call, *req, headers)
	if !ok {
		return nil
	}
	store, ok := s.store.(responsesImageStore)
	if !ok {
		return nil
	}
	binding, found, err := store.LoadResponsesImageStore(ctx, key)
	if err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be read")
	}
	if found && pinResponsesImageStoreRoute(routed, binding) {
		forceResponsesStore(req)
		setResponsesPreviousResponseID(req, binding.UpstreamID)
		// Drop historical images even when the follow-up is still under the
		// limit, so the forwarded transcript stops growing every turn.
		assignResponsesInput(req, compactResponsesHistoricalImages(req.Input))
		return nil
	}
	// No usable stored response. Forward the images and ask the upstream to
	// retain this turn. Compact only when that full body cannot be forwarded.
	forceResponsesStore(req)
	if int64(responsesMarshaledBytes(*req)) > s.configuredMultimodalLimit() {
		assignResponsesInput(req, compactResponsesHistoricalImages(req.Input))
	}
	return nil
}

func (s *Server) rejectOversizedResponsesRequest(req ResponsesRequest) error {
	limit := s.configuredMultimodalLimit()
	if int64(responsesMarshaledBytes(req)) > limit {
		return NewHTTPError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds "+strconv.FormatInt(limit, 10)+" bytes")
	}
	return nil
}

func (s *Server) rememberResponsesImageStore(ctx context.Context, call CallContext, route RouteSelection, headers http.Header, req ResponsesRequest, response any) error {
	if req.Store == nil || !*req.Store || !routeAllowsResponsesImageStore(route) {
		return nil
	}
	upstreamID := strings.TrimSpace(jevResponseID(response))
	if upstreamID == "" {
		return nil
	}
	key, ok := s.responsesImageStoreKey(call, req, headers)
	if !ok {
		return nil
	}
	store, ok := s.store.(responsesImageStore)
	if !ok {
		return nil
	}
	binding := responsesImageStoreBinding{
		KeyHash:       key,
		RouteID:       route.Route.ID,
		ProviderID:    route.Provider.ID,
		ProviderModel: route.ProviderModel,
		ResourceID:    routeResourceID(route),
		UpstreamID:    upstreamID,
		ExpiresAt:     time.Now().Add(responsesImageStoreTTL).Unix(),
	}
	if err := store.SaveResponsesImageStore(ctx, binding); err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be saved")
	}
	return nil
}

func (s *Server) responsesImageStoreKey(call CallContext, req ResponsesRequest, headers http.Header) (string, bool) {
	session, ok := compatibilitySessionIdentifier(headers, req)
	if !ok || validateSessionIdentifier(session, "session_id_invalid", "Session identifier") != nil {
		return "", false
	}
	model := strings.TrimSpace(call.Model.Name)
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	if model == "" {
		return "", false
	}
	return deriveSessionAffinityKey(s.config.SecretKey, call.Key.ID, "responses-image-store\x00"+model+"\x00"+session), true
}

// routesAllowResponsesImageStore requires every candidate to accept stored
// continuations. One store:false account in the failover set would otherwise
// receive a transcript whose images had already been removed.
func routesAllowResponsesImageStore(routes []RouteSelection) bool {
	if len(routes) == 0 {
		return false
	}
	for _, route := range routes {
		if !routeAllowsResponsesImageStore(route) {
			return false
		}
	}
	return true
}

func routeAllowsResponsesImageStore(route RouteSelection) bool {
	// Subscription adapters delete previous_response_id and force store false.
	// Stripping images on those routes would drop the only copy the model has.
	switch strings.TrimSpace(route.Provider.Type) {
	case ProviderOpenAICodex, ProviderChatGPTSubscription:
		return false
	}
	if value, ok := responsesStoreSwitch(route); ok {
		return value == "on"
	}
	switch strings.TrimSpace(route.Provider.Type) {
	case ProviderOpenAI, ProviderAzureOpenAI, ProviderOpenAICompatible:
		return true
	default:
		return false
	}
}

// responsesStoreSwitch reads responses_store. A resource value wins over the
// provider value. Only "on" and "off" are meaningful.
func responsesStoreSwitch(route RouteSelection) (string, bool) {
	if route.Resource != nil {
		if value, ok := responsesStoreSwitchValue(route.Resource.Options); ok {
			return value, true
		}
	}
	return responsesStoreSwitchValue(route.Provider.Options)
}

func responsesStoreSwitchValue(options map[string]string) (string, bool) {
	if options == nil {
		return "", false
	}
	raw, ok := options["responses_store"]
	if !ok {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "off":
		return strings.ToLower(strings.TrimSpace(raw)), true
	default:
		return "", false
	}
}

func pinResponsesImageStoreRoute(routed *RoutedCall, binding responsesImageStoreBinding) bool {
	for _, route := range routed.Routes {
		if route.Route.ID == binding.RouteID && route.Provider.ID == binding.ProviderID && route.ProviderModel == binding.ProviderModel && routeResourceID(route) == binding.ResourceID && binding.UpstreamID != "" {
			routed.Routes = []RouteSelection{route}
			return true
		}
	}
	return false
}

func forceResponsesStore(req *ResponsesRequest) {
	if req == nil {
		return
	}
	enabled := true
	req.Store = &enabled
	if req.raw == nil {
		req.raw = map[string]json.RawMessage{}
	}
	setRawJSONField(req.raw, "store", true, true)
}

func setResponsesPreviousResponseID(req *ResponsesRequest, id string) {
	if req == nil || strings.TrimSpace(id) == "" {
		return
	}
	if req.raw == nil {
		req.raw = map[string]json.RawMessage{}
	}
	setRawJSONField(req.raw, "previous_response_id", id, true)
}

func assignResponsesInput(req *ResponsesRequest, input any) {
	if req == nil {
		return
	}
	req.Input = input
	if req.raw == nil {
		req.raw = map[string]json.RawMessage{}
	}
	setRawJSONField(req.raw, "input", input, input != nil)
}

func responsesMarshaledBytes(req ResponsesRequest) int {
	data, err := json.Marshal(req)
	if err != nil {
		return 0
	}
	return len(data)
}

func responsesRequestHasInlineImages(req ResponsesRequest) bool {
	return responsesValueHasInlineImage(req.Input)
}

func responsesValueHasInlineImage(value any) bool {
	switch typed := value.(type) {
	case string:
		return inlineImageDataURL(typed)
	case map[string]any:
		for _, child := range typed {
			if responsesValueHasInlineImage(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if responsesValueHasInlineImage(child) {
				return true
			}
		}
	}
	return false
}

func inlineImageDataURL(value string) bool {
	if len(value) <= responsesInlineImageMinChars {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:image/")
}

// compactResponsesHistoricalImages removes inline image bytes from items before
// the latest user message. Text stays, because an OpenAI-compatible server may
// accept previous_response_id without actually retaining the prior turn.
// With no user message, only the last item is kept intact.
func compactResponsesHistoricalImages(input any) any {
	items, ok := input.([]any)
	if !ok || len(items) < 2 {
		return input
	}
	keepFrom := len(items) - 1
	for index := len(items) - 1; index >= 0; index-- {
		if responsesItemRole(items[index]) == "user" {
			keepFrom = index
			break
		}
	}
	for index := 0; index < keepFrom; index++ {
		items[index], _ = stripInlineImageValue(items[index], 0)
	}
	return items
}

func responsesItemRole(item any) string {
	message, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	role, _ := message["role"].(string)
	return strings.ToLower(strings.TrimSpace(role))
}

func stripInlineImageValue(value any, depth int) (any, bool) {
	if depth > 8 {
		return value, false
	}
	switch typed := value.(type) {
	case string:
		if inlineImageDataURL(typed) {
			return responsesOmittedImageURL, true
		}
		return stripInlineImageJSONString(typed, depth)
	case map[string]any:
		changed := false
		for key, child := range typed {
			next, childChanged := stripInlineImageValue(child, depth+1)
			if childChanged {
				typed[key] = next
				changed = true
			}
		}
		return typed, changed
	case []any:
		changed := false
		for index, child := range typed {
			next, childChanged := stripInlineImageValue(child, depth+1)
			if childChanged {
				typed[index] = next
				changed = true
			}
		}
		return typed, changed
	default:
		return value, false
	}
}

func stripInlineImageJSONString(value string, depth int) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= responsesInlineImageMinChars || (trimmed[0] != '{' && trimmed[0] != '[') {
		return value, false
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return value, false
	}
	stripped, changed := stripInlineImageValue(decoded, depth+1)
	if !changed {
		return value, false
	}
	encoded, err := json.Marshal(stripped)
	if err != nil {
		return value, false
	}
	return string(encoded), true
}
