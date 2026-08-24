package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	providerCatalogLocalSource     = "local-provider-catalog"
	providerCatalogUpstreamSource  = "upstream-provider-catalog"
	providerCatalogUpstreamURL     = "https://raw.githubusercontent.com/ThinkInAIXYZ/PublicProviderConf/dev/dist/all.json"
	providerCatalogUpstreamTimeout = 15 * time.Second
	providerCatalogMaxBytes        = 16 << 20
)

type providerCatalogHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// refreshLocked fetches the latest public catalog for an explicit admin
// refresh. Any fetch, response, parse, or completeness failure falls back to
// the catalog shipped with TokenHub.
func (s *providerCatalogService) refreshLocked(ctx context.Context, previous []ProviderCatalogEntry) ([]ProviderCatalogEntry, string, error) {
	entries, upstreamErr := s.loadUpstreamProviderCatalog(ctx)
	if upstreamErr == nil {
		entries, upstreamErr = prepareProviderCatalogRefresh(entries, previous)
	}
	if upstreamErr == nil {
		if err := context.Cause(ctx); err != nil {
			return nil, providerCatalogUpstreamSource, err
		}
		if err := s.store.SaveProviderCatalogSnapshot(entries, providerCatalogUpstreamSource, time.Now().UTC()); err != nil {
			return nil, providerCatalogUpstreamSource, err
		}
		return cloneCatalogEntries(entries, false), providerCatalogUpstreamSource, nil
	}
	if err := context.Cause(ctx); err != nil {
		return nil, providerCatalogUpstreamSource, err
	}

	entries, localErr := loadLocalProviderCatalog(s.catalogFile)
	if localErr == nil {
		entries, localErr = prepareProviderCatalogRefresh(entries, previous)
	}
	if localErr != nil {
		return nil, providerCatalogLocalSource, fmt.Errorf("upstream provider catalog refresh failed (%v); local fallback failed: %w", upstreamErr, localErr)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, providerCatalogLocalSource, err
	}
	if err := s.store.SaveProviderCatalogSnapshot(entries, providerCatalogLocalSource, time.Now().UTC()); err != nil {
		return nil, providerCatalogLocalSource, err
	}
	return cloneCatalogEntries(entries, false), providerCatalogLocalSource, nil
}

func (s *providerCatalogService) loadUpstreamProviderCatalog(ctx context.Context) ([]ProviderCatalogEntry, error) {
	upstreamURL := strings.TrimSpace(s.upstreamURL)
	if upstreamURL == "" {
		return nil, fmt.Errorf("provider catalog upstream URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create provider catalog upstream request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.upstreamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch provider catalog upstream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch provider catalog upstream: unexpected HTTP status %d", resp.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, providerCatalogMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider catalog upstream: %w", err)
	}
	if len(content) > providerCatalogMaxBytes {
		return nil, fmt.Errorf("read provider catalog upstream: response exceeds %d bytes", providerCatalogMaxBytes)
	}
	entries, err := parseProviderCatalog(content, providerCatalogUpstreamSource)
	if err != nil {
		return nil, fmt.Errorf("parse provider catalog upstream: %w", err)
	}
	return entries, nil
}
