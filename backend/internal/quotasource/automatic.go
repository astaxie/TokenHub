package quotasource

import (
	"net/url"
	"strings"
)

// AutomaticSource follows balanceSourceOf/planQuotaSourceOf in Magpie 47b51c8.
// Fixed vendor endpoints are selected before looking for relay endpoints.
func AutomaticSource(base, accountToken string) (Config, bool) {
	u, err := url.Parse(base)
	if err != nil {
		return Config{}, false
	}
	host := strings.ToLower(u.Hostname())
	id := ""
	switch host {
	case "api.deepseek.com":
		id = "deepseek"
	case "api.moonshot.cn":
		id = "moonshot-cn"
	case "api.moonshot.ai":
		id = "moonshot"
	case "openrouter.ai":
		id = "openrouter"
	case "api.siliconflow.cn":
		id = "siliconflow-cn"
	case "api.siliconflow.com":
		id = "siliconflow"
	case "api.stepfun.com":
		id = "stepfun-cn"
	case "api.stepfun.ai":
		id = "stepfun"
	case "aihubmix.com":
		id = "aihubmix"
		if accountToken != "" {
			id = "aihubmix-account"
		}
	case "api-inference.modelscope.cn":
		id = "modelscope"
	case "open.bigmodel.cn":
		id = "glm"
	case "api.z.ai":
		id = "zai"
	case "api.kimi.com", "api.kimi.ai":
		if strings.Contains(u.Path, "/coding") {
			id = "kimi-code"
		}
	case "opencode.ai":
		if strings.Contains(u.Path, "/go") {
			id = "opencode-go"
		}
	case "api.minimaxi.com", "api.minimax.cn":
		id = "minimax-cn"
	case "api.minimax.io":
		id = "minimax"
	case "api.commandcode.ai":
		id = "commandcode"
	case "ollama.com":
		id = "ollama"
	case "api.mistral.ai":
		id = "mistral"
	}
	for _, p := range Presets() {
		if p.ID == id {
			c := p.Config
			c.Preset = p.ID
			c.Enabled = true
			if host == "api.minimax.cn" {
				c.URL = "https://api.minimax.cn/v1/token_plan/remains"
			}
			return c, true
		}
	}
	return Config{}, false
}

func RelayOrigin(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, vendor := range []string{"openai.com", "anthropic.com", "googleapis.com", "google.com", "azure.com", "amazonaws.com", "opencode.ai", "api.cline.bot", "siliconflow.cn", "siliconflow.com"} {
		if host == vendor || strings.HasSuffix(host, "."+vendor) {
			return ""
		}
	}
	return u.Scheme + "://" + u.Host
}
