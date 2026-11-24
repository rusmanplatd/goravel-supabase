package auth_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rusmanplatd/goravel-supabase/auth"
)

func makeRS256JWT(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	p, _ := json.Marshal(claims)
	si := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(si))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return si + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func makeES256JWT(t *testing.T, priv *ecdsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid})
	p, _ := json.Marshal(claims)
	si := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sum := sha256.Sum256([]byte(si))
	r, s, err := ecdsa.Sign(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return si + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func makeHS256JWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "HS256", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p) + ".fakesig"
}

func TestAuthPhase2PKCERingRecoveryAndSignOutCleanup(t *testing.T) {
	var seenChallenge bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/v1/recover":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["code_challenge"] != "" && body["code_challenge_method"] == "s256" {
				seenChallenge = true
			}
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "pkce":
			_, _ = w.Write([]byte(`{"access_token":"at-rec","refresh_token":"rt-rec","expires_in":3600,"user":{"id":"u1"}}`))
		case r.URL.Path == "/auth/v1/logout":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"server down"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := auth.New(srv.URL+"/auth/v1", auth.WithHTTPClient(srv.Client()), auth.WithFlowType(auth.FlowPKCE))
	ctx := context.Background()

	// Missing verifier fails fast with AuthPKCECodeVerifierMissingError
	_, err := c.ExchangeCodeForSession(ctx, "code1", "nonexistent")
	var missingErr *auth.AuthPKCECodeVerifierMissingError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected AuthPKCECodeVerifierMissingError, got %v", err)
	}

	// ResetPasswordForEmail in PKCE mode stores /recovery marker -> emits PASSWORD_RECOVERY
	var gotEvents []auth.AuthChangeEvent
	var evMu sync.Mutex
	sub := c.OnAuthStateChange(func(ev auth.AuthChangeEvent, _ *auth.Session) {
		evMu.Lock()
		gotEvents = append(gotEvents, ev)
		evMu.Unlock()
	})
	defer sub.Unsubscribe()

	if err := c.ResetPasswordForEmail(ctx, "a@b.com"); err != nil || !seenChallenge {
		t.Fatalf("ResetPasswordForEmail err=%v seenChallenge=%v", err, seenChallenge)
	}
	if _, err := c.ExchangeCodeForSession(ctx, "rec-code"); err != nil {
		t.Fatalf("ExchangeCodeForSession recovery: %v", err)
	}
	evMu.Lock()
	hasRecovery := false
	for _, ev := range gotEvents {
		if ev == auth.EventPasswordRecovery {
			hasRecovery = true
		}
	}
	evMu.Unlock()
	if !hasRecovery {
		t.Fatalf("expected EventPasswordRecovery, got %v", gotEvents)
	}

	// 5-slot PKCE ring: 6th concurrent flow evicts 1st
	for i := 1; i <= 6; i++ {
		if _, err := c.SignInWithOAuth(ctx, auth.SignInWithOAuthParams{
			Provider: "github",
			FlowID:   "f" + string(rune('0'+i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ExchangeCodeForSession(ctx, "c1", "f1"); !errors.As(err, &missingErr) {
		t.Fatalf("expected f1 evicted from 5-slot ring, got %v", err)
	}
	if _, err := c.ExchangeCodeForSession(ctx, "c6", "f6"); err != nil {
		t.Fatalf("expected f6 present in 5-slot ring, got %v", err)
	}

	// SignOut(ScopeLocal) clears session & PKCE verifiers even when /logout fails with 500
	if err := c.SignOut(ctx, auth.ScopeLocal); err != nil {
		t.Fatalf("ScopeLocal SignOut should succeed locally even on 500: %v", err)
	}
	if _, err := c.ExchangeCodeForSession(ctx, "c2", "f2"); !errors.As(err, &missingErr) {
		t.Fatalf("expected PKCE verifiers cleared on SignOut, got %v", err)
	}
}

func TestAuthPhase2SingleFlightRefreshEpochCooldownAndContextLock(t *testing.T) {
	// Context-aware MutexLock
	lock := auth.NewMutexLock()
	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = lock.Acquire(context.Background(), "k", func(context.Context) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	tctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := lock.Acquire(tctx, "k", func(context.Context) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded on MutexLock, got %v", err)
	}
	close(release)

	// Single-flight refresh deduplication
	var refreshCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "refresh_token" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			refreshCalls.Add(1)
			time.Sleep(40 * time.Millisecond)
			if body["refresh_token"] == "bad-rt" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error_code":"refresh_token_not_found","msg":"Invalid Refresh Token"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"new-at","refresh_token":"new-rt","expires_in":3600,"user":{"id":"u1"}}`))
			return
		}
		if r.URL.Path == "/auth/v1/logout" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := auth.New(srv.URL+"/auth/v1", auth.WithHTTPClient(srv.Client()))
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.RefreshSession(context.Background(), "rt-shared")
			if err != nil || res.Session.AccessToken != "new-at" {
				t.Errorf("concurrent RefreshSession = %+v, %v", res, err)
			}
		}()
	}
	wg.Wait()
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected 1 single-flight HTTP call, got %d", refreshCalls.Load())
	}

	// SignOut mid-refresh discards in-flight refresh
	refreshErrCh := make(chan error, 1)
	go func() {
		_, err := c.RefreshSession(context.Background(), "rt-race")
		refreshErrCh <- err
	}()
	time.Sleep(10 * time.Millisecond)
	_ = c.SignOut(context.Background(), auth.ScopeLocal)
	var discErr *auth.AuthRefreshDiscardedError
	if err := <-refreshErrCh; !errors.As(err, &discErr) {
		t.Fatalf("expected AuthRefreshDiscardedError, got %v", err)
	}

	// Failure cooldown prevents hammering permanently rejected refresh token
	before := refreshCalls.Load()
	_, err1 := c.RefreshSession(context.Background(), "bad-rt")
	_, err2 := c.RefreshSession(context.Background(), "bad-rt")
	if err1 == nil || err2 == nil {
		t.Fatalf("expected errors for bad-rt: %v, %v", err1, err2)
	}
	if refreshCalls.Load() != before+1 {
		t.Fatalf("expected cooldown to prevent second HTTP call, calls=%d", refreshCalls.Load()-before)
	}
}

func TestAuthPhase2JWKSAlgorithmsFallbackErrorsMFAAndAdminPagination(t *testing.T) {
	rsaPriv, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	rsJWT := makeRS256JWT(t, rsaPriv, "rsa1", map[string]any{"sub": "u-rsa", "aal": "aal1", "exp": time.Now().Add(time.Hour).Unix()})
	esJWT := makeES256JWT(t, ecPriv, "ec1", map[string]any{"sub": "u-ec", "exp": time.Now().Add(time.Hour).Unix()})
	expiredES := makeES256JWT(t, ecPriv, "ec1", map[string]any{"sub": "u-ec", "exp": time.Now().Add(-time.Hour).Unix()})
	hsJWT := makeHS256JWT(t, map[string]any{"sub": "u-hs", "exp": time.Now().Add(time.Hour).Unix()})

	xBytes := make([]byte, 32)
	yBytes := make([]byte, 32)
	ecPriv.X.FillBytes(xBytes)
	ecPriv.Y.FillBytes(yBytes)
	eBytes := big.NewInt(int64(rsaPriv.E)).Bytes()

	jwks := &auth.JWKS{
		Keys: []auth.JWK{
			{Kty: "RSA", Kid: "rsa1", Alg: "RS256", N: base64.RawURLEncoding.EncodeToString(rsaPriv.N.Bytes()), E: base64.RawURLEncoding.EncodeToString(eBytes)},
			{Kty: "EC", Kid: "ec1", Alg: "ES256", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(xBytes), Y: base64.RawURLEncoding.EncodeToString(yBytes)},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/v1/.well-known/jwks.json":
			_ = json.NewEncoder(w).Encode(jwks)
		case r.URL.Path == "/auth/v1/user":
			_, _ = w.Write([]byte(`{"id":"u1","factors":[{"id":"rc1","factor_type":"recovery_code","status":"verified"}]}`))
		case r.URL.Path == "/auth/v1/factors":
			_, _ = w.Write([]byte(`{"id":"f1","type":"totp","totp":{"qr_code":"<svg></svg>"}}`))
		case r.URL.Path == "/auth/v1/factors/f1/challenge":
			_, _ = w.Write([]byte(`{"id":"ch1","type":"webauthn","webauthn":{"challenge":"abc"}}`))
		case r.URL.Path == "/auth/v1/factors/f1/verify":
			_, _ = w.Write([]byte(`{"access_token":"` + rsJWT + `","refresh_token":"rt1","expires_in":3600}`))
		case r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "id_token":
			_, _ = w.Write([]byte(`{"access_token":"` + rsJWT + `","refresh_token":"rt1","expires_in":3600}`))
		case r.URL.Path == "/auth/v1/signup":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error_code":"weak_password","msg":"weak","weak_password":{"reasons":["length"]}}`))
		case r.URL.Path == "/auth/v1/otp":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"msg":"bad gateway"}`))
		case r.URL.Path == "/auth/v1/admin/users":
			w.Header().Set("x-total-count", "42")
			w.Header().Set("Link", `<https://ex/admin/users?page=2>; rel="next", <https://ex/admin/users?page=5>; rel="last"`)
			_, _ = w.Write([]byte(`{"users":[{"id":"u1"}]}`))
		case r.URL.Path == "/auth/v1/admin/custom-providers":
			if r.URL.Query().Get("type") != "oidc" {
				t.Errorf("custom-providers type = %q, want oidc", r.URL.Query().Get("type"))
			}
			_, _ = w.Write([]byte(`{"providers":[{"identifier":"custom:okta"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := auth.New(srv.URL+"/auth/v1", auth.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// RS256 & ES256 JWKS verification + custom JWKS + AllowExpired + HS256 /user fallback
	if cl, err := c.GetClaims(ctx, rsJWT); err != nil || cl.Claims["sub"] != "u-rsa" {
		t.Fatalf("RS256 GetClaims = %+v, %v", cl, err)
	}
	if cl, err := c.GetClaimsWithOptions(ctx, auth.GetClaimsOptions{JWT: esJWT, JWKS: jwks}); err != nil || cl.Claims["sub"] != "u-ec" {
		t.Fatalf("ES256 custom JWKS GetClaims = %+v, %v", cl, err)
	}
	if _, err := c.GetClaims(ctx, expiredES); err == nil {
		t.Fatal("expected error for expired ES256 JWT")
	}
	if cl, err := c.GetClaimsWithOptions(ctx, auth.GetClaimsOptions{JWT: expiredES, AllowExpired: true, JWKS: jwks}); err != nil || cl.Claims["sub"] != "u-ec" {
		t.Fatalf("AllowExpired GetClaims = %+v, %v", cl, err)
	}
	if cl, err := c.GetClaims(ctx, hsJWT); err != nil || cl.Claims["sub"] != "u-hs" {
		t.Fatalf("HS256 /user fallback GetClaims = %+v, %v", cl, err)
	}

	// Typed error classes
	_, err := c.SignUp(ctx, auth.SignUpParams{Email: "a@b.com", Password: "123"})
	var weakErr *auth.AuthWeakPasswordError
	if !errors.As(err, &weakErr) || len(weakErr.Reasons) != 1 || weakErr.Reasons[0] != "length" {
		t.Fatalf("expected AuthWeakPasswordError, got %v", err)
	}
	err = c.SignInWithOtp(ctx, auth.SignInWithOtpParams{Email: "a@b.com"})
	var retryErr *auth.AuthRetryableFetchError
	if !errors.As(err, &retryErr) || retryErr.Status != 502 {
		t.Fatalf("expected AuthRetryableFetchError 502, got %v", err)
	}

	// MFA QR prefix, RecoveryCode bucket, AAL optional JWT, WebAuthn ceremony
	_, _ = c.SetSession(ctx, rsJWT, "rt1")
	enr, err := c.MFA.Enroll(ctx, auth.MFAEnrollParams{FactorType: "totp"})
	if err != nil || !strings.HasPrefix(enr.TOTP.QRCode, "data:image/svg+xml;utf-8,<svg>") {
		t.Fatalf("MFA.Enroll QR = %+v, %v", enr, err)
	}
	lf, err := c.MFA.ListFactors(ctx)
	if err != nil || len(lf.RecoveryCode) != 1 {
		t.Fatalf("MFA.ListFactors RecoveryCode = %+v, %v", lf, err)
	}
	aal, err := c.MFA.GetAuthenticatorAssuranceLevel(ctx, rsJWT)
	if err != nil || aal.CurrentLevel != "aal1" {
		t.Fatalf("AAL with explicit JWT = %+v, %v", aal, err)
	}
	if _, err := c.MFA.WebAuthn.Register(ctx, auth.MFAWebAuthnRegisterParams{}); !errors.Is(err, auth.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for nil ceremony, got %v", err)
	}
	if _, err := c.MFA.WebAuthn.Register(ctx, auth.MFAWebAuthnRegisterParams{
		FriendlyName: "YubiKey",
		RPID:         "example.com",
		Ceremony:     func(context.Context, map[string]any) (map[string]any, error) { return map[string]any{"id": "c1"}, nil },
	}); err != nil {
		t.Fatalf("MFA.WebAuthn.Register error: %v", err)
	}
	if _, err := c.LinkIdentityWithIdToken(ctx, auth.SignInWithIdTokenParams{Provider: "google", Token: "idtok"}); err != nil {
		t.Fatalf("LinkIdentityWithIdToken error: %v", err)
	}

	// Admin pagination + CustomProviders type filter
	users, err := c.Admin.ListUsers(ctx)
	if err != nil || users.Total != 42 || users.NextPage != 2 || users.LastPage != 5 {
		t.Fatalf("Admin.ListUsers pagination = %+v, %v", users, err)
	}
	if _, err := c.Admin.CustomProviders.ListProviders(ctx, "oidc"); err != nil {
		t.Fatalf("Admin.CustomProviders.ListProviders error: %v", err)
	}
}
