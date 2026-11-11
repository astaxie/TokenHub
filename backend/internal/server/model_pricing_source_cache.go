package server

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
)

const modelPricingValidationCacheLimit = 256

type modelPricingValidationEntry struct {
	ready chan struct{}
	err   error
}

type modelPricingValidationCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]*modelPricingValidationEntry
}

var providerPricingValidationCache modelPricingValidationCache

// Cache only pure configuration validation. Source and provider availability are
// still read on every resolution. Hash the complete record rather than timestamps
// so imports, restores, and direct database repairs cannot reuse stale validation.
func (c *modelPricingValidationCache) validate(source ProviderModel, validate func(ProviderModel) error) error {
	encoded, err := json.Marshal(source)
	if err != nil {
		return err
	}
	key := sha256.Sum256(encoded)
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		c.mu.Unlock()
		<-entry.ready
		return entry.err
	}
	if c.entries == nil {
		c.entries = make(map[[sha256.Size]byte]*modelPricingValidationEntry)
	}
	if len(c.entries) >= modelPricingValidationCacheLimit {
		for candidate, entry := range c.entries {
			select {
			case <-entry.ready:
				delete(c.entries, candidate)
			default:
				continue
			}
			break
		}
	}
	if len(c.entries) >= modelPricingValidationCacheLimit {
		// All slots are validating different policies; keep the cache bounded.
		c.mu.Unlock()
		return validate(source)
	}
	entry := &modelPricingValidationEntry{ready: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()
	entry.err = validate(source)
	close(entry.ready)
	return entry.err
}
