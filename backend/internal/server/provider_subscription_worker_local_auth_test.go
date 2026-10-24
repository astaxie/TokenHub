package server

import "testing"

func TestSubscriptionAuthorizationRequiresOwnedLoopbackLandingPage(t *testing.T) {
	for _, test := range []struct {
		url            string
		owned, allowed bool
	}{
		{"https://accounts.example.test/login", false, true},
		{"http://127.0.0.1:18432/", true, true},
		{"http://localhost:19654/", true, true},
		{"http://[::1]:19723/", true, true},
		{"http://127.0.0.1:18432/", false, false},
		{"http://127.0.0.1/", true, false},
		{"http://127.0.0.1:0/", true, false},
		{"http://127.0.0.1:65536/", true, false},
		{"http://127.0.0.1.attacker.example:18432/", true, false},
		{"http://169.254.169.254:18432/", true, false},
		{"http://user:secret@127.0.0.1:18432/", true, false},
		{"http://127.0.0.1:18432/#fragment", true, false},
		{"javascript:alert(1)", true, false},
	} {
		if got := subscriptionAuthorizationURLAllowed(test.url, test.owned); got != test.allowed {
			t.Fatalf("authorization URL %q owned=%v allowed=%v, want %v", test.url, test.owned, got, test.allowed)
		}
	}
}
