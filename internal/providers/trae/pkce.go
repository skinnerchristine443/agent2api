package trae

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// pkceVerifier 返回一个全新的 RFC 7636 code verifier。
func pkceVerifier() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// pkceChallenge 为某个 verifier 派生 S256 challenge。
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// pkcePair 返回一个全新的 PKCE verifier 及其 S256 challenge。该 verifier
// 会被携带穿过浏览器往返流程，并在 v3 code 交换时重放。
func pkcePair() (verifier, challenge string, err error) {
	verifier, err = pkceVerifier()
	if err != nil {
		return "", "", err
	}
	if verifier == "" {
		return "", "", errors.New("empty pkce verifier")
	}
	return verifier, pkceChallenge(verifier), nil
}
