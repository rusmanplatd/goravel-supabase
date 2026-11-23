package auth

import "time"

// FlowType selects implicit vs PKCE OAuth flow.
type FlowType string

const (
	FlowImplicit FlowType = "implicit"
	FlowPKCE     FlowType = "pkce"
)

// SignOutScope defines the scope of a sign-out operation.
type SignOutScope string

const (
	ScopeGlobal SignOutScope = "global"
	ScopeLocal  SignOutScope = "local"
	ScopeOthers SignOutScope = "others"
)

// AuthChangeEvent identifies an authentication lifecycle event.
type AuthChangeEvent string

const (
	EventInitialSession       AuthChangeEvent = "INITIAL_SESSION"
	EventSignedIn             AuthChangeEvent = "SIGNED_IN"
	EventSignedOut            AuthChangeEvent = "SIGNED_OUT"
	EventTokenRefreshed       AuthChangeEvent = "TOKEN_REFRESHED"
	EventUserUpdated          AuthChangeEvent = "USER_UPDATED"
	EventPasswordRecovery     AuthChangeEvent = "PASSWORD_RECOVERY"
	EventMFAChallengeVerified AuthChangeEvent = "MFA_CHALLENGE_VERIFIED"
)

// Session represents an authenticated GoTrue session.
type Session struct {
	ProviderToken        string `json:"provider_token,omitempty"`
	ProviderRefreshToken string `json:"provider_refresh_token,omitempty"`
	AccessToken          string `json:"access_token"`
	RefreshToken         string `json:"refresh_token"`
	ExpiresIn            int64  `json:"expires_in"`
	ExpiresAt            int64  `json:"expires_at,omitempty"`
	TokenType            string `json:"token_type"`
	User                 *User  `json:"user,omitempty"`
}

// IsExpired reports whether the session access token is expired or within margin.
func (s *Session) IsExpired(margin time.Duration) bool {
	if s == nil || s.ExpiresAt == 0 {
		return false
	}
	return time.Now().Add(margin).Unix() >= s.ExpiresAt
}

// User represents a GoTrue user account.
type User struct {
	ID               string         `json:"id"`
	Aud              string         `json:"aud,omitempty"`
	Role             string         `json:"role,omitempty"`
	Email            string         `json:"email,omitempty"`
	Phone            string         `json:"phone,omitempty"`
	EmailConfirmedAt string         `json:"email_confirmed_at,omitempty"`
	PhoneConfirmedAt string         `json:"phone_confirmed_at,omitempty"`
	ConfirmedAt      string         `json:"confirmed_at,omitempty"`
	LastSignInAt     string         `json:"last_sign_in_at,omitempty"`
	AppMetadata      map[string]any `json:"app_metadata,omitempty"`
	UserMetadata     map[string]any `json:"user_metadata,omitempty"`
	Identities       []UserIdentity `json:"identities,omitempty"`
	Factors          []Factor       `json:"factors,omitempty"`
	IsAnonymous      bool           `json:"is_anonymous,omitempty"`
	CreatedAt        string         `json:"created_at,omitempty"`
	UpdatedAt        string         `json:"updated_at,omitempty"`
}

// UserIdentity represents a linked external identity provider for a user.
type UserIdentity struct {
	ID           string         `json:"id"`
	UserID       string         `json:"user_id"`
	IdentityID   string         `json:"identity_id"`
	Provider     string         `json:"provider"`
	IdentityData map[string]any `json:"identity_data,omitempty"`
	LastSignInAt string         `json:"last_sign_in_at,omitempty"`
	CreatedAt    string         `json:"created_at,omitempty"`
	UpdatedAt    string         `json:"updated_at,omitempty"`
}

// Factor represents an enrolled MFA factor.
type Factor struct {
	ID           string `json:"id"`
	FriendlyName string `json:"friendly_name,omitempty"`
	FactorType   string `json:"factor_type"`
	Status       string `json:"status"`
	Phone        string `json:"phone,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

// AuthResponse wraps a User and optional Session returned by auth endpoints.
type AuthResponse struct {
	User    *User    `json:"user,omitempty"`
	Session *Session `json:"session,omitempty"`
}

// SignUpParams configures SignUp.
type SignUpParams struct {
	Email        string         `json:"email,omitempty"`
	Phone        string         `json:"phone,omitempty"`
	Password     string         `json:"password"`
	Channel      string         `json:"channel,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
	EmailRedirectTo string      `json:"-"`
	CaptchaToken string         `json:"-"`
}

// SignInWithPasswordParams configures SignInWithPassword.
type SignInWithPasswordParams struct {
	Email        string `json:"email,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Password     string `json:"password"`
	CaptchaToken string `json:"-"`
}

// SignInWithOtpParams configures SignInWithOtp.
type SignInWithOtpParams struct {
	Email           string         `json:"email,omitempty"`
	Phone           string         `json:"phone,omitempty"`
	Channel         string         `json:"channel,omitempty"`
	Data            map[string]any `json:"data,omitempty"`
	ShouldCreateUser *bool         `json:"create_user,omitempty"`
	EmailRedirectTo string         `json:"-"`
	CaptchaToken    string         `json:"-"`
}

// VerifyOtpParams configures VerifyOtp.
type VerifyOtpParams struct {
	Email        string `json:"email,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Token        string `json:"token,omitempty"`
	TokenHash    string `json:"token_hash,omitempty"`
	Type         string `json:"type"`
	RedirectTo   string `json:"redirect_to,omitempty"`
	CaptchaToken string `json:"-"`
}

// SignInWithOAuthParams configures SignInWithOAuth.
type SignInWithOAuthParams struct {
	Provider    string
	RedirectTo  string
	Scopes      string
	QueryParams map[string]string
	FlowID      string // optional concurrent PKCE flow key
}

// OAuthResponse is returned by SignInWithOAuth and LinkIdentity.
type OAuthResponse struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	FlowID   string `json:"flow_id,omitempty"`
}

// SignInWithSSOParams configures SignInWithSSO.
type SignInWithSSOParams struct {
	ProviderID   string `json:"provider_id,omitempty"`
	Domain       string `json:"domain,omitempty"`
	RedirectTo   string `json:"redirect_to,omitempty"`
	CaptchaToken string `json:"-"`
	FlowID       string `json:"-"`
}

// SSOResponse is returned by SignInWithSSO.
type SSOResponse struct {
	URL    string `json:"url"`
	FlowID string `json:"flow_id,omitempty"`
}

// SignInWithIdTokenParams configures SignInWithIdToken.
type SignInWithIdTokenParams struct {
	Provider     string `json:"provider"`
	Token        string `json:"id_token"`
	AccessToken  string `json:"access_token,omitempty"`
	Nonce        string `json:"nonce,omitempty"`
	CaptchaToken string `json:"-"`
}

// SignInWithWeb3Params configures SignInWithWeb3 (Ethereum or Solana).
type SignInWithWeb3Params struct {
	Chain        string `json:"chain"` // "ethereum" or "solana"
	Message      string `json:"message"`
	Signature    string `json:"signature"`
	CaptchaToken string `json:"-"`
}

// SignInAnonymouslyParams configures SignInAnonymously.
type SignInAnonymouslyParams struct {
	Data         map[string]any `json:"data,omitempty"`
	CaptchaToken string         `json:"-"`
}

// ResetPasswordOptions configures ResetPasswordForEmail.
type ResetPasswordOptions struct {
	RedirectTo   string
	CaptchaToken string
}

// ResendParams configures Resend.
type ResendParams struct {
	Type            string `json:"type"`
	Email           string `json:"email,omitempty"`
	Phone           string `json:"phone,omitempty"`
	EmailRedirectTo string `json:"-"`
	CaptchaToken    string `json:"-"`
}

// UpdateUserParams configures UpdateUser.
type UpdateUserParams struct {
	Email           string         `json:"email,omitempty"`
	Phone           string         `json:"phone,omitempty"`
	Password        string         `json:"password,omitempty"`
	Nonce           string         `json:"nonce,omitempty"`
	Data            map[string]any `json:"data,omitempty"`
	EmailRedirectTo string         `json:"-"`
}

// ClaimsResponse is returned by GetClaims.
type ClaimsResponse struct {
	Claims    map[string]any `json:"claims"`
	Header    map[string]any `json:"header"`
	Signature string         `json:"signature"`
}

// Subscription represents an active OnAuthStateChange listener.
type Subscription struct {
	ID          string
	Unsubscribe func()
}
