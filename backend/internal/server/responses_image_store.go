package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	InputBoundary string
	ExpiresAt     int64
}

func (responsesImageStoreBinding) TableName() string {
	return "responses_image_store_bindings"
}

func responsesImageStoreMigration() dbschema.Migration {
	return dbschema.Migration{Version: 11, Name: "add-responses-image-store-bindings", Statements: []string{
		`CREATE TABLE IF NOT EXISTS responses_image_store_bindings (key_hash text PRIMARY KEY, route_id text NOT NULL, provider_id text NOT NULL, provider_model text NOT NULL, resource_id text NOT NULL, upstream_id text NOT NULL, input_boundary text NOT NULL, expires_at bigint NOT NULL)`,
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
			DoUpdates: clause.AssignmentColumns([]string{"route_id", "provider_id", "provider_model", "resource_id", "upstream_id", "input_boundary", "expires_at"}),
		}).Create(&binding).Error
	})
}

// responsesDecodeLimit admits a Responses replay that is larger than the body
// TokenHub is willing to forward on a store-capable route. ChatGPT subscription
// and Codex routes forward up to this same ceiling, because those APIs reject
// store and cannot drop already seen images.
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
	var routes []RouteSelection
	if routed != nil {
		routes = routed.Routes
	}
	return s.rejectOversizedResponsesRequest(*req, routes)
}

func (s *Server) prepareResponsesImageStore(ctx context.Context, routed *RoutedCall, req *ResponsesRequest, headers http.Header) error {
	if req == nil || routed == nil {
		return nil
	}
	// The caller already owns continuation state. conversation and
	// previous_response_id cannot be sent together.
	if responsesRawStringField(*req, "previous_response_id") != "" || responsesRawFieldPresent(*req, "conversation") {
		return nil
	}
	if !routesAllowResponsesImageStore(routed.Routes) {
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
	if found {
		suffix, matched := responsesImageStoreNewItems(req.Input, binding.InputBoundary)
		boundary := ""
		if matched {
			boundary = responsesImageStoreExtendedBoundary(binding.InputBoundary, suffix)
		}
		if matched && boundary != "" && pinResponsesImageStoreRoute(routed, binding) {
			forceResponsesStore(req)
			setResponsesPreviousResponseID(req, binding.UpstreamID)
			assignResponsesInput(req, suffix)
			req.imageStoreBoundary = boundary
			return nil
		}
	}
	// No usable stored response. Keep every image. Ask the upstream to retain
	// this turn only when it actually carries inline images; otherwise an
	// oversized body fails with the configured limit instead of being trimmed.
	if !responsesRequestHasInlineImages(*req) {
		return nil
	}
	forceResponsesStore(req)
	req.imageStoreBoundary = responsesImageStoreBoundary(req.Input)
	return nil
}

func (s *Server) rejectOversizedResponsesRequest(req ResponsesRequest, routes []RouteSelection) error {
	limit := s.responsesForwardLimit(routes)
	if int64(responsesMarshaledBytes(req)) > limit {
		return NewHTTPError(http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds "+strconv.FormatInt(limit, 10)+" bytes")
	}
	return nil
}

// responsesForwardLimit keeps the configured multimodal cap unless every
// candidate is a ChatGPT subscription or Codex route. A mixed failover set
// stays at that cap so a store-capable target never receives an unshrunk body.
func (s *Server) responsesForwardLimit(routes []RouteSelection) int64 {
	if routesForwardFullResponsesReplay(routes) {
		return s.responsesDecodeLimit()
	}
	return s.configuredMultimodalLimit()
}

func routesForwardFullResponsesReplay(routes []RouteSelection) bool {
	if len(routes) == 0 {
		return false
	}
	for _, route := range routes {
		switch strings.TrimSpace(route.Provider.Type) {
		case ProviderOpenAICodex, ProviderChatGPTSubscription:
		default:
			return false
		}
	}
	return true
}

func (s *Server) rememberResponsesImageStore(ctx context.Context, call CallContext, route RouteSelection, headers http.Header, req ResponsesRequest, response any) error {
	if strings.TrimSpace(req.imageStoreBoundary) == "" || req.Store == nil || !*req.Store || !routeAllowsResponsesImageStore(route) {
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
		InputBoundary: req.imageStoreBoundary,
		ExpiresAt:     time.Now().Add(responsesImageStoreTTL).Unix(),
	}
	if err := store.SaveResponsesImageStore(ctx, binding); err != nil {
		return NewHTTPError(http.StatusServiceUnavailable, "responses_image_store_unavailable", "Responses image continuation could not be saved")
	}
	return nil
}

func (s *Server) responsesImageStoreKey(call CallContext, req ResponsesRequest, headers http.Header) (string, bool) {
	session, ok := responsesImageStoreSessionID(headers, req)
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
	// OpenAI and Azure document stored responses. Other APIs, including
	// OpenAI-compatible ones, stay off until responses_store=on.
	switch strings.TrimSpace(route.Provider.Type) {
	case ProviderOpenAI, ProviderAzureOpenAI:
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
	return namedOptionSwitch(options, "responses_store")
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

// responsesImageStoreSessionID accepts an explicit Codex session. prompt_cache_key
// is a cache-routing hint and may be shared by unrelated conversations.
func responsesImageStoreSessionID(headers http.Header, request ResponsesRequest) (string, bool) {
	for _, value := range []string{
		headers.Get("session-id"),
		headers.Get("session_id"),
		compatibilityClientMetadataSessionID(request),
		headers.Get("thread-id"),
		headers.Get("x-client-request-id"),
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			return value, true
		}
	}
	return "", false
}

func responsesRawFieldPresent(request ResponsesRequest, key string) bool {
	if request.raw == nil {
		return false
	}
	raw, ok := request.raw[key]
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// responsesImageStoreNewItems returns the items that were not already accepted
// into the stored response. An echo is skipped only when it is not the next
// stored item. A stored assistant message still has to match, or a later turn
// falls back to the full replay. A mismatch keeps the caller on a full replay.
func responsesImageStoreNewItems(input any, boundary string) ([]any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	var expected []string
	if json.Unmarshal([]byte(strings.TrimSpace(boundary)), &expected) != nil || len(expected) == 0 {
		return nil, false
	}
	index := 0
	for _, hash := range expected {
		matched := false
		for index < len(items) {
			if responsesInputItemHash(items[index]) == hash {
				matched = true
				index++
				break
			}
			if !responsesImageStoreReplayEcho(items[index]) {
				return nil, false
			}
			index++
		}
		if !matched {
			return nil, false
		}
	}
	for index < len(items) && responsesImageStoreReplayEcho(items[index]) {
		index++
	}
	if index >= len(items) {
		return nil, false
	}
	return append([]any{}, items[index:]...), true
}

func responsesImageStoreBoundary(input any) string {
	items, ok := input.([]any)
	if !ok {
		if input == nil {
			return ""
		}
		items = []any{input}
	}
	if len(items) == 0 {
		return ""
	}
	return responsesImageStoreExtendedBoundary(emptyResponsesImageStoreBoundary(), items)
}

func emptyResponsesImageStoreBoundary() string {
	return "[]"
}

func responsesImageStoreExtendedBoundary(existing string, suffix []any) string {
	var hashes []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(existing)), &hashes); err != nil {
		return ""
	}
	for _, item := range suffix {
		hash := responsesInputItemHash(item)
		if hash == "" {
			return ""
		}
		hashes = append(hashes, hash)
	}
	if len(hashes) == 0 {
		return ""
	}
	encoded, err := json.Marshal(hashes)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func responsesInputItemHash(item any) string {
	data, err := json.Marshal(item)
	if err != nil || len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// responsesImageStoreReplayEcho reports items the stored response already
// contains as model output. User messages and tool results are new input.
func responsesImageStoreReplayEcho(item any) bool {
	message, ok := item.(map[string]any)
	if !ok {
		return false
	}
	if role, _ := message["role"].(string); strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return true
	}
	kind, _ := message["type"].(string)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "reasoning", "function_call", "custom_tool_call", "web_search_call", "file_search_call", "computer_call", "image_generation_call", "mcp_call", "local_shell_call", "tool_call":
		return true
	default:
		return false
	}
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
