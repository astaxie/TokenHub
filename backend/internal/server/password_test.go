package server

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestAdminPasswordsUseBcrypt(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "bcrypt-user",
		Email:    "bcrypt-user@example.com",
		Role:     "admin",
		Status:   StatusActive,
	}, "correct-password")
	if err != nil {
		t.Fatal(err)
	}

	stored := privateAdminUser(t, store, user.ID)
	if !strings.HasPrefix(stored.PasswordHash, "$2") {
		t.Fatalf("expected bcrypt password hash, got %q", stored.PasswordHash)
	}
	if stored.PasswordHash == HashSecret("correct-password") {
		t.Fatal("password must not use the generic SHA-256 secret hash")
	}
	if _, _, err := store.AuthenticateAdminUser(user.Username, "correct-password", time.Hour); err != nil {
		t.Fatalf("expected bcrypt password authentication to succeed: %v", err)
	}
	if _, _, err := store.AuthenticateAdminUser(user.Username, "wrong-password", time.Hour); AsHTTPError(err).Code != "invalid_credentials" {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}

func TestLegacyAdminPasswordMigratesAfterLogin(t *testing.T) {
	store := NewMemoryStore()
	legacyHash := HashSecret("legacy-password")
	user, err := store.CreateAdminUser(AdminUser{
		Username:     "legacy-user",
		Email:        "legacy-user@example.com",
		Role:         "admin",
		Status:       StatusActive,
		PasswordHash: legacyHash,
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.AuthenticateAdminUser(user.Username, "legacy-password", time.Hour); err != nil {
		t.Fatalf("expected legacy password authentication to succeed: %v", err)
	}
	stored := privateAdminUser(t, store, user.ID)
	if stored.PasswordHash == legacyHash || !strings.HasPrefix(stored.PasswordHash, "$2") {
		t.Fatalf("expected legacy hash to be upgraded, got %q", stored.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("legacy-password")) != nil {
		t.Fatal("upgraded hash does not verify the existing password")
	}
}

func TestOversizeLegacyAdminPasswordMigratesAfterLogin(t *testing.T) {
	store := NewMemoryStore()
	password := strings.Repeat("a", 73)
	legacyHash := HashSecret(password)
	user, err := store.CreateAdminUser(AdminUser{
		Username:     "oversize-legacy-user",
		Email:        "oversize-legacy-user@example.com",
		Role:         "admin",
		Status:       StatusActive,
		PasswordHash: legacyHash,
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.AuthenticateAdminUser(user.Username, password, time.Hour); err != nil {
		t.Fatalf("expected oversize legacy password authentication to succeed: %v", err)
	}
	stored := privateAdminUser(t, store, user.ID)
	if stored.PasswordHash == legacyHash || !strings.HasPrefix(stored.PasswordHash, prehashedPasswordPrefix) {
		t.Fatalf("expected legacy hash to be upgraded, got %q", stored.PasswordHash)
	}
	if _, _, err := store.AuthenticateAdminUser(user.Username, password, time.Hour); err != nil {
		t.Fatalf("expected upgraded password authentication to succeed: %v", err)
	}
	if _, _, err := store.AuthenticateAdminUser(user.Username, password+"x", time.Hour); AsHTTPError(err).Code != "invalid_credentials" {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}

func TestAdminPasswordRejectsBcryptOversizeInput(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateAdminUser(AdminUser{
		Username: "oversize-user",
		Email:    "oversize-user@example.com",
		Role:     "admin",
		Status:   StatusActive,
	}, strings.Repeat("a", 73))
	if AsHTTPError(err).Code != "invalid_password" {
		t.Fatalf("expected invalid password error, got %v", err)
	}
}

func privateAdminUser(t *testing.T, store *GormStore, userID string) AdminUser {
	t.Helper()
	var user AdminUser
	if err := store.db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	return user
}
