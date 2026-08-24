package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AffinityKindCodexSession = "codex_session"
	noBindingGeneration      = int64(-1)
	codexSessionAffinityTTL  = time.Hour
)

type RequestAffinity struct {
	AdapterType string
	Kind        string
	KeyHash     string
}

// persistsBinding reports whether this affinity mode reads and writes
// AdapterSessionBinding.
//
// Derived from Kind rather than stored as a struct field: a field would have to
// be set at every construction site, and missing one silently changes
// persistence behaviour. Deriving it means a new mode only needs one line here
// and cannot be forgotten.
//
// Cache locality is purely stateless and must return false, otherwise every chat
// request would pay for the binding table's DELETE+SELECT+UPDATE.
func (a *RequestAffinity) persistsBinding() bool {
	return a != nil && a.Kind == AffinityKindCodexSession
}

func resolveCodexSessionAffinity(secret string, apiKeyID string, headers http.Header, request ResponsesRequest) (*RequestAffinity, error) {
	canonical, ok := codexSessionIdentifier(headers, request)
	if !ok {
		return nil, nil
	}
	if err := validateSessionIdentifier(canonical, "codex_session_id_invalid", "Codex session identifier"); err != nil {
		return nil, err
	}
	return &RequestAffinity{
		AdapterType: ProviderOpenAICodex,
		Kind:        AffinityKindCodexSession,
		KeyHash:     deriveSessionAffinityKey(secret, apiKeyID, canonical),
	}, nil
}

func codexSessionIdentifier(headers http.Header, request ResponsesRequest) (string, bool) {
	for _, value := range []string{
		headers.Get("session-id"),
		headers.Get("session_id"),
		codexClientMetadataSessionID(request),
		codexRawStringField(request, "prompt_cache_key"),
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

func codexClientMetadataSessionID(request ResponsesRequest) string {
	if request.raw == nil {
		return ""
	}
	raw, ok := request.raw["client_metadata"]
	if !ok {
		return ""
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil {
		return ""
	}
	var sessionID string
	if value, ok := metadata["session_id"]; ok {
		_ = json.Unmarshal(value, &sessionID)
	}
	return strings.TrimSpace(sessionID)
}

func codexRawStringField(request ResponsesRequest, key string) string {
	if request.raw == nil {
		return ""
	}
	raw, ok := request.raw[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func (s *GormStore) GetAdapterSessionBinding(ctx context.Context, adapterType string, providerID string, affinityKeyHash string) (AdapterSessionBinding, bool, error) {
	adapterType = strings.TrimSpace(adapterType)
	providerID = strings.TrimSpace(providerID)
	affinityKeyHash = strings.TrimSpace(affinityKeyHash)
	cutoff := time.Now().UTC().Add(-codexSessionAffinityTTL)
	if err := s.db.WithContext(ctx).
		Where("adapter_type = ? AND provider_id = ? AND affinity_key_hash = ? AND last_used_at <= ?", adapterType, providerID, affinityKeyHash, cutoff).
		Delete(&AdapterSessionBinding{}).Error; err != nil {
		return AdapterSessionBinding{}, false, err
	}
	var binding AdapterSessionBinding
	err := s.db.WithContext(ctx).
		Where("adapter_type = ? AND provider_id = ? AND affinity_key_hash = ?", adapterType, providerID, affinityKeyHash).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AdapterSessionBinding{}, false, nil
	}
	if err != nil {
		return AdapterSessionBinding{}, false, err
	}
	return binding, true, nil
}

// CommitAdapterSessionBinding creates, touches, or compare-and-swap rebinds a
// durable adapter session. A generation of -1 means the caller observed no
// binding. Successful use refreshes the one-hour sliding expiration window.
func (s *GormStore) CommitAdapterSessionBinding(ctx context.Context, binding AdapterSessionBinding, expectedGeneration int64) (AdapterSessionBinding, bool, error) {
	binding.AdapterType = strings.TrimSpace(binding.AdapterType)
	binding.AffinityKind = strings.TrimSpace(binding.AffinityKind)
	binding.ProviderID = strings.TrimSpace(binding.ProviderID)
	binding.AffinityKeyHash = strings.TrimSpace(binding.AffinityKeyHash)
	binding.ResourceID = strings.TrimSpace(binding.ResourceID)
	if binding.AdapterType == "" || binding.AffinityKind == "" || binding.ProviderID == "" || binding.AffinityKeyHash == "" || binding.ResourceID == "" {
		return AdapterSessionBinding{}, false, NewHTTPError(500, "adapter_session_binding_invalid", "Adapter session binding is incomplete")
	}

	now := time.Now().UTC()
	var committed AdapterSessionBinding
	changed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var resource ProviderResource
		if err := tx.First(&resource, "id = ? AND provider_id = ?", binding.ResourceID, binding.ProviderID).Error; err != nil {
			return notFound(err, "provider_resource_not_found", "Provider resource not found")
		}

		var current AdapterSessionBinding
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("adapter_type = ? AND provider_id = ? AND affinity_key_hash = ?", binding.AdapterType, binding.ProviderID, binding.AffinityKeyHash).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if expectedGeneration != noBindingGeneration {
				return nil
			}
			binding.ID = firstNonEmpty(binding.ID, NewID("asb"))
			binding.Generation = 1
			binding.CreatedAt = now
			binding.UpdatedAt = now
			binding.LastUsedAt = now
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				committed = binding
				changed = true
				return nil
			}
			return tx.Where("adapter_type = ? AND provider_id = ? AND affinity_key_hash = ?", binding.AdapterType, binding.ProviderID, binding.AffinityKeyHash).
				First(&committed).Error
		}
		if err != nil {
			return err
		}

		if current.ResourceID == binding.ResourceID {
			if err := tx.Model(&AdapterSessionBinding{}).Where("id = ?", current.ID).
				Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error; err != nil {
				return err
			}
			current.LastUsedAt = now
			current.UpdatedAt = now
			committed = current
			return nil
		}
		if current.Generation != expectedGeneration {
			committed = current
			return nil
		}

		result := tx.Model(&AdapterSessionBinding{}).
			Where("id = ? AND generation = ?", current.ID, expectedGeneration).
			Updates(map[string]any{
				"resource_id":   binding.ResourceID,
				"generation":    expectedGeneration + 1,
				"rebind_reason": binding.RebindReason,
				"last_used_at":  now,
				"updated_at":    now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return tx.Where("id = ?", current.ID).First(&committed).Error
		}
		current.ResourceID = binding.ResourceID
		current.Generation = expectedGeneration + 1
		current.RebindReason = binding.RebindReason
		current.LastUsedAt = now
		current.UpdatedAt = now
		committed = current
		changed = true
		return nil
	})
	if err != nil {
		return AdapterSessionBinding{}, false, err
	}
	return committed, changed, nil
}

func applyAdapterSessionAffinity(ctx context.Context, store Store, routed RoutedCall) (RoutedCall, map[string]AdapterSessionBinding, error) {
	if routed.Affinity == nil || routed.Affinity.KeyHash == "" || !routed.Affinity.persistsBinding() {
		return routed, nil, nil
	}
	bindings := map[string]AdapterSessionBinding{}
	seenProviders := map[string]struct{}{}
	for _, route := range routed.Routes {
		if route.Provider.Type != routed.Affinity.AdapterType {
			continue
		}
		if _, seen := seenProviders[route.Provider.ID]; seen {
			continue
		}
		seenProviders[route.Provider.ID] = struct{}{}
		keyHash := providerScopedAffinityKeyHash(routed.Affinity.KeyHash, route.Provider.ID)
		binding, ok, err := store.GetAdapterSessionBinding(ctx, routed.Affinity.AdapterType, route.Provider.ID, keyHash)
		if err != nil {
			return routed, nil, err
		}
		if ok {
			bindings[route.Provider.ID] = binding
		}
	}
	if len(bindings) == 0 {
		return routed, bindings, nil
	}

	ordered := append([]RouteSelection(nil), routed.Routes...)
	for providerID, binding := range bindings {
		boundIndex := -1
		for index, route := range ordered {
			if route.Provider.ID == providerID && routeResourceID(route) == binding.ResourceID {
				boundIndex = index
				break
			}
		}
		if boundIndex < 0 {
			continue
		}
		targetIndex := boundIndex
		for index, route := range ordered {
			if route.Provider.ID == providerID && route.Route.Priority == ordered[boundIndex].Route.Priority {
				targetIndex = index
				break
			}
		}
		if targetIndex < boundIndex {
			bound := ordered[boundIndex]
			copy(ordered[targetIndex+1:boundIndex+1], ordered[targetIndex:boundIndex])
			ordered[targetIndex] = bound
		}
	}
	routed.Routes = ordered
	return routed, bindings, nil
}

func commitAdapterSessionAffinity(ctx context.Context, store Store, routed RoutedCall, bindings map[string]AdapterSessionBinding, route RouteSelection, rebindReason string) error {
	if routed.Affinity == nil || !routed.Affinity.persistsBinding() || route.Provider.Type != routed.Affinity.AdapterType {
		return nil
	}
	resourceID := routeResourceID(route)
	if resourceID == "" {
		return nil
	}
	expectedGeneration := noBindingGeneration
	if current, ok := bindings[route.Provider.ID]; ok {
		expectedGeneration = current.Generation
		if current.ResourceID == resourceID {
			rebindReason = ""
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, _, err := store.CommitAdapterSessionBinding(commitCtx, AdapterSessionBinding{
		AdapterType:     routed.Affinity.AdapterType,
		AffinityKind:    routed.Affinity.Kind,
		ProviderID:      route.Provider.ID,
		AffinityKeyHash: providerScopedAffinityKeyHash(routed.Affinity.KeyHash, route.Provider.ID),
		ResourceID:      resourceID,
		RebindReason:    rebindReason,
	}, expectedGeneration)
	return err
}

func providerScopedAffinityKeyHash(baseHash string, providerID string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(baseHash) + "\x00" + strings.TrimSpace(providerID)))
	return hex.EncodeToString(hash[:])
}
