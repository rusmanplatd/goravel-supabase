# Auth (`auth`)

The `auth` package implements a pure-Go Supabase GoTrue client covering password, OTP/magic link, OAuth/PKCE, identity linking, SSO, Web3, anonymous sign-in, MFA (TOTP, Phone, WebAuthn, recovery codes), Passkeys, OAuth Server consent/grants, JWKS claim verification, and Admin APIs.

- **Package:** `github.com/rusmanplatd/goravel-supabase/auth`
- **Access via Client:** `sb.Auth` (pure-Go) or `facades.Supabase().Auth()` (Goravel)

---

## 1. Email & Password Authentication

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
)

func main() {
	sb, err := client.New("https://xyzcompany.supabase.co", "sb_publishable_...")
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// Sign up with user metadata and email redirect
	signUpRes, err := sb.Auth.SignUp(ctx, auth.SignUpParams{
		Email:           "ada@example.com",
		Password:        "strong-password-123",
		EmailRedirectTo: "https://app.example.com/welcome",
		Data: map[string]any{
			"full_name": "Ada Lovelace",
			"plan":      "pro",
		},
	})
	if err != nil {
		var apiErr *auth.AuthApiError
		if errors.As(err, &apiErr) {
			log.Fatalf("SignUp failed (%d %s): %s", apiErr.Status, apiErr.Code, apiErr.Message)
		}
		log.Fatal(err)
	}
	fmt.Println("Signed up user ID:", signUpRes.User.ID)

	// Sign in with password
	signInRes, err := sb.Auth.SignInWithPassword(ctx, auth.SignInWithPasswordParams{
		Email:    "ada@example.com",
		Password: "strong-password-123",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Access token:", signInRes.Session.AccessToken)
}
```

---

## 2. Magic Link, OTP (Email & SMS), VerifyOtp, and Resend

```go
ctx := context.Background()

// 1. Send Email Magic Link / OTP
createUser := true
err := sb.Auth.SignInWithOtp(ctx, auth.SignInWithOtpParams{
	Email:            "ada@example.com",
	EmailRedirectTo:  "https://app.example.com/auth/callback",
	ShouldCreateUser: &createUser,
	Data:             map[string]any{"source": "magic_link"},
})
if err != nil {
	log.Fatal(err)
}

// 2. Send SMS OTP
err = sb.Auth.SignInWithOtp(ctx, auth.SignInWithOtpParams{
	Phone:   "+15551234567",
	Channel: "sms",
})

// 3. Resend confirmation or OTP
err = sb.Auth.Resend(ctx, auth.ResendParams{
	Type:            "signup",
	Email:           "ada@example.com",
	EmailRedirectTo: "https://app.example.com/auth/callback",
})

// 4. Verify OTP (or TokenHash from email link)
res, err := sb.Auth.VerifyOtp(ctx, auth.VerifyOtpParams{
	Email: "ada@example.com",
	Token: "123456",
	Type:  "email",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println("Verified user:", res.User.Email)
```

---

## 3. OAuth & PKCE Flow (Goravel Controller Example)

```go
package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/facades"
)

type OAuthController struct{}

// Redirect initiates the OAuth PKCE flow and redirects the browser to the provider.
func (c *OAuthController) Redirect(ctx http.Context) http.Response {
	sb := facades.Supabase()

	oauthRes, err := sb.Auth().SignInWithOAuth(ctx.Context(), auth.SignInWithOAuthParams{
		Provider:   "github",
		RedirectTo: "https://app.example.com/auth/callback",
		Scopes:     "read:user user:email",
		QueryParams: map[string]string{
			"allow_signup": "true",
		},
	})
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}

	return ctx.Response().Redirect(302, oauthRes.URL)
}

// Callback exchanges the authorization code (or full callback URL) for a Session.
func (c *OAuthController) Callback(ctx http.Context) http.Response {
	sb := facades.Supabase()
	code := ctx.Request().Query("code")

	authRes, err := sb.Auth().ExchangeCodeForSession(ctx.Context(), code)
	if err != nil {
		return ctx.Response().Json(400, http.Json{"error": err.Error()})
	}

	return ctx.Response().Success().Json(http.Json{
		"user":         authRes.User,
		"access_token": authRes.Session.AccessToken,
	})
}
```

You can also parse a raw callback URL directly using `auth.ParseCallbackURL(rawURL)` or `sb.Auth.GetSessionFromURL(ctx, rawURL)`.

---

## 4. Anonymous, OIDC ID Token, Enterprise SSO, and Web3

```go
ctx := context.Background()

// Anonymous sign-in
anonRes, err := sb.Auth.SignInAnonymously(ctx, auth.SignInAnonymouslyParams{
	Data: map[string]any{"guest": true},
})

// External OIDC ID token (Google, Apple, etc.)
idTokenRes, err := sb.Auth.SignInWithIdToken(ctx, auth.SignInWithIdTokenParams{
	Provider: "google",
	Token:    googleIDToken,
	Nonce:    rawNonce,
})

// Enterprise SAML/SSO by domain or provider ID
ssoRes, err := sb.Auth.SignInWithSSO(ctx, auth.SignInWithSSOParams{
	Domain:     "acme.com",
	RedirectTo: "https://app.example.com/sso/callback",
})
fmt.Println("Redirect user to SSO URL:", ssoRes.URL)

// Web3 (Ethereum SIWE or Solana)
web3Res, err := sb.Auth.SignInWithWeb3(ctx, auth.SignInWithWeb3Params{
	Chain:     "ethereum",
	Message:   siweMessage,
	Signature: hexSignature,
})
```

---

## 5. Identity Linking & Unlinking

```go
ctx := context.Background()

// List linked identities for the current user
identities, err := sb.Auth.GetUserIdentities(ctx)
if err != nil {
	log.Fatal(err)
}

// Link a new OAuth identity (redirects user to oauthRes.URL)
oauthRes, err := sb.Auth.LinkIdentity(ctx, auth.SignInWithOAuthParams{
	Provider:   "google",
	RedirectTo: "https://app.example.com/settings/linked",
})

// Or link directly using an OIDC ID token
_, err = sb.Auth.LinkIdentityWithIdToken(ctx, auth.SignInWithIdTokenParams{
	Provider: "apple",
	Token:    appleIDToken,
})

// Unlink an existing identity
if len(identities) > 1 {
	err = sb.Auth.UnlinkIdentity(ctx, identities[1])
}
```

---

## 6. Session, User, Password Recovery, JWKS Claims & Auth Listener

```go
ctx := context.Background()

// Listen to auth state changes
sub := sb.Auth.OnAuthStateChange(func(event auth.AuthChangeEvent, session *auth.Session) {
	switch event {
	case auth.EventInitialSession:
		fmt.Println("Initial session loaded")
	case auth.EventSignedIn:
		fmt.Println("User signed in:", session.User.Email)
	case auth.EventTokenRefreshed:
		fmt.Println("Token refreshed")
	case auth.EventUserUpdated:
		fmt.Println("User updated")
	case auth.EventPasswordRecovery:
		fmt.Println("Password recovery flow active")
	case auth.EventSignedOut:
		fmt.Println("User signed out")
	}
})
defer sub.Unsubscribe()

// Get current session (auto-refreshes if near expiry)
session, err := sb.Auth.GetSession(ctx)

// Manually set or refresh a session
_, err = sb.Auth.SetSession(ctx, accessToken, refreshToken)
_, err = sb.Auth.RefreshSession(ctx)

// Fetch user from GoTrue
user, err := sb.Auth.GetUser(ctx, accessToken)

// Verify & extract JWT claims locally via JWKS (or GetClaimsWithOptions)
claims, err := sb.Auth.GetClaims(ctx, accessToken)
claimsCustom, err := sb.Auth.GetClaimsWithOptions(ctx, auth.GetClaimsOptions{
	JWT:          accessToken,
	AllowExpired: false,
})
fmt.Println("Claims subject:", claims.Claims["sub"], claimsCustom != nil)

// Request password reset email & reauthenticate before sensitive updates
err = sb.Auth.ResetPasswordForEmail(ctx, "ada@example.com", auth.ResetPasswordOptions{
	RedirectTo: "https://app.example.com/reset-password",
})
err = sb.Auth.Reauthenticate(ctx)

// Update user email, password, or metadata
updatedUser, err := sb.Auth.UpdateUser(ctx, auth.UpdateUserParams{
	Password: "new-strong-password-456",
	Data:     map[string]any{"theme": "dark"},
})

// Sign out (ScopeLocal, ScopeGlobal, or ScopeOthers)
err = sb.Auth.SignOut(ctx, auth.ScopeLocal)
```

---

## 7. Multi-Factor Authentication (MFA, Recovery Codes & WebAuthn)

```go
ctx := context.Background()

// 1. Enroll a TOTP factor
enroll, err := sb.Auth.MFA.Enroll(ctx, auth.MFAEnrollParams{
	FactorType:   "totp",
	FriendlyName: "Authenticator App",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println("Scan QR:", enroll.TOTP.QRCode)

// 2. Challenge & verify in one call (or call Challenge + Verify separately)
session, err := sb.Auth.MFA.ChallengeAndVerify(ctx, auth.MFAChallengeAndVerifyParams{
	FactorID: enroll.ID,
	Code:     "123456",
})

// 3. List enrolled factors & check Authenticator Assurance Level (aal1/aal2)
factors, err := sb.Auth.MFA.ListFactors(ctx)
aal, err := sb.Auth.MFA.GetAuthenticatorAssuranceLevel(ctx)
fmt.Printf("Current AAL: %s, Next AAL: %s, TOTP count: %d\n",
	aal.CurrentLevel, aal.NextLevel, len(factors.TOTP))

// 4. Generate & verify MFA recovery codes
codes, err := sb.Auth.MFA.RecoveryCodes.Generate(ctx, "Backup Codes")
fmt.Println("Save these codes:", codes.RecoveryCodes)
_, err = sb.Auth.MFA.RecoveryCodes.Verify(ctx, codes.RecoveryCodes[0])

// 5. Unenroll a factor
_, err = sb.Auth.MFA.Unenroll(ctx, enroll.ID)
```

---

## 8. Passkeys & OAuth Server Consent

### Passkeys (Two-Step Server Flow)

```go
ctx := context.Background()

// Registration flow
regOpts, err := sb.Auth.Passkey.RegistrationOptions(ctx)
// Send regOpts to browser navigator.credentials.create(...), then verify:
passkey, err := sb.Auth.Passkey.RegistrationVerify(ctx, auth.PasskeyRegistrationVerifyParams{
	ChallengeID: regOpts.ChallengeID,
	FriendlyName: "MacBook Touch ID",
	Credential:  clientAttestationPayload,
})

// List, rename, and delete passkeys
list, err := sb.Auth.Passkey.List(ctx)
_, err = sb.Auth.Passkey.Update(ctx, passkey.ID, "Work MacBook")
err = sb.Auth.Passkey.Delete(ctx, passkey.ID)

// Authentication flow
authOpts, err := sb.Auth.Passkey.AuthenticationOptions(ctx)
authRes, err := sb.Auth.Passkey.AuthenticationVerify(ctx, auth.PasskeyAuthenticationVerifyParams{
	ChallengeID: authOpts.ChallengeID,
	Credential:  clientAssertionPayload,
})
```

### OAuth Server Consent & Grants

```go
ctx := context.Background()

details, err := sb.Auth.OAuth.GetAuthorizationDetails(ctx, authorizationID)
fmt.Println("Client requesting access:", details.Client.Name)

consent, err := sb.Auth.OAuth.ApproveAuthorization(ctx, authorizationID)
// Or deny: consent, err = sb.Auth.OAuth.DenyAuthorization(ctx, authorizationID)
fmt.Println("Redirect to:", consent.RedirectTo)

// Manage user's granted OAuth apps
grants, err := sb.Auth.OAuth.ListGrants(ctx)
if len(grants) > 0 {
	err = sb.Auth.OAuth.RevokeGrant(ctx, grants[0].Client.ID)
}
```

---

## 9. Admin API (Service Role)

Initialize a client with your `service_role` / secret key:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
)

func main() {
	adminSB, err := client.New(
		"https://xyzcompany.supabase.co",
		"sb_secret_...",
		client.WithPersistSession(false),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer adminSB.Close()

	ctx := context.Background()

	// Create user
	user, err := adminSB.Auth.Admin.CreateUser(ctx, auth.AdminCreateUserParams{
		Email:        "grace@example.com",
		Password:     "temp-password-123",
		EmailConfirm: true,
		UserMetadata: map[string]any{"name": "Grace Hopper"},
		AppMetadata:  map[string]any{"role": "admin"},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Update user by ID
	confirmed := true
	user, err = adminSB.Auth.Admin.UpdateUserById(ctx, user.ID, auth.AdminUpdateUserParams{
		EmailConfirm: &confirmed,
		AppMetadata:  map[string]any{"role": "superadmin"},
	})

	// List users with pagination
	page, perPage := 1, 25
	usersPage, err := adminSB.Auth.Admin.ListUsers(ctx, auth.AdminListUsersParams{
		Page:    &page,
		PerPage: &perPage,
	})
	fmt.Println("Total users:", usersPage.Total)

	// Invite user by email & generate auth links
	_, err = adminSB.Auth.Admin.InviteUserByEmail(ctx, "newbie@example.com", map[string]any{"team": "core"})
	link, err := adminSB.Auth.Admin.GenerateLink(ctx, auth.GenerateLinkParams{
		Type:  "magiclink",
		Email: "grace@example.com",
	})
	fmt.Println("Action link:", link.Properties.ActionLink)

	// Admin MFA, Passkey, OAuth Clients, and Custom Providers
	factors, err := adminSB.Auth.Admin.MFA.ListFactors(ctx, user.ID)
	passkeys, err := adminSB.Auth.Admin.Passkey.ListPasskeys(ctx, user.ID)
	oauthClients, err := adminSB.Auth.Admin.OAuth.ListClients(ctx)
	providers, err := adminSB.Auth.Admin.CustomProviders.ListProviders(ctx)
	_, _, _, _ = factors, passkeys, oauthClients, providers

	// Delete user
	_, err = adminSB.Auth.Admin.DeleteUser(ctx, user.ID)
}
```
