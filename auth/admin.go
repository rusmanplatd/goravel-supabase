package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// AdminClient implements GoTrue Admin APIs requiring a service-role or secret key.
type AdminClient struct {
	client          *Client
	MFA             *AdminMFAClient
	OAuth           *AdminOAuthClient
	Passkey         *AdminPasskeyClient
	CustomProviders *AdminCustomProvidersClient
}

type AdminCreateUserParams struct {
	Email            string         `json:"email,omitempty"`
	Phone            string         `json:"phone,omitempty"`
	Password         string         `json:"password,omitempty"`
	EmailConfirm     bool           `json:"email_confirm,omitempty"`
	PhoneConfirm     bool           `json:"phone_confirm,omitempty"`
	UserMetadata     map[string]any `json:"user_metadata,omitempty"`
	AppMetadata      map[string]any `json:"app_metadata,omitempty"`
	Role             string         `json:"role,omitempty"`
	BanDuration      string         `json:"ban_duration,omitempty"`
}

type AdminUpdateUserParams struct {
	Email        string         `json:"email,omitempty"`
	Phone        string         `json:"phone,omitempty"`
	Password     string         `json:"password,omitempty"`
	EmailConfirm *bool          `json:"email_confirm,omitempty"`
	PhoneConfirm *bool          `json:"phone_confirm,omitempty"`
	UserMetadata map[string]any `json:"user_metadata,omitempty"`
	AppMetadata  map[string]any `json:"app_metadata,omitempty"`
	Role         string         `json:"role,omitempty"`
	BanDuration  string         `json:"ban_duration,omitempty"`
}

type AdminListUsersParams struct {
	Page    int
	PerPage int
}

type AdminListUsersResponse struct {
	Users    []User `json:"users"`
	Aud      string `json:"aud,omitempty"`
	NextPage int    `json:"next_page,omitempty"`
	LastPage int    `json:"last_page,omitempty"`
	Total    int    `json:"total,omitempty"`
}

type GenerateLinkParams struct {
	Type       string         `json:"type"` // signup, invite, magiclink, recovery, email_change_current, email_change_new
	Email      string         `json:"email"`
	NewEmail   string         `json:"new_email,omitempty"`
	Password   string         `json:"password,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	RedirectTo string         `json:"redirect_to,omitempty"`
}

type GenerateLinkResponse struct {
	ActionLink       string `json:"action_link"`
	EmailOtp         string `json:"email_otp"`
	HashedToken      string `json:"hashed_token"`
	RedirectTo       string `json:"redirect_to"`
	VerificationType string `json:"verification_type"`
	User             *User  `json:"user,omitempty"`
}

func (a *AdminClient) CreateUser(ctx context.Context, params AdminCreateUserParams) (*User, error) {
	var user User
	if err := a.client.doJSON(ctx, http.MethodPost, "/admin/users", params, "", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (a *AdminClient) GetUserById(ctx context.Context, uid string) (*User, error) {
	var user User
	if err := a.client.doJSON(ctx, http.MethodGet, "/admin/users/"+url.PathEscape(uid), nil, "", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

var linkPageRe = regexp.MustCompile(`[?&]page=(\d+)`)

func parsePaginationHeaders(h http.Header) (nextPage, lastPage, total int) {
	if rawTotal := h.Get("x-total-count"); rawTotal != "" {
		total, _ = strconv.Atoi(strings.TrimSpace(rawTotal))
	}
	if link := h.Get("Link"); link != "" {
		for _, part := range strings.Split(link, ",") {
			part = strings.TrimSpace(part)
			m := linkPageRe.FindStringSubmatch(part)
			if len(m) != 2 {
				continue
			}
			p, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if strings.Contains(part, `rel="next"`) {
				nextPage = p
			} else if strings.Contains(part, `rel="last"`) {
				lastPage = p
			}
		}
	}
	return nextPage, lastPage, total
}

func (a *AdminClient) ListUsers(ctx context.Context, params ...AdminListUsersParams) (*AdminListUsersResponse, error) {
	q := url.Values{}
	if len(params) > 0 {
		if params[0].Page > 0 {
			q.Set("page", fmt.Sprintf("%d", params[0].Page))
		}
		if params[0].PerPage > 0 {
			q.Set("per_page", fmt.Sprintf("%d", params[0].PerPage))
		}
	}
	path := "/admin/users"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp AdminListUsersResponse
	hdrs, err := a.client.doJSONWithHeaders(ctx, http.MethodGet, path, nil, "", &resp)
	if err != nil {
		return nil, err
	}
	resp.NextPage, resp.LastPage, resp.Total = parsePaginationHeaders(hdrs)
	return &resp, nil
}

func (a *AdminClient) UpdateUserById(ctx context.Context, uid string, params AdminUpdateUserParams) (*User, error) {
	var user User
	if err := a.client.doJSON(ctx, http.MethodPut, "/admin/users/"+url.PathEscape(uid), params, "", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (a *AdminClient) DeleteUser(ctx context.Context, uid string, shouldSoftDelete ...bool) (*User, error) {
	user, err := a.GetUserById(ctx, uid)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if len(shouldSoftDelete) > 0 && shouldSoftDelete[0] {
		body["should_soft_delete"] = true
	}
	if err := a.client.doJSON(ctx, http.MethodDelete, "/admin/users/"+url.PathEscape(uid), body, "", nil); err != nil {
		return nil, err
	}
	return user, nil
}

func (a *AdminClient) InviteUserByEmail(ctx context.Context, email string, data map[string]any, redirectTo ...string) (*User, error) {
	body := map[string]any{"email": email}
	if data != nil {
		body["data"] = data
	}
	path := "/invite"
	if len(redirectTo) > 0 && redirectTo[0] != "" {
		path += "?redirect_to=" + url.QueryEscape(redirectTo[0])
	}
	var user User
	if err := a.client.doJSON(ctx, http.MethodPost, path, body, "", &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (a *AdminClient) GenerateLink(ctx context.Context, params GenerateLinkParams) (*GenerateLinkResponse, error) {
	var raw json.RawMessage
	if err := a.client.doJSON(ctx, http.MethodPost, "/admin/generate_link", params, "", &raw); err != nil {
		return nil, err
	}
	var resp GenerateLinkResponse
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, &AuthError{Name: "AuthError", Message: err.Error(), Err: err}
		}
		if resp.User == nil {
			var u User
			if err := json.Unmarshal(raw, &u); err == nil && (u.ID != "" || u.Email != "" || u.Phone != "") {
				resp.User = &u
			}
		}
	}
	return &resp, nil
}

func (a *AdminClient) SignOut(ctx context.Context, jwt string, scope ...SignOutScope) error {
	sc := ScopeGlobal
	if len(scope) > 0 && scope[0] != "" {
		sc = scope[0]
	}
	return a.client.doJSON(ctx, http.MethodPost, "/logout?scope="+url.QueryEscape(string(sc)), nil, jwt, nil)
}

// --- Admin MFA ---

type AdminMFAClient struct {
	client *Client
}

func (m *AdminMFAClient) ListFactors(ctx context.Context, userID string) ([]Factor, error) {
	var factors []Factor
	if err := m.client.doJSON(ctx, http.MethodGet, "/admin/users/"+url.PathEscape(userID)+"/factors", nil, "", &factors); err != nil {
		return nil, err
	}
	return factors, nil
}

func (m *AdminMFAClient) DeleteFactor(ctx context.Context, userID, factorID string) (*MFAUnenrollResponse, error) {
	var resp MFAUnenrollResponse
	path := "/admin/users/" + url.PathEscape(userID) + "/factors/" + url.PathEscape(factorID)
	if err := m.client.doJSON(ctx, http.MethodDelete, path, nil, "", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- Admin OAuth Clients ---

type AdminOAuthClient struct {
	client *Client
}

type OAuthClientRecord struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name"`
	ClientSecret string   `json:"client_secret,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
	GrantTypes   []string `json:"grant_types,omitempty"`
	ClientType   string   `json:"client_type,omitempty"`
}

func (o *AdminOAuthClient) ListClients(ctx context.Context) ([]OAuthClientRecord, error) {
	var resp struct {
		Clients []OAuthClientRecord `json:"clients"`
	}
	if err := o.client.doJSON(ctx, http.MethodGet, "/admin/oauth/clients", nil, "", &resp); err != nil {
		return nil, err
	}
	return resp.Clients, nil
}

func (o *AdminOAuthClient) CreateClient(ctx context.Context, params map[string]any) (*OAuthClientRecord, error) {
	var rec OAuthClientRecord
	if err := o.client.doJSON(ctx, http.MethodPost, "/admin/oauth/clients", params, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (o *AdminOAuthClient) GetClient(ctx context.Context, clientID string) (*OAuthClientRecord, error) {
	var rec OAuthClientRecord
	if err := o.client.doJSON(ctx, http.MethodGet, "/admin/oauth/clients/"+url.PathEscape(clientID), nil, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (o *AdminOAuthClient) UpdateClient(ctx context.Context, clientID string, params map[string]any) (*OAuthClientRecord, error) {
	var rec OAuthClientRecord
	if err := o.client.doJSON(ctx, http.MethodPut, "/admin/oauth/clients/"+url.PathEscape(clientID), params, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (o *AdminOAuthClient) DeleteClient(ctx context.Context, clientID string) error {
	return o.client.doJSON(ctx, http.MethodDelete, "/admin/oauth/clients/"+url.PathEscape(clientID), nil, "", nil)
}

func (o *AdminOAuthClient) RegenerateClientSecret(ctx context.Context, clientID string) (*OAuthClientRecord, error) {
	var rec OAuthClientRecord
	if err := o.client.doJSON(ctx, http.MethodPost, "/admin/oauth/clients/"+url.PathEscape(clientID)+"/regenerate_secret", nil, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// --- Admin Passkeys ---

type AdminPasskeyClient struct {
	client *Client
}

func (p *AdminPasskeyClient) ListPasskeys(ctx context.Context, userID string) ([]Passkey, error) {
	var passkeys []Passkey
	if err := p.client.doJSON(ctx, http.MethodGet, "/admin/users/"+url.PathEscape(userID)+"/passkeys", nil, "", &passkeys); err != nil {
		return nil, err
	}
	return passkeys, nil
}

func (p *AdminPasskeyClient) DeletePasskey(ctx context.Context, userID, passkeyID string) error {
	path := "/admin/users/" + url.PathEscape(userID) + "/passkeys/" + url.PathEscape(passkeyID)
	return p.client.doJSON(ctx, http.MethodDelete, path, nil, "", nil)
}

// --- Admin Custom OIDC/OAuth Providers ---

type CustomOAuthProvider struct {
	ID           string         `json:"id,omitempty"`
	Identifier   string         `json:"identifier"`
	ProviderType string         `json:"provider_type,omitempty"`
	Name         string         `json:"name,omitempty"`
	ClientID     string         `json:"client_id,omitempty"`
	ClientSecret string         `json:"client_secret,omitempty"`
	Issuer       string         `json:"issuer,omitempty"`
	Enabled      bool           `json:"enabled,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

// AdminCustomProvidersClient implements GoTrue Admin custom OAuth2/OIDC provider APIs.
type AdminCustomProvidersClient struct {
	client *Client
}

func (p *AdminCustomProvidersClient) ListProviders(ctx context.Context, providerType ...string) ([]CustomOAuthProvider, error) {
	path := "/admin/custom-providers"
	if len(providerType) > 0 && providerType[0] != "" {
		path += "?type=" + url.QueryEscape(providerType[0])
	}
	var resp struct {
		Providers []CustomOAuthProvider `json:"providers"`
	}
	if err := p.client.doJSON(ctx, http.MethodGet, path, nil, "", &resp); err != nil {
		return nil, err
	}
	return resp.Providers, nil
}

func (p *AdminCustomProvidersClient) CreateProvider(ctx context.Context, params map[string]any) (*CustomOAuthProvider, error) {
	var rec CustomOAuthProvider
	if err := p.client.doJSON(ctx, http.MethodPost, "/admin/custom-providers", params, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (p *AdminCustomProvidersClient) GetProvider(ctx context.Context, identifier string) (*CustomOAuthProvider, error) {
	var rec CustomOAuthProvider
	if err := p.client.doJSON(ctx, http.MethodGet, "/admin/custom-providers/"+url.PathEscape(identifier), nil, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (p *AdminCustomProvidersClient) UpdateProvider(ctx context.Context, identifier string, params map[string]any) (*CustomOAuthProvider, error) {
	var rec CustomOAuthProvider
	if err := p.client.doJSON(ctx, http.MethodPut, "/admin/custom-providers/"+url.PathEscape(identifier), params, "", &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (p *AdminCustomProvidersClient) DeleteProvider(ctx context.Context, identifier string) error {
	return p.client.doJSON(ctx, http.MethodDelete, "/admin/custom-providers/"+url.PathEscape(identifier), nil, "", nil)
}

func (a *AdminClient) ListProviders(ctx context.Context, providerType ...string) ([]CustomOAuthProvider, error) {
	return a.CustomProviders.ListProviders(ctx, providerType...)
}

func (a *AdminClient) CreateProvider(ctx context.Context, params map[string]any) (*CustomOAuthProvider, error) {
	return a.CustomProviders.CreateProvider(ctx, params)
}

func (a *AdminClient) GetProvider(ctx context.Context, identifier string) (*CustomOAuthProvider, error) {
	return a.CustomProviders.GetProvider(ctx, identifier)
}

func (a *AdminClient) UpdateProvider(ctx context.Context, identifier string, params map[string]any) (*CustomOAuthProvider, error) {
	return a.CustomProviders.UpdateProvider(ctx, identifier, params)
}

func (a *AdminClient) DeleteProvider(ctx context.Context, identifier string) error {
	return a.CustomProviders.DeleteProvider(ctx, identifier)
}
