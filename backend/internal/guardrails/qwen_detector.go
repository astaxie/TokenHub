package guardrails

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const defaultQwenGuardModel = "Qwen/Qwen3Guard-Gen-0.6B"

var qwenSafetyLine = regexp.MustCompile(`(?im)^\s*(?:safety|label)\s*:\s*(safe|controversial|unsafe)\s*$`)
var qwenCategoryLine = regexp.MustCompile(`(?im)^\s*categories?\s*:\s*(.+?)\s*$`)

type QwenDetectorConfig struct {
	URL     string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type QwenDetector struct {
	config QwenDetectorConfig
	client *http.Client
}

func NewQwenDetector(config QwenDetectorConfig) ModelDetector {
	config.URL = strings.TrimSpace(config.URL)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	if config.URL == "" {
		return nil
	}
	if config.Model == "" {
		config.Model = defaultQwenGuardModel
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	return &QwenDetector{config: config, client: &http.Client{Timeout: config.Timeout}}
}

func (d *QwenDetector) Detect(ctx context.Context, text string) (ModelResult, error) {
	if d == nil || d.client == nil || d.config.URL == "" {
		return ModelResult{}, ErrModelUnavailable
	}
	payload := map[string]any{
		"model": d.config.Model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": text,
		}},
		"temperature": 0,
		"max_tokens":  128,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ModelResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.config.URL, bytes.NewReader(body))
	if err != nil {
		return ModelResult{}, err
	}
	request.Header.Set("content-type", "application/json")
	if d.config.APIKey != "" {
		request.Header.Set("authorization", "Bearer "+d.config.APIKey)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return ModelResult{}, fmt.Errorf("qwen guard request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ModelResult{}, fmt.Errorf("qwen guard response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ModelResult{}, fmt.Errorf("qwen guard returned HTTP %d", response.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil || len(decoded.Choices) == 0 {
		return ModelResult{}, errors.New("qwen guard returned an invalid chat completion")
	}
	return parseQwenGuardResult(decoded.Choices[0].Message.Content)
}

func parseQwenGuardResult(content string) (ModelResult, error) {
	match := qwenSafetyLine.FindStringSubmatch(content)
	if len(match) != 2 {
		label := strings.ToLower(strings.TrimSpace(content))
		if label == "safe" || label == "controversial" || label == "unsafe" {
			return ModelResult{Safety: label}, nil
		}
		return ModelResult{}, errors.New("qwen guard response is missing a safety label")
	}
	result := ModelResult{Safety: strings.ToLower(match[1])}
	if categoryMatch := qwenCategoryLine.FindStringSubmatch(content); len(categoryMatch) == 2 {
		for _, category := range strings.Split(categoryMatch[1], ",") {
			category = strings.TrimSpace(category)
			if category != "" && !strings.EqualFold(category, "none") {
				result.Categories = append(result.Categories, category)
			}
		}
	}
	return result, nil
}
