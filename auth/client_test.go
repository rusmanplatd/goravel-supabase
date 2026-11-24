package auth_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rusmanplatd/goravel-supabase/auth"
)

func makeEdDSAJWT(t *testing.T, priv ed25519.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": kid})
	p, _ := json.Marshal(claims)
	hEnc := base64.RawURLEncoding.EncodeToString(h)
	pEnc := base64.RawURLEncoding.EncodeToString(p)
	signingInput := hEnc + "." + pEnc
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestStorageLockAndParseCallbackURL(t *testing.T) {
	ctx := context.Background()
	st := auth.NewMemoryStorage()
	if err := st.SetItem(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetItem(ctx, "k")
	if got != "v" {
		t.Fatalf("got %q, want v", got)
	}
	_ = st.RemoveItem(ctx, "k")
	got, _ = st.GetItem(ctx, "k")
	if got != "" {
		t.Fatalf("expected empty after remove, got %q", got)
	}

	lock := auth.NewMutexLock()
	var counter int
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lock.Acquire(ctx, "same", func(ctx context.Context) error {
				counter++
				return nil
			})
		}()
	}
	wg.Wait()
	if counter != 10 {
		t.Fatalf("counter = %d, want 10", counter)
	}

	cb, err := auth.ParseCallbackURL("https://app.example.com/callback?code=abc123&flow_id=f1#access_token=at&refresh_token=rt&expires_in=3600")
	if err != nil {
		t.Fatal(err)
	}
	if cb.Code != "abc123" || cb.FlowID != "f1" || cb.AccessToken != "at" || cb.RefreshToken != "rt" || cb.ExpiresIn != 3600 {
		t.Fatalf("cb = %+v", cb)
	}
}

func TestConcurrentPKCEAndSignInFlows(t *testing.T) {
	var seenVerifiers []string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Supabase-Api-Version"); got != auth.DefaultAPIVersion {
			t.Errorf("%s %s X-Supabase-Api-Version = %q, want %q", r.Method, r.URL.Path, got, auth.DefaultAPIVersion)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "pkce":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			seenVerifiers = append(seenVerifiers, body["code_verifier"])
			mu.Unlock()
			_, _ = w.Write([]byte(`{"access_token":"pkce-at","refresh_token":"pkce-rt","expires_in":3600,"token_type":"bearer","user":{"id":"u1"}}`))
		case r.URL.Path == "/auth/v1/signup":
			_, _ = w.Write([]byte(`{"access_token":"su-at","refresh_token":"su-rt","expires_in":3600,"token_type":"bearer","user":{"id":"u2","is_anonymous":true}}`))
		case r.URL.Path == "/auth/v1/token":
			_, _ = w.Write([]byte(`{"access_token":"pw-at","refresh_token":"pw-rt","expires_in":3600,"token_type":"bearer","user":{"id":"u3"}}`))
		case r.URL.Path == "/auth/v1/verify":
			if got := r.URL.Query().Get("redirect_to"); got != "https://app.example.com/cb" {
				t.Errorf("VerifyOtp redirect_to query = %q, want https://app.example.com/cb", got)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, hasRedirect := body["redirect_to"]; hasRedirect {
				t.Errorf("VerifyOtp body must not include redirect_to: %v", body)
			}
			_, _ = w.Write([]byte(`{"access_token":"otp-at","refresh_token":"otp-rt","expires_in":3600,"token_type":"bearer","user":{"id":"u4"}}`))
		case r.URL.Path == "/auth/v1/sso":
			_, _ = w.Write([]byte(`{"url":"https://idp.example.com/sso"}`))
		case r.URL.Path == "/auth/v1/otp" || r.URL.Path == "/auth/v1/recover" || r.URL.Path == "/auth/v1/resend" || r.URL.Path == "/auth/v1/reauthenticate":
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/auth/v1/error":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error_code":"invalid_credentials","msg":"Invalid login credentials"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := auth.New(srv.URL+"/auth/v1", auth.WithHTTPClient(srv.Client()), auth.WithFlowType(auth.FlowPKCE))
	ctx := context.Background()

	// Concurrent PKCE flows store isolated verifiers
	o1, err := c.SignInWithOAuth(ctx, auth.SignInWithOAuthParams{Provider: "github", FlowID: "flow-1"})
	if err != nil || !strings.Contains(o1.URL, "code_challenge=") {
		t.Fatalf("o1 = %+v, err = %v", o1, err)
	}
	o2, err := c.SignInWithOAuth(ctx, auth.SignInWithOAuthParams{Provider: "google", FlowID: "flow-2"})
	if err != nil || !strings.Contains(o2.URL, "code_challenge=") {
		t.Fatalf("o2 = %+v, err = %v", o2, err)
	}

	if _, err := c.GetSessionFromURL(ctx, "https://app.test/cb?code=c1&flow_id=flow-1"); err != nil {
		t.Fatalf("exchange flow-1: %v", err)
	}
	if _, err := c.ExchangeCodeForSession(ctx, "c2", "flow-2"); err != nil {
		t.Fatalf("exchange flow-2: %v", err)
	}
	if len(seenVerifiers) != 2 || seenVerifiers[0] == "" || seenVerifiers[1] == "" || seenVerifiers[0] == seenVerifiers[1] {
		t.Fatalf("expected 2 distinct non-empty PKCE verifiers, got %v", seenVerifiers)
	}

	// Other flows
	if _, err := c.SignUp(ctx, auth.SignUpParams{Email: "a@b.com", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SignInWithPassword(ctx, auth.SignInWithPasswordParams{Email: "a@b.com", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SignInWithOtp(ctx, auth.SignInWithOtpParams{Email: "a@b.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.VerifyOtp(ctx, auth.VerifyOtpParams{Email: "a@b.com", Token: "123456", Type: "email", RedirectTo: "https://app.example.com/cb"}); err != nil {
		t.Fatal(err)
	}
	if sso, err := c.SignInWithSSO(ctx, auth.SignInWithSSOParams{Domain: "acme.com"}); err != nil || sso.URL == "" {
		t.Fatalf("sso = %+v, err = %v", sso, err)
	}
	if _, err := c.SignInWithIdToken(ctx, auth.SignInWithIdTokenParams{Provider: "google", Token: "idtok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SignInWithWeb3(ctx, auth.SignInWithWeb3Params{Chain: "ethereum", Message: "siwe", Signature: "0xsig"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SignInAnonymously(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.ResetPasswordForEmail(ctx, "a@b.com"); err != nil {
		t.Fatal(err)
	}
	if err := c.Resend(ctx, auth.ResendParams{Type: "signup", Email: "a@b.com"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Reauthenticate(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSessionJWKSAutoRefreshIdentitiesMFAAndOAuthPasskey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	aal1JWT := makeEdDSAJWT(t, priv, "k1", map[string]any{
		"sub": "u1",
		"aal": "aal1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	aal2JWT := makeEdDSAJWT(t, priv, "k1", map[string]any{
		"sub": "u1",
		"aal": "aal2",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/v1/.well-known/jwks.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{{
					"kty": "OKP", "crv": "Ed25519", "kid": "k1", "alg": "EdDSA",
					"x": base64.RawURLEncoding.EncodeToString(pub),
				}},
			})
		case r.URL.Path == "/auth/v1/user" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"u1","email":"a@b.com","identities":[{"id":"i1","identity_id":"id1","provider":"github"}],"factors":[{"id":"f1","factor_type":"totp","status":"verified"}]}`))
		case r.URL.Path == "/auth/v1/user" && r.Method == http.MethodPut:
			_, _ = w.Write([]byte(`{"id":"u1","email":"new@b.com"}`))
		case r.URL.Path == "/auth/v1/token" && r.URL.Query().Get("grant_type") == "refresh_token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": aal1JWT, "refresh_token": "rt2", "expires_in": 3600, "token_type": "bearer",
				"user": map[string]any{"id": "u1"},
			})
		case r.URL.Path == "/auth/v1/logout":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/user/identities/authorize":
			_, _ = w.Write([]byte(`{"url":"https://github.com/login/oauth"}`))
		case strings.HasPrefix(r.URL.Path, "/auth/v1/user/identities/"):
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/factors":
			_, _ = w.Write([]byte(`{"id":"f1","type":"totp"}`))
		case r.URL.Path == "/auth/v1/factors/f1/challenge":
			_, _ = w.Write([]byte(`{"id":"ch1","type":"totp"}`))
		case r.URL.Path == "/auth/v1/factors/f1/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": aal2JWT, "refresh_token": "rt-aal2", "expires_in": 3600, "token_type": "bearer",
				"user": map[string]any{"id": "u1"},
			})
		case r.URL.Path == "/auth/v1/factors/f1" && r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"id":"f1"}`))
		case r.URL.Path == "/auth/v1/factors/recovery-codes" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"codes":["code1","code2"],"enabled":true}`))
		case r.URL.Path == "/auth/v1/factors/recovery-codes" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"enabled":true,"remaining":2,"total":2}`))
		case r.URL.Path == "/auth/v1/factors/recovery-codes/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": aal2JWT, "refresh_token": "rt-rc", "expires_in": 3600})
		case r.URL.Path == "/auth/v1/factors/recovery-codes/regenerate":
			_, _ = w.Write([]byte(`{"codes":["code3","code4"]}`))
		case r.URL.Path == "/auth/v1/factors/recovery-codes" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/oauth/authorizations/az1":
			_, _ = w.Write([]byte(`{"authorization_id":"az1","scope":"openid"}`))
		case r.URL.Path == "/auth/v1/oauth/authorizations/az1/consent":
			_, _ = w.Write([]byte(`{"redirect_url":"https://client.test/cb"}`))
		case r.URL.Path == "/auth/v1/user/oauth/grants" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"scopes":["openid"]}]`))
		case r.URL.Path == "/auth/v1/user/oauth/grants" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/passkeys/registration/options":
			_, _ = w.Write([]byte(`{"challenge_id":"rc1","options":{"challenge":"abc"}}`))
		case r.URL.Path == "/auth/v1/passkeys/registration/verify":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["credential"]; !ok {
				t.Errorf("passkey registration verify missing 'credential': %v", body)
			}
			if _, bad := body["credential_response"]; bad {
				t.Errorf("passkey registration verify must not send 'credential_response': %v", body)
			}
			if _, bad := body["friendly_name"]; bad {
				t.Errorf("passkey registration verify must not send 'friendly_name': %v", body)
			}
			_, _ = w.Write([]byte(`{"id":"pk1","friendly_name":"MacBook"}`))
		case r.URL.Path == "/auth/v1/passkeys/authentication/options":
			_, _ = w.Write([]byte(`{"challenge_id":"ac1","options":{"challenge":"def"}}`))
		case r.URL.Path == "/auth/v1/passkeys/authentication/verify":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["credential"]; !ok {
				t.Errorf("passkey auth verify missing 'credential': %v", body)
			}
			if _, bad := body["credential_response"]; bad {
				t.Errorf("passkey auth verify must not send 'credential_response': %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": aal1JWT, "refresh_token": "rt-pk", "expires_in": 3600})
		case r.URL.Path == "/auth/v1/passkeys" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"pk1","friendly_name":"MacBook"}]`))
		case r.URL.Path == "/auth/v1/passkeys/pk1" && r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{"id":"pk1","friendly_name":"Renamed"}`))
		case r.URL.Path == "/auth/v1/passkeys/pk1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := auth.New(srv.URL+"/auth/v1",
		auth.WithHTTPClient(srv.Client()),
		auth.WithAutoRefreshInterval(10*time.Millisecond),
	)
	ctx := context.Background()

	var events []auth.AuthChangeEvent
	var evMu sync.Mutex
	sub := c.OnAuthStateChange(func(ev auth.AuthChangeEvent, _ *auth.Session) {
		evMu.Lock()
		events = append(events, ev)
		evMu.Unlock()
	})
	defer sub.Unsubscribe()

	if _, err := c.SetSession(ctx, aal1JWT, "rt1"); err != nil {
		t.Fatal(err)
	}

	// JWKS verification
	claimsResp, err := c.GetClaims(ctx)
	if err != nil {
		t.Fatalf("GetClaims failed: %v", err)
	}
	if claimsResp.Claims["sub"] != "u1" {
		t.Fatalf("claims = %+v", claimsResp.Claims)
	}

	// Auto-refresh start/stop race safety
	c.StartAutoRefresh()
	time.Sleep(25 * time.Millisecond)
	c.StopAutoRefresh()

	// Identities & UpdateUser
	if _, err := c.UpdateUser(ctx, auth.UpdateUserParams{Email: "new@b.com"}); err != nil {
		t.Fatal(err)
	}
	if ids, err := c.GetUserIdentities(ctx); err != nil || len(ids) != 1 {
		t.Fatalf("identities = %+v, err = %v", ids, err)
	}
	if link, err := c.LinkIdentity(ctx, auth.SignInWithOAuthParams{Provider: "github"}); err != nil || link.URL == "" {
		t.Fatalf("link = %+v, err = %v", link, err)
	}
	if err := c.UnlinkIdentity(ctx, auth.UserIdentity{IdentityID: "id1"}); err != nil {
		t.Fatal(err)
	}

	// MFA + AAL + Recovery codes
	if _, err := c.MFA.Enroll(ctx, auth.MFAEnrollParams{FactorType: "totp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MFA.ChallengeAndVerify(ctx, auth.MFAChallengeAndVerifyParams{FactorID: "f1", Code: "123456"}); err != nil {
		t.Fatal(err)
	}
	aal, err := c.MFA.GetAuthenticatorAssuranceLevel(ctx)
	if err != nil || aal.CurrentLevel != "aal2" {
		t.Fatalf("aal = %+v, err = %v", aal, err)
	}
	if _, err := c.MFA.ListFactors(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MFA.Unenroll(ctx, "f1"); err != nil {
		t.Fatal(err)
	}
	if rc, err := c.MFA.RecoveryCodes.Generate(ctx); err != nil || len(rc.Codes) != 2 {
		t.Fatalf("rc generate = %+v, err = %v", rc, err)
	}
	if st, err := c.MFA.RecoveryCodes.GetStatus(ctx); err != nil || !st.Enabled {
		t.Fatalf("rc status = %+v, err = %v", st, err)
	}
	if _, err := c.MFA.RecoveryCodes.Verify(ctx, "code1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MFA.RecoveryCodes.Regenerate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MFA.RecoveryCodes.Unenroll(ctx); err != nil {
		t.Fatal(err)
	}

	// OAuth Server
	if _, err := c.OAuth.GetAuthorizationDetails(ctx, "az1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OAuth.ApproveAuthorization(ctx, "az1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OAuth.DenyAuthorization(ctx, "az1"); err != nil {
		t.Fatal(err)
	}
	if grants, err := c.OAuth.ListGrants(ctx); err != nil || len(grants) != 1 {
		t.Fatalf("grants = %+v, err = %v", grants, err)
	}
	if err := c.OAuth.RevokeGrant(ctx, "cid1"); err != nil {
		t.Fatal(err)
	}

	// Passkey two-step + ErrUnsupported guard
	if _, err := c.Passkey.RegisterPasskey(ctx, "MyKey", nil); !errors.Is(err, auth.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if _, err := c.Passkey.SignInWithPasskey(ctx, nil); !errors.Is(err, auth.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
	if _, err := c.Passkey.RegistrationOptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Passkey.RegistrationVerify(ctx, auth.PasskeyRegistrationVerifyParams{ChallengeID: "rc1", Credential: map[string]any{"id": "cred1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Passkey.AuthenticationOptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Passkey.AuthenticationVerify(ctx, auth.PasskeyAuthenticationVerifyParams{ChallengeID: "ac1", Credential: map[string]any{"id": "cred1"}}); err != nil {
		t.Fatal(err)
	}
	if pks, err := c.Passkey.List(ctx); err != nil || len(pks) != 1 {
		t.Fatalf("pks = %+v, err = %v", pks, err)
	}
	if _, err := c.Passkey.Update(ctx, "pk1", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := c.Passkey.Delete(ctx, "pk1"); err != nil {
		t.Fatal(err)
	}

	// SignOut
	if err := c.SignOut(ctx, auth.ScopeGlobal); err != nil {
		t.Fatal(err)
	}
}

func TestAdminAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/auth/v1/admin/users" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"u1","email":"admin@test.com"}`))
		case r.URL.Path == "/auth/v1/admin/users" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"users":[{"id":"u1"}]}`))
		case r.URL.Path == "/auth/v1/admin/users/u1" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
			_, _ = w.Write([]byte(`{"id":"u1","email":"updated@test.com"}`))
		case r.URL.Path == "/auth/v1/admin/users/u1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/invite":
			_, _ = w.Write([]byte(`{"id":"u2","email":"inv@test.com"}`))
		case r.URL.Path == "/auth/v1/admin/generate_link":
			_, _ = w.Write([]byte(`{"id":"u1","email":"a@b.com","action_link":"https://ex/verify","email_otp":"123456","hashed_token":"ht1","redirect_to":"https://ex/cb","verification_type":"magiclink"}`))
		case r.URL.Path == "/auth/v1/logout":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/admin/users/u1/factors":
			_, _ = w.Write([]byte(`[{"id":"f1","factor_type":"totp"}]`))
		case r.URL.Path == "/auth/v1/admin/users/u1/factors/f1":
			_, _ = w.Write([]byte(`{"id":"f1"}`))
		case r.URL.Path == "/auth/v1/admin/oauth/clients" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"clients":[{"client_id":"c1"}]}`))
		case r.URL.Path == "/auth/v1/admin/oauth/clients" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"client_id":"c1","client_secret":"sec"}`))
		case r.URL.Path == "/auth/v1/admin/oauth/clients/c1" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
			_, _ = w.Write([]byte(`{"client_id":"c1"}`))
		case r.URL.Path == "/auth/v1/admin/oauth/clients/c1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/admin/oauth/clients/c1/regenerate_secret":
			_, _ = w.Write([]byte(`{"client_id":"c1","client_secret":"newsec"}`))
		case r.URL.Path == "/auth/v1/admin/users/u1/passkeys":
			_, _ = w.Write([]byte(`[{"id":"pk1"}]`))
		case r.URL.Path == "/auth/v1/admin/users/u1/passkeys/pk1":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/auth/v1/admin/custom-providers" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"providers":[{"identifier":"custom:okta"}]}`))
		case r.URL.Path == "/auth/v1/admin/custom-providers" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"identifier":"custom:okta"}`))
		case r.URL.Path == "/auth/v1/admin/custom-providers/custom:okta" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
			_, _ = w.Write([]byte(`{"identifier":"custom:okta"}`))
		case r.URL.Path == "/auth/v1/admin/custom-providers/custom:okta" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := auth.New(srv.URL+"/auth/v1", auth.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	if _, err := c.Admin.CreateUser(ctx, auth.AdminCreateUserParams{Email: "admin@test.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.GetUserById(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if list, err := c.Admin.ListUsers(ctx, auth.AdminListUsersParams{Page: 1, PerPage: 10}); err != nil || len(list.Users) != 1 {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if _, err := c.Admin.UpdateUserById(ctx, "u1", auth.AdminUpdateUserParams{Email: "updated@test.com"}); err != nil {
		t.Fatal(err)
	}
	if delUser, err := c.Admin.DeleteUser(ctx, "u1"); err != nil || delUser == nil || delUser.ID != "u1" {
		t.Fatalf("DeleteUser = %+v, err = %v", delUser, err)
	}
	if _, err := c.Admin.InviteUserByEmail(ctx, "inv@test.com", nil); err != nil {
		t.Fatal(err)
	}
	if linkResp, err := c.Admin.GenerateLink(ctx, auth.GenerateLinkParams{Type: "magiclink", Email: "a@b.com"}); err != nil || linkResp.User == nil || linkResp.User.ID != "u1" || linkResp.ActionLink != "https://ex/verify" {
		t.Fatalf("GenerateLink = %+v, err = %v", linkResp, err)
	}
	if err := c.Admin.SignOut(ctx, "user-jwt"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.MFA.ListFactors(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.MFA.DeleteFactor(ctx, "u1", "f1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.OAuth.ListClients(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.OAuth.CreateClient(ctx, map[string]any{"client_name": "app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.OAuth.GetClient(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.OAuth.UpdateClient(ctx, "c1", map[string]any{"client_name": "app2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.OAuth.RegenerateClientSecret(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Admin.OAuth.DeleteClient(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.Passkey.ListPasskeys(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Admin.Passkey.DeletePasskey(ctx, "u1", "pk1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.ListProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.CreateProvider(ctx, map[string]any{"identifier": "custom:okta"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.GetProvider(ctx, "custom:okta"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Admin.UpdateProvider(ctx, "custom:okta", map[string]any{"name": "Okta"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Admin.DeleteProvider(ctx, "custom:okta"); err != nil {
		t.Fatal(err)
	}
}
