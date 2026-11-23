package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func postAdminLogin(t *testing.T, handler http.Handler, identity string, password string) responseBody {
	t.Helper()
	return doJSON(t, handler, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": identity,
		"password": password,
	}, "")
}

func TestAdminLoginLimitsRepeatedFailures(t *testing.T) {
	config := ConfigFromEnv()
	config.BootstrapAdminPassword = "configured-login-guard-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	handler := NewWithConfig(store, config).Handler()

	for attempt := 1; attempt <= adminLoginAccountLimit; attempt++ {
		response := postAdminLogin(t, handler, "admin@tokenhub.local", "wrong-password")
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body, "invalid_credentials") {
			t.Fatalf("attempt %d: expected 401 invalid_credentials, got %d: %s", attempt, response.Code, response.Body)
		}
	}
	limited := postAdminLogin(t, handler, "admin@tokenhub.local", "wrong-password")
	if limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body, "login_rate_limited") {
		t.Fatalf("expected 429 login_rate_limited, got %d: %s", limited.Code, limited.Body)
	}
	// The correct password must not bypass an active limit: otherwise the
	// counter would tell an attacker they found it.
	if correct := postAdminLogin(t, handler, "admin@tokenhub.local", config.BootstrapAdminPassword); correct.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the limit to apply to a correct password too, got %d: %s", correct.Code, correct.Body)
	}
}

func TestAdminLoginSuccessClearsAccountBudget(t *testing.T) {
	config := ConfigFromEnv()
	config.BootstrapAdminPassword = "configured-login-guard-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	handler := NewWithConfig(store, config).Handler()

	failures := adminLoginAccountLimit - 1
	for attempt := 1; attempt <= failures; attempt++ {
		if response := postAdminLogin(t, handler, "admin@tokenhub.local", "wrong-password"); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d: %s", attempt, response.Code, response.Body)
		}
	}
	if response := postAdminLogin(t, handler, "admin@tokenhub.local", config.BootstrapAdminPassword); response.Code != http.StatusOK {
		t.Fatalf("expected sign-in below the limit to succeed, got %d: %s", response.Code, response.Body)
	}
	// A success resets the account budget, so the next failure is a plain 401.
	if response := postAdminLogin(t, handler, "admin@tokenhub.local", "wrong-password"); response.Code != http.StatusUnauthorized {
		t.Fatalf("expected the account budget to reset after a success, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminLoginLimitsFailuresPerSourceAcrossAccounts(t *testing.T) {
	config := ConfigFromEnv()
	config.BootstrapAdminPassword = "configured-login-guard-password"
	store := NewMemoryStoreWithConfig(config)
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	handler := NewWithConfig(store, config).Handler()

	identities := []string{"admin@tokenhub.local", "admin", "missing@tokenhub.local", "unknown-user"}
	attempts := 0
	for attempt := 0; attempt < adminLoginSourceLimit; attempt++ {
		identity := identities[attempt%len(identities)]
		if response := postAdminLogin(t, handler, identity, "wrong-password"); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d for %q: expected 401, got %d: %s", attempt, identity, response.Code, response.Body)
		}
		attempts++
	}
	if attempts != adminLoginSourceLimit {
		t.Fatalf("expected %d attempts, ran %d", adminLoginSourceLimit, attempts)
	}
	// A fresh account from the same source is still blocked, so rotating
	// identities does not buy unlimited guesses.
	response := postAdminLogin(t, handler, "another-unseen-user", "wrong-password")
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body, "login_rate_limited") {
		t.Fatalf("expected the source budget to block a new identity, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminLoginLimiterExpiresAndStaysBounded(t *testing.T) {
	now := time.Date(2026, time.November, 23, 12, 0, 0, 0, time.UTC)
	var limiter adminLoginLimiter

	for attempt := 1; attempt <= adminLoginAccountLimit; attempt++ {
		if reservation, wait := limiter.reserve("192.0.2.10", "user@example.test", now); wait != 0 {
			t.Fatalf("attempt %d: unexpectedly limited for %s", attempt, wait)
		} else {
			reservation.fail()
		}
	}
	if _, wait := limiter.reserve("192.0.2.10", "user@example.test", now); wait <= 0 {
		t.Fatal("expected the account budget to be exhausted")
	}
	if _, wait := limiter.reserve("192.0.2.10", "user@example.test", now.Add(adminLoginWindow+time.Second)); wait != 0 {
		t.Fatalf("expected the window to expire, still limited for %s", wait)
	}

	bounded := adminLoginLimiter{}
	// Every reservation adds a source and an account bucket, so this exactly
	// fills the map without sweeping anything out.
	for index := 0; index < adminLoginMaxBuckets/2; index++ {
		source := "198.51.100." + strconv.Itoa(index%256) + "-" + strconv.Itoa(index/256)
		reservation, wait := bounded.reserve(source, source, now)
		if reservation.limiter == nil {
			t.Fatalf("bucket %d was rejected before the cap: %s", index, wait)
		}
		reservation.release()
	}
	if _, wait := bounded.reserve("203.0.113.200", "overflow@example.test", now); wait <= 0 {
		t.Fatal("expected the bucket cap to reject new keys instead of growing without bound")
	}
}

func TestAdminLoginSpendsDummyVerificationForUnknownAccounts(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: strings.Repeat("a", 64)})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })

	original := adminLoginPasswordVerifier
	verifications := 0
	adminLoginPasswordVerifier = func(hash, password string) (bool, bool) {
		verifications++
		return original(hash, password)
	}
	t.Cleanup(func() { adminLoginPasswordVerifier = original })

	_, _, err := server.store.AuthenticateAdminUser("absent@example.test", "synthetic-password", time.Hour)
	if AsHTTPError(err).Status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an unknown account, got %v", err)
	}
	if verifications == 0 {
		t.Fatal("unknown accounts must spend the same verification work as a wrong password")
	}
}

func TestAdminLoginRejectsDisabledAccountOnlyAfterPasswordMatches(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "disabled-user",
		Email:    "disabled-user@example.com",
		Role:     "user",
		Status:   StatusDisabled,
	}, "disabled-account-password")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.AuthenticateAdminUser(user.Username, "wrong-password", time.Hour); AsHTTPError(err).Code != "invalid_credentials" {
		t.Fatalf("a disabled account must not be distinguishable without the password, got %v", err)
	}
	if _, _, err := store.AuthenticateAdminUser(user.Username, "disabled-account-password", time.Hour); AsHTTPError(err).Code != "admin_user_disabled" {
		t.Fatalf("expected admin_user_disabled once the password matches, got %v", err)
	}
}
