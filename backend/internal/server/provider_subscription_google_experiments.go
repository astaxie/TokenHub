package server

import (
	"context"
	"crypto/sha256"
	"runtime"
	"strings"
	"sync"
	"time"
)

type googleSubscriptionModelKey struct{}

// Keep caches behind a pointer: OAuth catalog previews copy the adapter.
type googleSubscriptionAccountState struct {
	mu       sync.Mutex
	flags    map[[32]byte]geminiSubscriptionFlags
	projects map[[32]byte]googleSubscriptionCachedProject
}

type googleSubscriptionCachedProject struct {
	value googleSubscriptionProject
	at    time.Time
}

type geminiSubscriptionFlags struct {
	pro31, noPro, latestFlash, latestFlashLite bool
	at                                         time.Time
}

func (a *GoogleSubscriptionAdapter) accountCacheKey(p Provider, creds ProviderResourceCredentials, project string) [32]byte {
	return sha256.Sum256([]byte(strings.Join([]string{a.ProviderType, a.inferenceBase(p), a.LoadURL, firstNonEmpty(creds.RefreshToken, creds.AccessToken), project}, "\x00")))
}

func (a *GoogleSubscriptionAdapter) flags(ctx context.Context, p Provider, creds ProviderResourceCredentials, project string) (geminiSubscriptionFlags, error) {
	key := a.accountCacheKey(p, creds, project)
	if a.accountState != nil {
		a.accountState.mu.Lock()
		cached, ok := a.accountState.flags[key]
		a.accountState.mu.Unlock()
		if ok && time.Since(cached.at) < time.Hour {
			return cached, nil
		}
	}
	platform := map[string]string{"darwin/amd64": "DARWIN_AMD64", "darwin/arm64": "DARWIN_ARM64", "linux/amd64": "LINUX_AMD64", "linux/arm64": "LINUX_ARM64", "windows/amd64": "WINDOWS_AMD64"}[runtime.GOOS+"/"+runtime.GOARCH]
	metadata := map[string]any{"ideName": "IDE_UNSPECIFIED", "pluginType": "GEMINI", "ideVersion": "0.61.0", "platform": firstNonEmpty(platform, "PLATFORM_UNSPECIFIED"), "updateChannel": "stable", "duetProject": project}
	var result struct {
		Flags []struct {
			ID    int64 `json:"flagId"`
			Value bool  `json:"boolValue"`
		} `json:"flags"`
	}
	flags := geminiSubscriptionFlags{at: time.Now()}
	if err := a.call(ctx, a.inferenceBase(p), "listExperiments", creds, map[string]any{"project": project, "metadata": metadata}, &result); err == nil {
		for _, flag := range result.Flags {
			switch flag.ID {
			case 45760185:
				flags.pro31 = flag.Value
			case 45768879:
				flags.noPro = flag.Value
			case 45842815:
				flags.latestFlash = flag.Value
			case 45827489:
				flags.latestFlashLite = flag.Value
			}
		}
	} else if ctx.Err() != nil {
		return flags, ctx.Err()
	} else if guarded := providerEgressFailure(err); guarded != nil {
		return flags, guarded
	}
	// Gemini CLI defaults to disabled experiments if the optional API fails.
	if a.accountState != nil {
		a.accountState.mu.Lock()
		if a.accountState.flags == nil || len(a.accountState.flags) >= 256 {
			a.accountState.flags = make(map[[32]byte]geminiSubscriptionFlags)
		}
		a.accountState.flags[key] = flags
		a.accountState.mu.Unlock()
	}
	return flags, nil
}

func (flags geminiSubscriptionFlags) models(preview bool) []ProviderCatalogModel {
	choices := []struct {
		id        string
		available bool
	}{
		{"gemini-3.1-pro-preview", flags.pro31 && preview && !flags.noPro},
		{"gemini-3-pro-preview", !flags.pro31 && preview && !flags.noPro},
		{"gemini-3-flash-preview", preview},
		{"gemini-3.8-flash", flags.latestFlash},
		{"gemini-3.5-flash", !flags.latestFlash},
		{"gemini-3.5-flash-lite", flags.latestFlashLite},
		{"gemini-3.1-flash-lite", !flags.latestFlashLite},
		{"gemini-2.5-pro", !flags.noPro},
		{"gemini-2.5-flash", !flags.latestFlash},
		{"gemini-2.5-flash-lite", true},
	}
	models := []ProviderCatalogModel{}
	for _, choice := range choices {
		if choice.available {
			model := googleSubscriptionModel(choice.id, choice.id)
			model.InputModalities = append(model.InputModalities, "image")
			model.Metadata = map[string]string{"wire_model": flags.wire(choice.id), "source": "gemini-cli-account-experiments"}
			models = append(models, model)
		}
	}
	return models
}

func geminiSubscriptionMappedModel(model string) bool {
	switch model {
	case "gemini-3.5-flash", "gemini-3-flash", "gemini-3.8-flash", "gemini-3.1-flash-lite", "gemini-3.5-flash-lite":
		return true
	}
	return false
}

func (flags geminiSubscriptionFlags) wire(model string) string {
	switch {
	case flags.latestFlash && (model == "gemini-3.5-flash" || model == "gemini-3-flash"):
		return "gemini-3.8-flash"
	case !flags.latestFlash && (model == "gemini-3.5-flash" || model == "gemini-3.8-flash"):
		return "gemini-3-flash"
	case flags.latestFlashLite && model == "gemini-3.1-flash-lite":
		return "gemini-3.5-flash-lite"
	case !flags.latestFlashLite && model == "gemini-3.5-flash-lite":
		return "gemini-3.1-flash-lite"
	}
	return model
}
