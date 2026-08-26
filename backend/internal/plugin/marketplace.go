package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Marketplace struct {
	URL    string
	Client *http.Client
}

type MarketplaceIndex struct {
	Plugins []Descriptor `json:"plugins"`
}

func NewMarketplace(url string, client *http.Client) Marketplace {
	return Marketplace{URL: strings.TrimSpace(url), Client: client}
}

func (m Marketplace) List(ctx context.Context) ([]Descriptor, error) {
	if strings.TrimSpace(m.URL) == "" {
		return nil, nil
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plugin marketplace request failed: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return decodeMarketplaceIndex(data)
}

func decodeMarketplaceIndex(data []byte) ([]Descriptor, error) {
	var list []Descriptor
	if err := json.Unmarshal(data, &list); err == nil {
		return normalizeMarketplaceDescriptors(list), nil
	}
	var index MarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return normalizeMarketplaceDescriptors(index.Plugins), nil
}

func normalizeMarketplaceDescriptors(items []Descriptor) []Descriptor {
	normalized := make([]Descriptor, 0, len(items))
	for _, item := range items {
		item = NormalizeDescriptor(item)
		if item.Source == "" {
			item.Source = SourceMarketplace
		}
		normalized = append(normalized, item)
	}
	return normalized
}
