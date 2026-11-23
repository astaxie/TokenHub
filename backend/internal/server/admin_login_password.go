package server

import (
	"strings"
	"sync"
)

// adminLoginPasswordVerifier is a seam so tests can count verification work
// without depending on wall-clock timing.
var adminLoginPasswordVerifier = verifyPassword

// dummyPasswordHash provides realistic bcrypt work when there is no stored
// hash to compare against, so an absent account costs the same as a present one.
var dummyPasswordHash = sync.OnceValue(func() string {
	hash, err := hashPassword("tokenhub-dummy-password-verification")
	if err != nil {
		panic("failed to initialize password verification")
	}
	return hash
})

// consumePasswordVerification spends one bcrypt comparison. Hashing the input
// first also bounds the work for over-long passwords.
func consumePasswordVerification(password string) {
	_, _ = adminLoginPasswordVerifier(dummyPasswordHash(), HashSecret(password))
}

// verifyAdminLoginPassword compares the stored hash and pads the legacy path,
// whose constant-time compare is cheap and would otherwise reveal that the
// account exists or still holds an upgraded hash format.
func verifyAdminLoginPassword(hash, password string) (bool, bool) {
	valid, upgrade := adminLoginPasswordVerifier(hash, password)
	if !valid && !strings.HasPrefix(hash, "$2") && !strings.HasPrefix(hash, prehashedPasswordPrefix) {
		consumePasswordVerification(password)
	}
	return valid, upgrade
}
