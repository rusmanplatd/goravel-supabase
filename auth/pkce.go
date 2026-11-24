package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// PKCEMaxConcurrentFlows is the maximum number of concurrent PKCE verifier slots retained per client.
const PKCEMaxConcurrentFlows = 5

// CallbackParams represents authentication parameters extracted from a redirect/callback URL.
type CallbackParams struct {
	Code                 string
	AccessToken          string
	RefreshToken         string
	ProviderToken        string
	ProviderRefreshToken string
	TokenType            string
	ExpiresIn            int64
	Type                 string
	Error                string
	ErrorCode            string
	ErrorDescription     string
	FlowID               string
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

	flowID := merged.Get("sb_flow_id")
	if flowID == "" {
		flowID = merged.Get("flow_id")
	}

	return &CallbackParams{
		Code:                 merged.Get("code"),
		AccessToken:          merged.Get("access_token"),
		RefreshToken:         merged.Get("refresh_token"),
		ProviderToken:        merged.Get("provider_token"),
		ProviderRefreshToken: merged.Get("provider_refresh_token"),
		TokenType:            merged.Get("token_type"),
		ExpiresIn:            exp,
		Type:                 merged.Get("type"),
		Error:                merged.Get("error"),
		ErrorCode:            merged.Get("error_code"),
		ErrorDescription:     merged.Get("error_description"),
		FlowID:               flowID,
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

func pkceRingIndexKey(storageKey string) string {
	return storageKey + "-pkce-ring"
}

func (c *Client) storePKCEVerifier(ctx context.Context, flowID, verifier string, isRecovery bool) (string, error) {
	stored := verifier
	if isRecovery {
		stored += "/recovery"
	}
	key := pkceStorageKey(c.storageKey, flowID)
	if c.storage == nil {
		return key, nil
	}
	if err := c.storage.SetItem(ctx, key, stored); err != nil {
		return "", err
	}
	// Maintain bounded 5-slot ring for concurrent flows
	if flowID != "" {
		ringKey := pkceRingIndexKey(c.storageKey)
		var ring []string
		if raw, err := c.storage.GetItem(ctx, ringKey); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &ring)
		}
		// Deduplicate if flowID already in ring
		filtered := ring[:0]
		for _, id := range ring {
			if id != flowID {
				filtered = append(filtered, id)
			}
		}
		filtered = append(filtered, flowID)
		for len(filtered) > PKCEMaxConcurrentFlows {
			evicted := filtered[0]
			filtered = filtered[1:]
			_ = c.storage.RemoveItem(ctx, pkceStorageKey(c.storageKey, evicted))
		}
		if b, err := json.Marshal(filtered); err == nil {
			_ = c.storage.SetItem(ctx, ringKey, string(b))
		}
	}
	return key, nil
}

func (c *Client) removeAllPKCEVerifiers(ctx context.Context) {
	if c.storage == nil {
		return
	}
	_ = c.storage.RemoveItem(ctx, pkceStorageKey(c.storageKey, ""))
	ringKey := pkceRingIndexKey(c.storageKey)
	if raw, err := c.storage.GetItem(ctx, ringKey); err == nil && raw != "" {
		var ring []string
		if json.Unmarshal([]byte(raw), &ring) == nil {
			for _, id := range ring {
				_ = c.storage.RemoveItem(ctx, pkceStorageKey(c.storageKey, id))
			}
		}
	}
	_ = c.storage.RemoveItem(ctx, ringKey)
}
