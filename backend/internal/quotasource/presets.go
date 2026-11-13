package quotasource

import (
	"errors"
	"net/url"
	"strings"
)

type Preset struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Config Config `json:"config"`
}

// Presets are explicit opt-in rules, independent of the inference protocol.
// Account tokens and cookies remain in the gateway's encrypted store.
func Presets() []Preset {
	return []Preset{
		{"fusecode", "Fusecode", Config{Preset: "fusecode", URL: "https://fusecode.ai/v1/usage", Auth: "bearer", Expression: "$remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %; Month: subscription.monthly_usage_usd / subscription.monthly_limit_usd %"}},
		{"deepseek", "DeepSeek", Config{URL: "https://api.deepseek.com/user/balance", Auth: "bearer", Expression: "¥sum(balance_infos[currency=CNY].total_balance); $sum(balance_infos[currency=USD].total_balance)"}},
		{"moonshot-cn", "Moonshot China", Config{URL: "https://api.moonshot.cn/v1/users/me/balance", Auth: "bearer", Expression: "¥data.available_balance"}},
		{"moonshot", "Moonshot", Config{URL: "https://api.moonshot.ai/v1/users/me/balance", Auth: "bearer", Expression: "$data.available_balance"}},
		{"openrouter", "OpenRouter", Config{URL: "https://openrouter.ai/api/v1/credits", Auth: "bearer", Expression: "$(data.total_credits - data.total_usage)"}},
		{"stepfun-cn", "StepFun China", Config{URL: "https://api.stepfun.com/v1/accounts", Auth: "bearer", Expression: "¥balance"}},
		{"stepfun", "StepFun", Config{URL: "https://api.stepfun.ai/v1/accounts", Auth: "bearer", Expression: "$balance"}},
		{"aihubmix", "AiHubMix key", Config{URL: "https://aihubmix.com/dashboard/billing/remain", Auth: "bearer", Expression: "$total_usage"}},
		{"aihubmix-account", "AiHubMix account", Config{URL: "https://aihubmix.com/api/user/self", Auth: "raw", Expression: "$data.quota / 500000"}},
		{"modelscope", "ModelScope", Config{URL: "https://modelscope.cn/openapi/v1/magicubes/balance", Auth: "bearer", Expression: "data.available_balance", Unit: "magicubes"}},
		{"newapi-key", "NewAPI key", Config{Auth: "bearer", Expression: "$data.total_available / 500000"}},
		{"newapi-account", "NewAPI account", Config{Auth: "raw", Expression: "$data.quota / 500000"}},
		{"sub2api", "Sub2API key", Config{Auth: "bearer", Expression: "$remaining"}},
		{"sub2api-account", "Sub2API account", Config{Auth: "bearer", Expression: "$data.balance"}},
		{"glm", "GLM Coding Plan", Config{Preset: "glm", URL: "https://open.bigmodel.cn/api/monitor/usage/quota/limit", Auth: "raw"}},
		{"zai", "Z.ai Coding Plan", Config{Preset: "glm", URL: "https://api.z.ai/api/monitor/usage/quota/limit", Auth: "raw"}},
		{"kimi-code", "Kimi Code", Config{Preset: "kimi-code", URL: "https://api.kimi.com/coding/v1/usages", Auth: "bearer"}},
		{"opencode-go", "OpenCode Go", Config{Preset: "opencode-go", URL: "https://opencode.ai/zen/go/v1/usage", Auth: "bearer"}},
		{"minimax-cn", "MiniMax China", Config{Preset: "minimax", URL: "https://api.minimaxi.com/v1/token_plan/remains", Auth: "bearer"}},
		{"minimax", "MiniMax", Config{Preset: "minimax", URL: "https://api.minimax.io/v1/token_plan/remains", Auth: "bearer"}},
		{"commandcode", "Command Code", Config{Preset: "commandcode", URL: "https://api.commandcode.ai/alpha/billing/credits", Auth: "bearer"}},
		{"ollama", "Ollama Cloud", Config{Preset: "ollama", URL: "https://ollama.com/api/balance", Auth: "bearer"}},

		{"volcengine-coding", "Volcengine Coding Plan", Config{Preset: "volcengine-coding", URL: "https://open.volcengineapi.com/", Auth: "volcengine"}},
		{"volcengine-agent", "Volcengine Agent Plan", Config{Preset: "volcengine-agent", URL: "https://open.volcengineapi.com/", Auth: "volcengine"}},
		{"tencent", "Tencent Token Plan", Config{Preset: "tencent", URL: "https://tokenhub.tencentcloudapi.com/", Auth: "tencent"}},
		{"alibaba", "Alibaba Token Plan", Config{Preset: "alibaba", URL: "https://modelstudio.cn-beijing.aliyuncs.com/modelstudio/cli/generateAccessToken", Auth: "alibaba"}},
		{"stepfun-plan", "StepFun Plan", Config{Preset: "stepfun-plan", URL: "https://platform.stepfun.ai/api/step.openapi.devcenter.Dashboard/QueryStepPlanRateLimit", Auth: "none", Method: "POST", Body: "{}", Headers: map[string]string{"Oasis-Appid": "20700", "Oasis-Platform": "web", "Connect-Protocol-Version": "1", "Content-Type": "application/json"}}},
		{"stepfun-plan-cn", "StepFun Plan China", Config{Preset: "stepfun-plan", URL: "https://platform.stepfun.com/api/step.openapi.devcenter.Dashboard/QueryStepPlanRateLimit", Auth: "none", Method: "POST", Body: "{}", Headers: map[string]string{"Oasis-Appid": "10300", "Oasis-Platform": "web", "Connect-Protocol-Version": "1", "Content-Type": "application/json"}}},
		{"zhipu-balance", "Zhipu balance", Config{URL: "https://open.bigmodel.cn/api/biz/account/query-customer-account-report", Auth: "bearer", Expression: "¥data.availableBalance"}},
		{"zai-balance", "Z.ai balance", Config{URL: "https://api.z.ai/api/biz/account/query-customer-account-report", Auth: "bearer", Expression: "$data.availableBalance"}},
		{"mistral", "Mistral", Config{Preset: "mistral", URL: "https://api.mistral.ai/v1/billing/budget", Auth: "bearer"}},
	}
}

// Resolve fills defaults while retaining administrator overrides and relay URLs.
func Resolve(c Config, base string) (Config, error) {
	if c.Preset == "" {
		return c, c.Validate()
	}
	var preset *Preset
	for _, p := range Presets() {
		if p.ID == c.Preset {
			copy := p
			preset = &copy
			break
		}
	}
	if preset == nil {
		return c, errors.New("unknown quota preset")
	}
	defaults := preset.Config
	if c.URL == "" {
		c.URL = defaults.URL
	}
	if c.Expression == "" && len(c.Windows) == 0 {
		c.Expression = defaults.Expression
	}
	if c.Method == "" {
		c.Method = defaults.Method
	}
	if c.Body == "" {
		c.Body = defaults.Body
	}
	if c.Headers == nil {
		c.Headers = defaults.Headers
	}
	if c.Auth == "" {
		c.Auth = defaults.Auth
	}
	if defaults.Preset != "" {
		c.Preset = defaults.Preset
	}
	if c.URL == "" {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" {
			return c, errors.New("quota preset requires a provider base URL")
		}
		paths := map[string]string{"newapi-key": "/api/usage/token", "newapi-account": "/api/user/self", "sub2api": "/v1/usage", "sub2api-account": "/api/v1/user/profile"}
		u.Path = paths[c.Preset]
		u.RawPath = ""
		u.RawQuery = ""
		u.Fragment = ""
		c.URL = strings.TrimRight(u.String(), "/")
	}
	return c, c.Validate()
}
