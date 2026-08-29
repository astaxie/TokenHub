package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	if data, ok, err := m.readOfflineMirror(); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return decodeMarketplaceIndex(data)
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

func (m Marketplace) readOfflineMirror() ([]byte, bool, error) {
	raw := strings.TrimSpace(m.URL)
	if raw == "" {
		return nil, false, nil
	}
	if filepath.IsAbs(raw) {
		data, readErr := os.ReadFile(raw)
		return data, true, readErr
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme == "file" {
		if parsed.Host != "" {
			return nil, true, fmt.Errorf("plugin marketplace file mirror must not include a host")
		}
		data, readErr := os.ReadFile(parsed.Path)
		return data, true, readErr
	}
	if err == nil && parsed.Scheme != "" {
		return nil, false, nil
	}
	data, readErr := os.ReadFile(raw)
	return data, true, readErr
}

func decodeMarketplaceIndex(data []byte) ([]Descriptor, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("plugin marketplace index is empty")
	}
	var list []Descriptor
	if err := json.Unmarshal(data, &list); err == nil {
		return normalizeMarketplaceDescriptors(list), nil
	}
	if looksLikeMarketplaceChannelIndex(data) {
		index, err := DecodeMarketplaceIndex(data)
		if err != nil {
			return nil, err
		}
		return MarketplaceDescriptorsFromChannelIndex(index)
	}
	var index MarketplaceIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	return normalizeMarketplaceDescriptors(index.Plugins), nil
}

func looksLikeMarketplaceChannelIndex(data []byte) bool {
	var probe struct {
		SchemaVersion int    `json:"schema_version"`
		RepositoryID  string `json:"repository_id"`
		Channel       string `json:"channel"`
		Sequence      int64  `json:"sequence"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.SchemaVersion != 0 || strings.TrimSpace(probe.RepositoryID) != "" ||
		strings.TrimSpace(probe.Channel) != "" || probe.Sequence != 0
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
