package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminPasswordResetLinkKeepsTokenOutOfQueryAndUntrustedHosts(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{
		PublicBaseURL:      "https://api.tokenhub.example",
		CORSAllowedOrigins: []string{"https://console.tokenhub.example"},
	})
	t.Cleanup(func() { _ = server.Shutdown(t.Context()) })
	request := httptest.NewRequest(http.MethodPost, "https://api.tokenhub.example/api/admin/users/user/reset-password-email", nil)
	request.RemoteAddr = "198.51.100.7:4321"
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Referer", "https://attacker.example/reset")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Forwarded-Proto", "http")

	const token = "password-reset-secret-token"
	link := server.adminPasswordResetLink(request, token)
	target, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if target.Scheme != "https" || target.Host != "console.tokenhub.example" || target.Path != "/" {
		t.Fatalf("password reset target trusted request headers: %s", link)
	}
	if target.Query().Has("reset_token") || strings.Contains(target.RawQuery, token) {
		t.Fatalf("password reset token leaked in query: %s", link)
	}
	fragment, err := url.ParseQuery(target.Fragment)
	if err != nil || fragment.Get("reset_token") != token {
		t.Fatalf("password reset token fragment = %q, err=%v", target.Fragment, err)
	}
}

func TestAdminPasswordResetRejectsInvalidTokensBeforeHashing(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "reset-precheck-user", Email: "reset-precheck@example.test", Role: "user", Status: StatusActive,
	}, "old-password-123")
	if err != nil {
		t.Fatal(err)
	}
	expiredToken, _, err := store.CreateAdminPasswordResetToken(user.ID, user.ID, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	hashCalls := 0
	for name, token := range map[string]string{
		"unknown": "rst_unknown_reset_token",
		"expired": expiredToken,
	} {
		t.Run(name, func(t *testing.T) {
			_, resetErr := store.resetAdminUserPassword(token, "new-password-123", func(string) (string, error) {
				hashCalls++
				return "", nil
			})
			if httpErr := AsHTTPError(resetErr); httpErr.Code != "invalid_reset_token" {
				t.Fatalf("invalid token error = %v", resetErr)
			}
		})
	}
	if hashCalls != 0 {
		t.Fatalf("invalid reset tokens invoked password hashing %d times", hashCalls)
	}
}

func TestAdminPasswordResetHashingDoesNotHoldGlobalStoreMutex(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "reset-lock-user", Email: "reset-lock@example.test", Role: "user", Status: StatusActive,
	}, "old-password-123")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := store.CreateAdminPasswordResetToken(user.ID, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := hashPassword("new-password-123")
	if err != nil {
		t.Fatal(err)
	}

	hashingStarted := make(chan struct{})
	releaseHashing := make(chan struct{})
	resetDone := make(chan error, 1)
	go func() {
		_, resetErr := store.resetAdminUserPassword(token, "new-password-123", func(string) (string, error) {
			close(hashingStarted)
			<-releaseHashing
			return passwordHash, nil
		})
		resetDone <- resetErr
	}()
	select {
	case <-hashingStarted:
	case <-time.After(time.Second):
		t.Fatal("password reset did not reach password hashing")
	}

	operationDone := make(chan error, 1)
	go func() {
		_, _, createErr := store.CreateAdminPasswordResetToken(user.ID, user.ID, time.Hour)
		operationDone <- createErr
	}()
	operationBlocked := false
	var operationErr error
	select {
	case operationErr = <-operationDone:
	case <-time.After(time.Second):
		operationBlocked = true
	}
	close(releaseHashing)
	if resetErr := <-resetDone; resetErr != nil {
		t.Fatalf("password reset failed: %v", resetErr)
	}
	if operationBlocked {
		<-operationDone
		t.Fatal("password hashing held the global store mutex")
	}
	if operationErr != nil {
		t.Fatalf("concurrent store operation failed: %v", operationErr)
	}
}

func TestAdminPasswordResetTokenIsConsumedOnceAcrossInstances(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "password-reset.db")
	config := Config{SecretKey: "multi-instance-password-reset-secret"}
	storeA, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStoreWithConfig(databaseURL, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*GormStore{storeA, storeB} {
		sqlDB, dbErr := store.db.DB()
		if dbErr != nil {
			t.Fatal(dbErr)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	user, err := storeA.CreateAdminUser(AdminUser{
		Username: "reset-race-user", Email: "reset-race@example.test", Role: "user", Status: StatusActive,
	}, "old-password-123")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := storeA.CreateAdminPasswordResetToken(user.ID, user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	passwords := []string{"new-password-123-a", "new-password-123-b"}
	passwordHashes := make(map[string]string, len(passwords))
	for _, password := range passwords {
		passwordHash, hashErr := hashPassword(password)
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		passwordHashes[password] = passwordHash
	}

	type resetResult struct {
		password string
		err      error
	}
	start := make(chan struct{})
	hashing := make(chan struct{}, 2)
	releaseHashing := make(chan struct{})
	results := make(chan resetResult, 2)
	for index, store := range []*GormStore{storeA, storeB} {
		password := passwords[index]
		go func(store *GormStore, password string) {
			<-start
			_, resetErr := store.resetAdminUserPassword(token, password, func(password string) (string, error) {
				hashing <- struct{}{}
				<-releaseHashing
				return passwordHashes[password], nil
			})
			results <- resetResult{password: password, err: resetErr}
		}(store, password)
	}
	close(start)
	for range 2 {
		select {
		case <-hashing:
		case <-time.After(time.Second):
			close(releaseHashing)
			t.Fatal("both reset attempts did not pass token preflight")
		}
	}
	close(releaseHashing)
	successes := 0
	invalidTokens := 0
	successfulPassword := ""
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			successfulPassword = result.password
		case AsHTTPError(result.err).Code == "invalid_reset_token":
			invalidTokens++
		default:
			t.Fatalf("concurrent reset returned unexpected error: %v", result.err)
		}
	}
	if successes != 1 || invalidTokens != 1 {
		t.Fatalf("concurrent reset results: successes=%d invalid_tokens=%d", successes, invalidTokens)
	}
	if _, _, err := storeA.AuthenticateAdminUser(user.Email, successfulPassword, time.Hour); err != nil {
		t.Fatalf("winning password did not authenticate: %v", err)
	}
	for _, password := range passwords {
		if password == successfulPassword {
			continue
		}
		if _, _, err := storeA.AuthenticateAdminUser(user.Email, password, time.Hour); err == nil {
			t.Fatalf("losing password %q authenticated", password)
		}
	}
	for _, store := range []*GormStore{storeA, storeB} {
		if _, err := store.ResetAdminUserPassword(token, "replayed-password-123"); AsHTTPError(err).Code != "invalid_reset_token" {
			t.Fatalf("consumed reset token was reusable: %v", err)
		}
	}
}
