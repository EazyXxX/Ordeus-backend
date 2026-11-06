package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	cookieName      = "ordeus_sessions"
	tokenBytes      = 32
	tokenSeparator  = "."
	sessionLifetime = 30 * 24 * time.Hour
	// One browser can stay signed in to several accounts. The first token in
	// the cookie is the active account.
	maxDeviceAccounts = 8
)

func newToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// tokensFrom reads the cookie, ignoring anything that is not a token we issued.
func tokensFrom(r *http.Request) []string {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	tokens := make([]string, 0, maxDeviceAccounts)
	for _, token := range strings.Split(cookie.Value, tokenSeparator) {
		raw, err := hex.DecodeString(token)
		if err == nil && len(raw) == tokenBytes && !slices.Contains(tokens, token) {
			tokens = append(tokens, token)
		}
		if len(tokens) == maxDeviceAccounts {
			break
		}
	}
	return tokens
}

func (s *Service) writeTokens(w http.ResponseWriter, tokens []string) {
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    strings.Join(tokens, tokenSeparator),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionLifetime / time.Second),
	}
	if len(tokens) == 0 {
		cookie.MaxAge = -1
	}
	http.SetCookie(w, cookie)
}
