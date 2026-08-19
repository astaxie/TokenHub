package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveCodexImageGeneration(t *testing.T) {
	if os.Getenv("TOKENHUB_LIVE_CODEX_IMAGE") != "1" {
		t.Skip("set TOKENHUB_LIVE_CODEX_IMAGE=1 to use a connected Codex subscription")
	}
	config := ConfigFromEnv()
	store, err := OpenStoreWithConfig(config.DatabaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, config)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	var providerCount int64
	var resourceCount int64
	_ = store.db.Model(&Provider{}).Where("status = ? AND healthy = ?", StatusActive, true).Count(&providerCount).Error
	_ = store.db.Model(&ProviderResource{}).Where("status = ? AND healthy = ?", StatusActive, true).Count(&resourceCount).Error
	t.Logf("database_driver=%s active_providers=%d active_resources=%d", store.dbDriver, providerCount, resourceCount)

	targetResourceID := strings.TrimSpace(os.Getenv("TOKENHUB_LIVE_CODEX_RESOURCE_ID"))
	var selected RouteSelection
	routes, err := server.imageRouteCandidates(codexImageModelName)
	if err != nil {
		t.Fatal(err)
	}
	routes = server.filterAndPrioritizeCodexImageRoutes(routes)
	for _, route := range routes {
		if targetResourceID == "" || routeResourceID(route) == targetResourceID {
			selected = route
			break
		}
	}
	if selected.Provider.ID == "" {
		t.Fatalf("no active Codex subscription route is configured for resource %q", targetResourceID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	imageBytes, revisedPrompt, usage, err := server.executeCodexSubscriptionImage(ctx, selected, ImageJob{
		Action:  "generate",
		Prompt:  "A simple red circle centered on a clean white background, flat geometric design, no text, no watermark.",
		Quality: "low",
		Size:    "1024x1024",
	})
	if err != nil {
		t.Fatal(err)
	}
	contentType := http.DetectContentType(imageBytes)
	extension := ".png"
	if contentType == "image/jpeg" {
		extension = ".jpg"
	} else if contentType == "image/webp" {
		extension = ".webp"
	} else if contentType != "image/png" {
		t.Fatalf("unexpected generated image type %q", contentType)
	}
	outputDir := strings.TrimSpace(os.Getenv("TOKENHUB_LIVE_IMAGE_OUTPUT_DIR"))
	if outputDir == "" {
		outputDir = filepath.Join("..", "..", "data", "images")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(outputDir, "live-codex-image-"+time.Now().UTC().Format("20060102T150405Z")+extension)
	if err := os.WriteFile(outputPath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("saved=%s model=%s input_tokens=%d cached_input_tokens=%d output_tokens=%d total_tokens=%d revised_prompt=%q",
		outputPath, selected.ProviderModel, usage.PromptTokens, usage.CachedInputTokens, usage.CompletionTokens, usage.TotalTokens, revisedPrompt)
}
