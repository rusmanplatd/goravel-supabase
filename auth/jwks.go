package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JWK represents a JSON Web Key used for local asymmetric JWT verification.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	// RSA
	N string `json:"n"`
	E string `json:"e"`
	// EC / OKP
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// JWKS represents a JSON Web Key Set document.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

type jwkKey = JWK
type jwksDocument = JWKS

type jwksCache struct {
	mu        sync.RWMutex
	keys      []jwkKey
	fetchedAt time.Time
	ttl       time.Duration
}

func (c *jwksCache) get(kid string) (jwkKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Since(c.fetchedAt) > c.ttl {
		return jwkKey{}, false
	}
	for _, k := range c.keys {
		if kid == "" || k.Kid == kid {
			return k, true
		}
	}
	return jwkKey{}, false
}

func (c *jwksCache) set(keys []jwkKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = keys
	c.fetchedAt = time.Now()
}

func parseJWTParts(token string) (header map[string]any, claims map[string]any, signingInput string, sig []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, "", nil, errors.New("auth: invalid JWT format")
	}
	hBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("auth: decode JWT header: %w", err)
	}
	pBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("auth: decode JWT claims: %w", err)
	}
	sig, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("auth: decode JWT signature: %w", err)
	}
	if err := json.Unmarshal(hBytes, &header); err != nil {
		return nil, nil, "", nil, err
	}
	if err := json.Unmarshal(pBytes, &claims); err != nil {
		return nil, nil, "", nil, err
	}
	return header, claims, parts[0] + "." + parts[1], sig, nil
}

func (c *Client) fetchJWKS(ctx context.Context) ([]jwkKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/.well-known/jwks.json", nil)
	if err != nil {
		return nil, err
	}
	c.applyDefaultHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeAuthError(raw, resp.StatusCode, resp.Status)
	}
	var doc jwksDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	c.jwks.set(doc.Keys)
	return doc.Keys, nil
}

func verifyAsymmetricJWT(alg string, key jwkKey, signingInput string, sig []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		pub, err := parseRSAJWK(key)
		if err != nil {
			return err
		}
		h, hf := hashForAlg(alg)
		hf.Write([]byte(signingInput))
		return rsa.VerifyPKCS1v15(pub, h, hf.Sum(nil), sig)
	case "ES256", "ES384", "ES512":
		pub, keySize, err := parseECJWK(key)
		if err != nil {
			return err
		}
		if len(sig) != 2*keySize {
			return errors.New("auth: invalid ECDSA signature length")
		}
		r := new(big.Int).SetBytes(sig[:keySize])
		s := new(big.Int).SetBytes(sig[keySize:])
		_, hf := hashForAlg(alg)
		hf.Write([]byte(signingInput))
		if !ecdsa.Verify(pub, hf.Sum(nil), r, s) {
			return errors.New("auth: invalid ECDSA signature")
		}
		return nil
	case "EdDSA":
		xBytes, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(xBytes) != ed25519.PublicKeySize {
			return errors.New("auth: invalid Ed25519 JWK")
		}
		if !ed25519.Verify(ed25519.PublicKey(xBytes), []byte(signingInput), sig) {
			return errors.New("auth: invalid EdDSA signature")
		}
		return nil
	default:
		return fmt.Errorf("auth: unsupported JWT alg %q", alg)
	}
}

func hashForAlg(alg string) (crypto.Hash, hash.Hash) {
	switch alg {
	case "RS384", "ES384":
		return crypto.SHA384, sha512.New384()
	case "RS512", "ES512":
		return crypto.SHA512, sha512.New()
	default:
		return crypto.SHA256, sha256.New()
	}
}

func parseRSAJWK(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(e.Int64()),
	}, nil
}

func parseECJWK(k jwkKey) (*ecdsa.PublicKey, int, error) {
	var curve elliptic.Curve
	var keySize int
	switch k.Crv {
	case "P-256":
		curve, keySize = elliptic.P256(), 32
	case "P-384":
		curve, keySize = elliptic.P384(), 48
	case "P-521":
		curve, keySize = elliptic.P521(), 66
	default:
		return nil, 0, fmt.Errorf("auth: unsupported EC curve %q", k.Crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, 0, err
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, 0, err
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}, keySize, nil
}
