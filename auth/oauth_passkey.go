package auth

import (
	"context"
	"net/http"
	"net/url"
)

// OAuthServerClient implements GoTrue OAuth Server user consent and grant APIs.
type OAuthServerClient struct {
	client *Client
}

type OAuthAuthorizationDetails struct {
	AuthorizationID string         `json:"authorization_id"`
	RedirectURL     string         `json:"redirect_url,omitempty"`
	Client          map[string]any `json:"client,omitempty"`
	User            map[string]any `json:"user,omitempty"`
	Scope           string         `json:"scope,omitempty"`
}

type OAuthConsentResponse struct {
	RedirectURL string `json:"redirect_url"`
}

type OAuthGrant struct {
	Client    map[string]any `json:"client,omitempty"`
	Scopes    []string       `json:"scopes,omitempty"`
	GrantedAt string         `json:"granted_at,omitempty"`
}

func (o *OAuthServerClient) GetAuthorizationDetails(ctx context.Context, authorizationID string) (*OAuthAuthorizationDetails, error) {
	token, err := o.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp OAuthAuthorizationDetails
	if err := o.client.doJSON(ctx, http.MethodGet, "/oauth/authorizations/"+url.PathEscape(authorizationID), nil, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (o *OAuthServerClient) ApproveAuthorization(ctx context.Context, authorizationID string) (*OAuthConsentResponse, error) {
	token, err := o.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp OAuthConsentResponse
	if err := o.client.doJSON(ctx, http.MethodPost, "/oauth/authorizations/"+url.PathEscape(authorizationID)+"/consent", map[string]any{
		"action": "approve",
	}, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (o *OAuthServerClient) DenyAuthorization(ctx context.Context, authorizationID string) (*OAuthConsentResponse, error) {
	token, err := o.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp OAuthConsentResponse
	if err := o.client.doJSON(ctx, http.MethodPost, "/oauth/authorizations/"+url.PathEscape(authorizationID)+"/consent", map[string]any{
		"action": "deny",
	}, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (o *OAuthServerClient) ListGrants(ctx context.Context) ([]OAuthGrant, error) {
	token, err := o.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var grants []OAuthGrant
	if err := o.client.doJSON(ctx, http.MethodGet, "/user/oauth/grants", nil, token, &grants); err != nil {
		return nil, err
	}
	return grants, nil
}

func (o *OAuthServerClient) RevokeGrant(ctx context.Context, clientID string) error {
	token, err := o.client.requireAccessToken(ctx)
	if err != nil {
		return err
	}
	return o.client.doJSON(ctx, http.MethodDelete, "/user/oauth/grants?client_id="+url.QueryEscape(clientID), nil, token, nil)
}

// PasskeyClient implements GoTrue Passkey / WebAuthn two-step server-usable HTTP APIs.
type PasskeyClient struct {
	client *Client
}

type Passkey struct {
	ID           string `json:"id"`
	FriendlyName string `json:"friendly_name,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
	LastUsedAt   string `json:"last_used_at,omitempty"`
}

type PasskeyRegistrationOptionsResponse struct {
	ChallengeID string         `json:"challenge_id"`
	Options     map[string]any `json:"options"`
}

type PasskeyRegistrationVerifyParams struct {
	ChallengeID string         `json:"challenge_id"`
	Credential  map[string]any `json:"credential"`
}

type PasskeyAuthenticationOptionsResponse struct {
	ChallengeID string         `json:"challenge_id"`
	Options     map[string]any `json:"options"`
}

type PasskeyAuthenticationVerifyParams struct {
	ChallengeID string         `json:"challenge_id"`
	Credential  map[string]any `json:"credential"`
}

func (p *PasskeyClient) RegistrationOptions(ctx context.Context) (*PasskeyRegistrationOptionsResponse, error) {
	token, err := p.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp PasskeyRegistrationOptionsResponse
	if err := p.client.doJSON(ctx, http.MethodPost, "/passkeys/registration/options", nil, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *PasskeyClient) RegistrationVerify(ctx context.Context, params PasskeyRegistrationVerifyParams) (*Passkey, error) {
	token, err := p.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp Passkey
	if err := p.client.doJSON(ctx, http.MethodPost, "/passkeys/registration/verify", params, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *PasskeyClient) AuthenticationOptions(ctx context.Context) (*PasskeyAuthenticationOptionsResponse, error) {
	var resp PasskeyAuthenticationOptionsResponse
	if err := p.client.doJSON(ctx, http.MethodPost, "/passkeys/authentication/options", nil, "", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *PasskeyClient) AuthenticationVerify(ctx context.Context, params PasskeyAuthenticationVerifyParams) (*AuthResponse, error) {
	var raw map[string]any
	if err := p.client.doJSON(ctx, http.MethodPost, "/passkeys/authentication/verify", params, "", &raw); err != nil {
		return nil, err
	}
	return p.client.decodeSessionOrUser(ctx, raw, EventSignedIn)
}

func (p *PasskeyClient) List(ctx context.Context) ([]Passkey, error) {
	token, err := p.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var passkeys []Passkey
	if err := p.client.doJSON(ctx, http.MethodGet, "/passkeys", nil, token, &passkeys); err != nil {
		return nil, err
	}
	return passkeys, nil
}

func (p *PasskeyClient) Update(ctx context.Context, passkeyID, friendlyName string) (*Passkey, error) {
	token, err := p.client.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp Passkey
	if err := p.client.doJSON(ctx, http.MethodPatch, "/passkeys/"+url.PathEscape(passkeyID), map[string]any{
		"friendly_name": friendlyName,
	}, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (p *PasskeyClient) Delete(ctx context.Context, passkeyID string) error {
	token, err := p.client.requireAccessToken(ctx)
	if err != nil {
		return err
	}
	return p.client.doJSON(ctx, http.MethodDelete, "/passkeys/"+url.PathEscape(passkeyID), nil, token, nil)
}

// RegisterPasskey wraps RegistrationOptions -> ceremony -> RegistrationVerify.
// Returns ErrUnsupported when ceremony is nil (no browser navigator.credentials DOM in Go).
func (p *PasskeyClient) RegisterPasskey(ctx context.Context, friendlyName string, ceremony func(ctx context.Context, opts map[string]any) (map[string]any, error)) (*Passkey, error) {
	if ceremony == nil {
		return nil, ErrUnsupported
	}
	opts, err := p.RegistrationOptions(ctx)
	if err != nil {
		return nil, err
	}
	cred, err := ceremony(ctx, opts.Options)
	if err != nil {
		return nil, err
	}
	return p.RegistrationVerify(ctx, PasskeyRegistrationVerifyParams{
		ChallengeID: opts.ChallengeID,
		Credential:  cred,
	})
}

// SignInWithPasskey wraps AuthenticationOptions -> ceremony -> AuthenticationVerify.
// Returns ErrUnsupported when ceremony is nil (no browser navigator.credentials DOM in Go).
func (p *PasskeyClient) SignInWithPasskey(ctx context.Context, ceremony func(ctx context.Context, opts map[string]any) (map[string]any, error)) (*AuthResponse, error) {
	if ceremony == nil {
		return nil, ErrUnsupported
	}
	opts, err := p.AuthenticationOptions(ctx)
	if err != nil {
		return nil, err
	}
	cred, err := ceremony(ctx, opts.Options)
	if err != nil {
		return nil, err
	}
	return p.AuthenticationVerify(ctx, PasskeyAuthenticationVerifyParams{
		ChallengeID: opts.ChallengeID,
		Credential:  cred,
	})
}
