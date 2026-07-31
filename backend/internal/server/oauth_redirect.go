package server

import "net/url"

func oauthRedirectWithFragment(returnURL string, values url.Values) string {
	target, err := url.Parse(returnURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		target, _ = url.Parse("http://localhost:3000/overview")
	}
	query := target.Query()
	for key := range values {
		query.Del(key)
	}
	target.RawQuery = query.Encode()
	// Only place OAuth tokens in the URL fragment (never in the query string).
	// Query strings are logged by proxies and load balancers; fragments stay
	// client-side and are never transmitted to servers.
	target.Fragment = values.Encode()
	return target.String()
}
