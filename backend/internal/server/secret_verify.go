package server

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
)

// SecretCiphertextPrefix marks values protected at rest with the store's
// AES-256-GCM scheme. Key-management and upgrade tooling uses it to locate
// ciphertext inside columns and inside JSON documents that embed protected
// values.
const SecretCiphertextPrefix = "enc:v1:"

// VerifySecretCiphertext reports whether value is a SecretCiphertextPrefix
// ciphertext that decrypts under secretKey. Values without the prefix carry
// no ciphertext and verify as true. A malformed or undecryptable value
// reports false instead of an error so callers can treat the check as a
// boolean gate; the store layer's decryptSecret swallows the same failures
// by returning an empty string.
func VerifySecretCiphertext(secretKey, value string) bool {
	if !strings.HasPrefix(value, SecretCiphertextPrefix) {
		return true
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, SecretCiphertextPrefix))
	if err != nil {
		return false
	}
	block, err := aes.NewCipher(secretKeyBytes(secretKey))
	if err != nil {
		return false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return false
	}
	if len(data) < gcm.NonceSize() {
		return false
	}
	_, err = gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	return err == nil
}
