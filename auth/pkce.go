package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
)

// CallbackParams represents authentication parameters extracted from a redirect/callback URL.
type CallbackParams struct {
	Code             string
	AccessToken      string
	RefreshToken     string
	ProviderToken    string
	TokenType        string
	ExpiresIn        int64
	Type             string
	Error            string
	ErrorCode        string
	ErrorDescription string
	FlowID           string
}

// ParseCallbackURL extracts OAuth/PKCE/implicit callback parameters from a URL's query string
// and/or fragment without requiring any browser DOM (`window.location`).
func ParseCallbackURL(rawURL string) (*CallbackParams, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	merged := url.Values{}
	for k, vals := range u.Query() {
		for _, v := range vals {
			merged.Add(k, v)
		}
	}
	if u.Fragment != "" {
		if fragVals, err := url.ParseQuery(strings.TrimPrefix(u.Fragment, "#")); err == nil {
			for k, vals := range fragVals {
				for _, v := range vals {
					if merged.Get(k) == "" {
						merged.Set(k, v)
					}
				}
			}
		}
	}

	var exp int64
	if rawExp := merged.Get("expires_in"); rawExp != "" {
		exp, _ = strconv.ParseInt(rawExp, 10, 64)
	}

	return &CallbackParams{
		Code:             merged.Get("code"),
		AccessToken:      merged.Get("access_token"),
		RefreshToken:     merged.Get("refresh_token"),
		ProviderToken:    merged.Get("provider_token"),
		TokenType:        merged.Get("token_type"),
		ExpiresIn:        exp,
		Type:             merged.Get("type"),
		Error:            merged.Get("error"),
		ErrorCode:        merged.Get("error_code"),
		ErrorDescription: merged.Get("error_description"),
		FlowID:           merged.Get("flow_id"),
	}, nil
}

// GeneratePKCEVerifier creates a cryptographically random RFC 7636 PKCE code verifier.
func GeneratePKCEVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GeneratePKCEChallenge computes the S256 code challenge for a PKCE code verifier.
func GeneratePKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func pkceStorageKey(storageKey, flowID string) string {
	base := storageKey + "-code-verifier"
	if flowID != "" {
		return base + ":" + flowID
	}
	return base
}
