package server

import (
	"net/url"
	"strings"

	"tokenhub/backend/internal/quotasource"
)

func magpieCloudSource(p magpieQuotaProvider) (quotasource.Config, bool) {
	if p.AccessKeyID == "" || p.SecretAccessKey == "" {
		return quotasource.Config{}, false
	}
	base := firstNonEmpty(p.Chat, p.Responses, p.Anthropic)
	u, _ := url.Parse(base)
	host := ""
	path := ""
	if u != nil {
		host = u.Hostname()
		path = u.Path
	}
	id := ""
	region := ""
	switch {
	case p.Preset == "volcengine" || p.ID == "volcengine" || strings.HasSuffix(host, ".volces.com"):
		id = "volcengine-coding"
		region = "cn-beijing"
		if p.KeyPlan == "agent" || p.KeyPlan == "" && strings.Contains(path, "/plan") {
			id = "volcengine-agent"
		}
		if strings.HasPrefix(host, "ark.") {
			region = strings.TrimSuffix(strings.TrimPrefix(host, "ark."), ".volces.com")
		}
	case p.Preset == "tencent-cloud" || p.ID == "tencent-cloud" || strings.HasSuffix(host, ".tencentmaas.com"):
		id = "tencent"
		region = "ap-guangzhou"
		if p.KeyPlan == "enterprise-intl" || p.KeyPlan == "intl" || p.KeyPlan == "" && strings.Contains(host, "-intl.") {
			region = "ap-singapore"
		}
	case p.Preset == "alibaba-token-plan" || p.ID == "alibaba-token-plan" || strings.HasSuffix(host, ".aliyuncs.com"):
		id = "alibaba"
		region = "cn-beijing"
		if strings.Contains(host, "ap-southeast-1") {
			region = "ap-southeast-1"
		}
	}
	for _, preset := range quotasource.Presets() {
		if preset.ID == id {
			c := preset.Config
			c.Enabled = true
			c.Preset = id
			c.AccessKeyID = p.AccessKeyID
			c.Region = region
			return c, true
		}
	}
	return quotasource.Config{}, false
}
