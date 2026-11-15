package quotasource

import (
	"strings"
	"testing"
)

func TestMagpieScreenshotExpression(t *testing.T) {
	rule := "$remaining; 周: subscription.weekly_usage_usd / subscription.weekly_limit_usd %; 月: subscription.monthly_usage_usd / subscription.monthly_limit_usd %"
	parts, err := ReadParts([]byte(`{"remaining":0,"subscription":{"weekly_usage_usd":100.7,"weekly_limit_usd":100,"monthly_usage_usd":25.2,"monthly_limit_usd":100}}`), rule)
	if err != nil || len(parts) != 3 || parts[0].Text != "$0.00" || parts[1].Label != "周" || parts[1].Text != "100.7%" || parts[2].Text != "25.2%" {
		t.Fatalf("screenshot expression changed: %+v, %v", parts, err)
	}
	parts, err = ReadParts([]byte(`{"remaining":0,"subscription":{"monthly_usage_usd":25.2,"monthly_limit_usd":100}}`), rule)
	if err != nil || len(parts) != 3 || parts[1].Text != "—" || parts[2].Text != "25.2%" {
		t.Fatalf("partial reading changed: %+v, %v", parts, err)
	}
}

func TestMagpieRequestSeparatesKeyAndAccountToken(t *testing.T) {
	for _, token := range []string{"opaque-panel-token", "eyJheader.payload.signature", "Bearer explicit-token", "synthetic-key", "Bearer synthetic-key", ""} {
		t.Run(token, func(t *testing.T) {
			c := Config{Enabled: true, Auth: "magpie", Method: " post ", URL: "https://relay.example/balance?key=${API_KEY}", Expression: "$remaining", Headers: map[string]string{"X-Key": "{{token}}"}}
			c, err := Resolve(c, "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := PrepareMagpie(c, "synthetic-key", token)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got.URL, "key=synthetic-key") || got.Headers["X-Key"] != "synthetic-key" || got.Method != "POST" {
				t.Fatalf("request changed: %+v", got)
			}
			want := token
			if token == "eyJheader.payload.signature" {
				want = "Bearer " + token
			}
			if token == "synthetic-key" || token == "Bearer synthetic-key" {
				want = ""
			}
			if got.Headers["Authorization"] != want {
				t.Fatalf("authorization: got %q, want %q", got.Headers["Authorization"], want)
			}
		})
	}
}

func TestMagpieRelayDefaultsAndAccountFallback(t *testing.T) {
	for _, tc := range []struct{ path, token, header, wantPath, wantField string }{
		{"/v1/usage", "", "", "/v1/usage", "$remaining"},
		{"/api/user/self", "opaque-panel-token", "", "/api/user/self", "$data.quota / 500000"},
		{"/api/user/self", "", "", "/api/usage/token", "$data.total_available / 500000"},
		{"/api/v1/user/profile", "", "", "/v1/usage", "$remaining"},
		{"/api/v1/user/profile", "", "Cookie", "/api/v1/user/profile", "$data.balance"},
		{"/api/usage/token", "", "", "/api/usage/token", "$data.total_available / 500000"},
		{"/dashboard/billing/credit_grants", "", "", "/dashboard/billing/credit_grants", "$total_available"},
	} {
		t.Run(tc.path+tc.token+tc.header, func(t *testing.T) {
			c := Config{Enabled: true, Auth: "magpie", URL: "https://relay.example" + tc.path}
			if tc.header != "" {
				c.Headers = map[string]string{tc.header: "synthetic-session"}
			}
			c, err := Resolve(c, "")
			if err != nil {
				t.Fatal(err)
			}
			got, err := PrepareMagpie(c, "synthetic-key", tc.token)
			if err != nil || got.URL != "https://relay.example"+tc.wantPath || got.Expression != tc.wantField {
				t.Fatalf("relay semantics changed: %+v, %v", got, err)
			}
		})
	}
	if _, err := PrepareMagpie(Config{Auth: "magpie", URL: "https://relay.example/v1/usage"}, "key", "account-token"); err == nil {
		t.Fatal("key-only endpoint accepted account token")
	}
	c, err := PrepareMagpie(Config{Enabled: true, Auth: "magpie", URL: "https://relay.example/balance?key={key}", Expression: "$remaining"}, "key+ /?", "")
	if err != nil || !strings.HasSuffix(c.URL, "key=key%2B+%2F%3F") {
		t.Fatalf("key was not URL-escaped: %+v %v", c, err)
	}
}
