package server

import (
	"context"
	"net/http"
	"testing"
)

func TestMagpiePlanCatalogPreservesVersionedURLsAndUnknownPrices(t *testing.T) {
	entries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"baidu-qianfan-personal":             "https://qianfan.baidubce.com/v2/tokenplan/personal",
		"baidu-qianfan-team":                 "https://qianfan.baidubce.com/v2/tokenplan/team",
		"huawei-maas-token-plan":             "https://api.modelarts-maas.com/plan/v2",
		"volcengine-coding-plan":             "https://ark.cn-beijing.volces.com/api/coding/v3",
		"volcengine-agent-plan":              "https://ark.cn-beijing.volces.com/api/plan/v3",
		"qwen-token-plan":                    "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
		"kimi-code-global":                   "https://api.kimi.ai/coding/v1",
		"tencent-token-plan-enterprise-intl": "https://tokenhub-intl.tencentmaas.com/plan/v3",
		"clinepass":                          "https://api.cline.bot/api/v1",
		"ollama-cloud":                       "https://ollama.com/v1",
		"commandcode":                        "https://api.commandcode.ai/provider/v1",
		"nous":                               "https://inference-api.nousresearch.com/v1",
	}
	// A public catalog refresh must retain plan endpoints and empty account
	// catalogs instead of dropping them or substituting pay-as-you-go roots.
	merged := mergeCuratedProviderCatalogEntries([]ProviderCatalogEntry{
		{ID: "volcengine-coding-plan", BaseURL: "https://ark.cn-beijing.volces.com/api/v3"},
	}, entries)
	for _, entry := range merged {
		base, ok := want[entry.ID]
		if !ok {
			continue
		}
		if entry.BaseURL != base || entry.Type != "openai_compatible" {
			t.Fatalf("%s: unexpected endpoint or adapter: %s %s", entry.ID, entry.BaseURL, entry.Type)
		}
		request, err := openAICompatibleRequest(context.Background(), Provider{BaseURL: entry.BaseURL, APIKey: "synthetic-plan-key"}, http.MethodPost, "model", "/chat/completions", []byte(`{"messages":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		if request.URL.String() != base+"/chat/completions" || request.Header.Get("Authorization") != "Bearer synthetic-plan-key" {
			t.Fatalf("%s changed its versioned inference URL or authentication", entry.ID)
		}
		for _, model := range entry.Models {
			if model.Metadata["pricing_status"] != "unverified" || model.Metadata["upstream_commit"] != "d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1" {
				t.Fatalf("%s/%s lost unknown-price provenance: %+v", entry.ID, model.ID, model.Metadata)
			}
		}
		delete(want, entry.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing curated plans after refresh: %v", want)
	}
}
