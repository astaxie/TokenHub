package quotasource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CloudRequest signs only the known control-plane host. An inference URL or
// arbitrary relay cannot receive account-wide cloud credentials by accident.
func CloudRequest(ctx context.Context, c Config, secret string, now time.Time) (*http.Request, error) {
	var req *http.Request
	var err error
	switch c.Auth {
	case "volcengine":
		action := "GetCodingPlanUsage"
		if c.Preset == "volcengine-agent" {
			action = "GetAFPUsage"
		}
		region := c.Region
		if region == "" {
			region = "cn-beijing"
		}
		query := url.Values{"Action": {action}, "Version": {"2024-01-01"}}
		body := []byte("{}")
		req, err = http.NewRequestWithContext(ctx, "POST", "https://open.volcengineapi.com/?"+volcQuery(query), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		auth, date, hash := volcSign(c.AccessKeyID, secret, region, query, body, now)
		req.Header.Set("Authorization", auth)
		req.Header.Set("Content-Type", volcContentType)
		req.Header.Set("X-Date", date)
		req.Header.Set("X-Content-Sha256", hash)
	case "tencent":
		region := c.Region
		if region == "" {
			region = "ap-guangzhou"
		}
		if c.PlanID == "" {
			return nil, errors.New("the Tencent quota requires an explicit TeamId to avoid combining different plans")
		}
		body, _ := json.Marshal(map[string]any{"TeamId": c.PlanID})
		const host = "tokenhub.tencentcloudapi.com"
		const action = "DescribeTokenPlan"
		req, err = http.NewRequestWithContext(ctx, "POST", "https://"+host+"/", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", tcContentType)
		req.Header.Set("X-TC-Action", action)
		req.Header.Set("X-TC-Version", "2026-03-22")
		req.Header.Set("X-TC-Region", region)
		req.Header.Set("X-TC-Timestamp", strconv.FormatInt(now.Unix(), 10))
		req.Header.Set("Authorization", tc3Sign(c.AccessKeyID, secret, "tokenhub", host, action, body, now))
	case "alibaba":
		region := c.Region
		if region == "" {
			region = "cn-beijing"
		}
		if region != "cn-beijing" && region != "ap-southeast-1" {
			return nil, errors.New("unsupported Alibaba Cloud quota region")
		}
		host := "modelstudio." + region + ".aliyuncs.com"
		const path = "/modelstudio/cli/generateAccessToken"
		req, err = http.NewRequestWithContext(ctx, "POST", "https://"+host+path, nil)
		if err != nil {
			return nil, err
		}
		// The caller supplies a unique request context; nanosecond time plus a hash
		// of the key is used only as a signing nonce, never as an idempotency key.
		nonce := volcHex([]byte(c.AccessKeyID + now.UTC().Format(time.RFC3339Nano)))[:32]
		for k, v := range aliACSSign(c.AccessKeyID, secret, host, path, "GenerateCLIAccessToken", "2026-02-10", nil, now, nonce) {
			if k != "host" {
				req.Header.Set(k, v)
			}
		}
	default:
		return nil, errors.New("unsupported cloud quota authentication")
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func AlibabaUsageRequest(ctx context.Context, region, token string) (*http.Request, error) {
	host, action := "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway"
	if region == "ap-southeast-1" {
		host, action = "bailian-singapore-cs.alibabacloud.com", "IntlBroadScopeAspnGateway"
	} else if region != "" && region != "cn-beijing" {
		return nil, errors.New("unsupported Alibaba Cloud quota region")
	}
	if region == "" {
		region = "cn-beijing"
	}
	const api = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"
	params, _ := json.Marshal(map[string]any{"Api": api, "V": "1.0", "Data": map[string]any{"cornerstoneParam": map[string]any{"protocol": "V2", "console": "ONE_CONSOLE", "productCode": "p_efm", "switchUserType": 3, "consoleSite": "BAILIAN_ALIYUN"}}})
	form := url.Values{"params": {string(params)}, "region": {region}}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+host+"/cli/api.json?action="+action+"&product=sfm_bailian&api="+url.QueryEscape(api), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func parseCloud(body []byte, c Config, now time.Time) (Result, error) {
	var ws []QuotaWindow
	plan := ""
	switch c.Preset {
	case "volcengine-coding", "volcengine-agent":
		var r struct {
			Result           json.RawMessage
			ResponseMetadata struct{ Error any }
		}
		if json.Unmarshal(body, &r) != nil || r.ResponseMetadata.Error != nil {
			return Result{}, errors.New("the Volcengine quota query was refused")
		}
		if c.Preset == "volcengine-agent" {
			ws, plan = volcReadAFP(r.Result)
		} else {
			ws = volcReadCodingPlan(r.Result)
		}
	case "tencent":
		var r struct {
			Response struct {
				tencentPlan
				Error any
			}
		}
		if json.Unmarshal(body, &r) != nil || r.Response.Error != nil {
			return Result{}, errors.New("the Tencent quota query was refused")
		}
		if w, ok := r.Response.tencentPlan.window(now); ok {
			ws = []QuotaWindow{w}
		}
		plan = r.Response.Name
		if r.Response.Status != "" && !strings.EqualFold(r.Response.Status, "enable") {
			for i := range ws {
				ws[i].Used = 100
			}
		}
	case "alibaba":
		var node map[string]json.RawMessage
		if json.Unmarshal(body, &node) != nil {
			return Result{}, errors.New("invalid Alibaba quota reply")
		}
		raw := body
		// The gateway wraps business data at data.DataV2.data.data; a cached or
		// explicitly supplied business response may already be unwrapped.
		if data, ok := node["data"]; ok {
			var env struct {
				Success *bool `json:"success"`
				DataV2  json.RawMessage
			}
			if json.Unmarshal(data, &env) == nil && len(env.DataV2) > 0 {
				if env.Success != nil && !*env.Success {
					return Result{}, errors.New("the Alibaba quota gateway refused the query")
				}
				raw = env.DataV2
				for i := 0; i < 2; i++ {
					var nested struct {
						Data json.RawMessage `json:"data"`
					}
					if json.Unmarshal(raw, &nested) == nil && len(nested.Data) > 0 && nested.Data[0] == '{' {
						raw = nested.Data
					}
				}
			}
		}
		ws = aliReadWindows(raw)
	case "stepfun-plan":
		var err error
		ws, err = readStepPlan(body)
		if err != nil {
			return Result{}, errors.New("invalid StepFun plan reply")
		}
	}
	return planResult(plan, ws, c, now)
}
