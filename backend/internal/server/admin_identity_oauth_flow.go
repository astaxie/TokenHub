package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	adminOAuthFlowTTL                          = 10 * time.Minute
	adminOAuthExchangeTTL                      = time.Minute
	adminOAuthFlowsPerClientScopeProviderLimit = 16
	adminOAuthFlowsPerClientScopeLimit         = 32
	adminOAuthFlowsGlobalLimit                 = 4096
	adminOAuthStateCookiePrefix                = "tokenhub_admin_oauth_state_"
	adminOAuthStateCookiePath                  = "/api/admin/auth/oauth/callback"
)

type adminOAuthFlowLimits struct {
	ClientScopeProvider int64
	ClientScope         int64
	Global              int64
}

var defaultAdminOAuthFlowLimits = adminOAuthFlowLimits{
	ClientScopeProvider: adminOAuthFlowsPerClientScopeProviderLimit,
	ClientScope:         adminOAuthFlowsPerClientScopeLimit,
	Global:              adminOAuthFlowsGlobalLimit,
}

type adminOAuthFlow struct {
	State                string
	BrowserNonce         string
	Source               string
	ProviderID           string
	ReturnURL            string
	RedirectURI          string
	CodeChallenge        string
	ProviderCodeVerifier string
	CookieSecure         bool
	CreatedAt            time.Time
}

type adminOAuthFlowRecord struct {
	ID                   string `gorm:"primaryKey"`
	StateHash            string `gorm:"uniqueIndex"`
	BrowserNonceHash     string
	ClientScopeHash      string `gorm:"index:idx_admin_oauth_flow_scope_expiry,priority:1;index:idx_admin_oauth_flow_scope_provider_expiry,priority:1"`
	ProviderID           string `gorm:"index:idx_admin_oauth_flow_scope_provider_expiry,priority:2"`
	ReturnURL            string
	RedirectURI          string
	CodeChallenge        string
	ProviderCodeVerifier string
	CookieSecure         bool
	CreatedAt            time.Time
	ExpiresAt            time.Time `gorm:"index;index:idx_admin_oauth_flow_scope_expiry,priority:2;index:idx_admin_oauth_flow_scope_provider_expiry,priority:3"`
}

type adminOAuthExchange struct {
	Code          string
	CodeChallenge string
	UserID        string
	CreatedAt     time.Time
}

type adminOAuthExchangeRecord struct {
	ID            string `gorm:"primaryKey"`
	CodeHash      string `gorm:"uniqueIndex"`
	CodeChallenge string
	UserID        string `gorm:"index"`
	CreatedAt     time.Time
	ExpiresAt     time.Time `gorm:"index"`
}

func (s *GormStore) SaveAdminOAuthFlow(flow adminOAuthFlow) error {
	if strings.TrimSpace(flow.State) == "" || strings.TrimSpace(flow.BrowserNonce) == "" ||
		strings.TrimSpace(flow.Source) == "" ||
		strings.TrimSpace(flow.ProviderID) == "" || strings.TrimSpace(flow.ReturnURL) == "" ||
		strings.TrimSpace(flow.RedirectURI) == "" || !validAdminOAuthCodeChallenge(flow.CodeChallenge) ||
		!validAdminOAuthCodeVerifier(flow.ProviderCodeVerifier) {
		return fmt.Errorf("admin OAuth flow is incomplete")
	}
	return s.saveAdminOAuthFlow(flow, defaultAdminOAuthFlowLimits)
}

func (s *GormStore) saveAdminOAuthFlow(flow adminOAuthFlow, limits adminOAuthFlowLimits) error {
	if limits.ClientScopeProvider <= 0 || limits.ClientScope <= 0 || limits.Global <= 0 {
		return fmt.Errorf("admin OAuth flow limits must be positive")
	}
	clientScopeHash, err := s.adminOAuthClientScopeHash(flow.Source)
	if err != nil {
		return err
	}
	record := adminOAuthFlowRecord{
		ID:                   NewID("oauth_flow"),
		StateHash:            HashSecret(flow.State),
		BrowserNonceHash:     HashSecret(flow.BrowserNonce),
		ClientScopeHash:      clientScopeHash,
		ProviderID:           flow.ProviderID,
		ReturnURL:            flow.ReturnURL,
		RedirectURI:          flow.RedirectURI,
		CodeChallenge:        flow.CodeChallenge,
		ProviderCodeVerifier: flow.ProviderCodeVerifier,
		CookieSecure:         flow.CookieSecure,
	}
	var admissionErr error
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "admin_oauth_flow", "capacity"); err != nil {
			return err
		}
		databaseNow, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		record.CreatedAt = databaseNow
		record.ExpiresAt = databaseNow.Add(adminOAuthFlowTTL)
		if err := tx.Where("expires_at <= ?", databaseNow).Delete(&adminOAuthFlowRecord{}).Error; err != nil {
			return err
		}
		checks := []struct {
			limit int64
			where string
			args  []any
		}{
			{limits.ClientScopeProvider, "client_scope_hash = ? AND provider_id = ? AND expires_at > ?", []any{record.ClientScopeHash, record.ProviderID, databaseNow}},
			{limits.ClientScope, "client_scope_hash = ? AND expires_at > ?", []any{record.ClientScopeHash, databaseNow}},
			{limits.Global, "expires_at > ?", []any{databaseNow}},
		}
		var retryAt time.Time
		for _, check := range checks {
			limited, availableAt, err := adminOAuthFlowLimitReached(tx, check.limit, check.where, check.args...)
			if err != nil {
				return err
			}
			if limited && availableAt.After(retryAt) {
				retryAt = availableAt
			}
		}
		if !retryAt.IsZero() {
			admissionErr = adminOAuthFlowLimitError(databaseNow, retryAt)
			return nil
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		return err
	}
	return admissionErr
}

func (s *GormStore) adminOAuthClientScopeHash(rawIP string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(rawIP))
	if err != nil {
		return "", fmt.Errorf("admin OAuth flow source is invalid")
	}
	address = address.Unmap().WithZone("")
	prefixBits := 64
	if address.Is4() {
		prefixBits = 24
	}
	scope := netip.PrefixFrom(address, prefixBits).Masked().String()
	key := strings.TrimSpace(s.secretKey)
	if key == "" {
		key = "tokenhub-admin-oauth-client-scope-development"
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte("tokenhub-admin-oauth-client-scope-v1\x00"))
	_, _ = mac.Write([]byte(scope))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func adminOAuthFlowLimitReached(tx *gorm.DB, limit int64, where string, args ...any) (bool, time.Time, error) {
	query := tx.Model(&adminOAuthFlowRecord{}).Where(where, args...)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, time.Time{}, err
	}
	if count < limit {
		return false, time.Time{}, nil
	}
	var earliest adminOAuthFlowRecord
	if err := tx.Model(&adminOAuthFlowRecord{}).
		Select("expires_at").
		Where(where, args...).
		Order("expires_at ASC").
		Take(&earliest).Error; err != nil {
		return false, time.Time{}, err
	}
	return true, earliest.ExpiresAt, nil
}

func adminOAuthFlowLimitError(now time.Time, retryAt time.Time) error {
	retryAfter := retryAt.Sub(now)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	if retryAfter > adminOAuthFlowTTL {
		retryAfter = adminOAuthFlowTTL
	}
	seconds := int((retryAfter + time.Second - 1) / time.Second)
	err := NewHTTPError(http.StatusTooManyRequests, "oauth_start_rate_limited", "Too many pending OAuth sign-ins")
	err.Headers = map[string]string{"Retry-After": strconv.Itoa(seconds)}
	return err
}

func (s *GormStore) ConsumeAdminOAuthFlow(state string, browserNonce string) (adminOAuthFlow, bool, error) {
	state = strings.TrimSpace(state)
	browserNonce = strings.TrimSpace(browserNonce)
	if state == "" || browserNonce == "" {
		return adminOAuthFlow{}, false, nil
	}
	stateHash := HashSecret(state)
	browserNonceHash := HashSecret(browserNonce)
	var record adminOAuthFlowRecord
	consumed := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "admin_oauth_flow", stateHash); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		databaseNow, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if err := query.First(&record, "state_hash = ? AND browser_nonce_hash = ? AND expires_at > ?", stateHash, browserNonceHash, databaseNow).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND state_hash = ? AND browser_nonce_hash = ?", record.ID, stateHash, browserNonceHash).Delete(&adminOAuthFlowRecord{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		consumed = true
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return adminOAuthFlow{}, false, nil
		}
		return adminOAuthFlow{}, false, err
	}
	return adminOAuthFlow{
		State:                state,
		BrowserNonce:         browserNonce,
		ProviderID:           record.ProviderID,
		ReturnURL:            record.ReturnURL,
		RedirectURI:          record.RedirectURI,
		CodeChallenge:        record.CodeChallenge,
		ProviderCodeVerifier: record.ProviderCodeVerifier,
		CookieSecure:         record.CookieSecure,
		CreatedAt:            record.CreatedAt,
	}, consumed, nil
}

func (s *GormStore) SaveAdminOAuthExchange(exchange adminOAuthExchange) error {
	if strings.TrimSpace(exchange.Code) == "" || !validAdminOAuthCodeChallenge(exchange.CodeChallenge) || strings.TrimSpace(exchange.UserID) == "" {
		return fmt.Errorf("admin OAuth exchange is incomplete")
	}
	record := adminOAuthExchangeRecord{
		ID:            NewID("oauth_exchange"),
		CodeHash:      HashSecret(exchange.Code),
		CodeChallenge: exchange.CodeChallenge,
		UserID:        exchange.UserID,
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		databaseNow, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		record.CreatedAt = databaseNow
		record.ExpiresAt = databaseNow.Add(adminOAuthExchangeTTL)
		if err := tx.Where("expires_at <= ?", databaseNow).Delete(&adminOAuthExchangeRecord{}).Error; err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
}

func (s *GormStore) ConsumeAdminOAuthExchange(code string, codeVerifier string) (adminOAuthExchange, bool, error) {
	code = strings.TrimSpace(code)
	codeChallenge, valid := adminOAuthCodeChallenge(codeVerifier)
	if code == "" || !valid {
		return adminOAuthExchange{}, false, nil
	}
	codeHash := HashSecret(code)
	var record adminOAuthExchangeRecord
	consumed := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.lockScopeForUpdate(tx, "admin_oauth_exchange", codeHash); err != nil {
			return err
		}
		query := tx
		if s.dbDriver == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		databaseNow, err := s.databaseNow(tx)
		if err != nil {
			return err
		}
		if err := query.First(&record, "code_hash = ? AND code_challenge = ? AND expires_at > ?", codeHash, codeChallenge, databaseNow).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND code_hash = ? AND code_challenge = ?", record.ID, codeHash, codeChallenge).Delete(&adminOAuthExchangeRecord{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		consumed = true
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return adminOAuthExchange{}, false, nil
		}
		return adminOAuthExchange{}, false, err
	}
	return adminOAuthExchange{
		Code:          code,
		CodeChallenge: record.CodeChallenge,
		UserID:        record.UserID,
		CreatedAt:     record.CreatedAt,
	}, consumed, nil
}

func adminOAuthStateCookieName(state string) string {
	return adminOAuthStateCookiePrefix + HashSecret(strings.TrimSpace(state))[:24]
}

func setAdminOAuthBindingCookie(w http.ResponseWriter, name string, value string, path string, secure bool, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl / time.Second),
		Expires:  time.Now().UTC().Add(ttl),
	})
}

func validateAdminOAuthPKCE(codeChallenge string, method string) (string, error) {
	codeChallenge = strings.TrimSpace(codeChallenge)
	if method != "S256" || !validAdminOAuthCodeChallenge(codeChallenge) {
		return "", NewHTTPError(http.StatusBadRequest, "invalid_oauth_code_challenge", "OAuth code challenge must use S256")
	}
	return codeChallenge, nil
}

func validAdminOAuthCodeChallenge(codeChallenge string) bool {
	if len(codeChallenge) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(codeChallenge)
	return err == nil && len(decoded) == sha256.Size
}

func validAdminOAuthCodeVerifier(codeVerifier string) bool {
	_, ok := adminOAuthCodeChallenge(strings.TrimSpace(codeVerifier))
	return ok
}

// newAdminOAuthProviderPKCE creates the backend-owned PKCE pair that binds the
// identity-provider authorization code to this server: the challenge travels in
// the authorize redirect and the verifier is presented at the token endpoint.
func newAdminOAuthProviderPKCE() (string, string, error) {
	codeVerifier, err := randomHex(32)
	if err != nil {
		return "", "", err
	}
	codeChallenge, ok := adminOAuthCodeChallenge(codeVerifier)
	if !ok {
		return "", "", fmt.Errorf("generated OAuth code verifier is invalid")
	}
	return codeVerifier, codeChallenge, nil
}

func adminOAuthCodeChallenge(codeVerifier string) (string, bool) {
	codeVerifier = strings.TrimSpace(codeVerifier)
	if len(codeVerifier) < 43 || len(codeVerifier) > 128 {
		return "", false
	}
	for _, char := range codeVerifier {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '.' || char == '_' || char == '~' {
			continue
		}
		return "", false
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]), true
}

func clearAdminOAuthBindingCookie(w http.ResponseWriter, name string, path string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	})
}
