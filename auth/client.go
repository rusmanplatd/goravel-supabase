package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultStorageKey      = "supabase.auth.token"
	DefaultRefreshMargin   = 60 * time.Second
	DefaultJWKSCacheTTL    = 10 * time.Minute
	DefaultAutoRefreshTick = 30 * time.Second
	DefaultAPIVersion      = "2024-01-01"
)

// Client is a pure-Go Supabase GoTrue Auth client.
type Client struct {
	url              string
	headers          map[string]string
	httpClient       *http.Client
	storage          Storage
	lock             Lock
	storageKey       string
	persistSession   bool
	autoRefreshToken bool
	flowType         FlowType
	refreshMargin    time.Duration
	autoRefreshTick  time.Duration
	disabled         bool

	jwks jwksCache

	mu        sync.RWMutex
	session   *Session
	listeners map[string]func(event AuthChangeEvent, session *Session)
	nextSubID atomic.Uint64

	autoRefreshMu     sync.Mutex
	autoRefreshCancel context.CancelFunc
	autoRefreshDone   chan struct{}

	refreshMu              sync.Mutex
	inflightRefresh        *refreshCall
	lastFailedRT           string
	lastFailedRTAt         time.Time
	lastFailedRTErr        error
	refreshFailureCooldown time.Duration
	sessionEpoch           atomic.Uint64

	// Sub-APIs
	MFA     *MFAClient
	OAuth   *OAuthServerClient
	Passkey *PasskeyClient
	Admin   *AdminClient
}

type refreshCall struct {
	done chan struct{}
	resp *AuthResponse
	err  error
}

// GetClaimsOptions configures GetClaimsWithOptions.
type GetClaimsOptions struct {
	JWT          string
	AllowExpired bool
	JWKS         *JWKS
}

// Option configures an auth.Client.
type Option func(*Client)

func WithHeaders(h map[string]string) Option {
	return func(c *Client) {
		for k, v := range h {
			c.headers[k] = v
		}
	}
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

func WithStorage(s Storage) Option {
	return func(c *Client) {
		if s != nil {
			c.storage = s
		}
	}
}

func WithLock(l Lock) Option {
	return func(c *Client) {
		if l != nil {
			c.lock = l
		}
	}
}

func WithStorageKey(key string) Option {
	return func(c *Client) {
		if key != "" {
			c.storageKey = key
		}
	}
}

func WithPersistSession(persist bool) Option {
	return func(c *Client) {
		c.persistSession = persist
	}
}

func WithAutoRefreshToken(enabled bool) Option {
	return func(c *Client) {
		c.autoRefreshToken = enabled
	}
}

func WithFlowType(ft FlowType) Option {
	return func(c *Client) {
		if ft != "" {
			c.flowType = ft
		}
	}
}

func WithRefreshMargin(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.refreshMargin = d
		}
	}
}

func WithAutoRefreshInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.autoRefreshTick = d
		}
	}
}

// WithDisabled marks the Auth client as disabled (used when third-party AccessToken mode is active).
func WithDisabled(disabled bool) Option {
	return func(c *Client) {
		c.disabled = disabled
	}
}

// New creates a new GoTrue Auth Client bound to baseURL (e.g. "https://xyz.supabase.co/auth/v1").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		url:              strings.TrimRight(baseURL, "/"),
		headers:          make(map[string]string),
		httpClient:       &http.Client{},
		storage:          NewMemoryStorage(),
		lock:             NewMutexLock(),
		storageKey:       DefaultStorageKey,
		persistSession:   true,
		autoRefreshToken: false,
		flowType:         FlowImplicit,
		refreshMargin:    DefaultRefreshMargin,
		autoRefreshTick:  DefaultAutoRefreshTick,
		jwks:             jwksCache{ttl: DefaultJWKSCacheTTL},
		listeners:        make(map[string]func(AuthChangeEvent, *Session)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	c.refreshFailureCooldown = 60 * time.Second

	c.MFA = &MFAClient{
		client:        c,
		RecoveryCodes: &RecoveryCodesClient{client: c},
	}
	c.MFA.WebAuthn = &MFAWebAuthnClient{mfa: c.MFA}
	c.OAuth = &OAuthServerClient{client: c}
	c.Passkey = &PasskeyClient{client: c}
	c.Admin = &AdminClient{
		client:          c,
		MFA:             &AdminMFAClient{client: c},
		OAuth:           &AdminOAuthClient{client: c},
		Passkey:         &AdminPasskeyClient{client: c},
		CustomProviders: &AdminCustomProvidersClient{client: c},
	}

	if c.autoRefreshToken && !c.disabled {
		c.StartAutoRefresh()
	}

	return c
}

func (c *Client) checkEnabled() error {
	if c.disabled {
		return ErrAccessTokenMode
	}
	return nil
}

func (c *Client) applyDefaultHeaders(req *http.Request) {
	for k, v := range c.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	if req.Header.Get("X-Supabase-Api-Version") == "" {
		req.Header.Set("X-Supabase-Api-Version", DefaultAPIVersion)
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, bearer string, out any) error {
	_, err := c.doJSONWithHeaders(ctx, method, path, body, bearer, out)
	return err
}

func (c *Client) doJSONWithHeaders(ctx context.Context, method, path string, body any, bearer string, out any) (http.Header, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.url+path, reader)
	if err != nil {
		return nil, &AuthRetryableFetchError{AuthError: &AuthError{Name: "AuthRetryableFetchError", Message: err.Error(), Err: err}}
	}
	c.applyDefaultHeaders(req)
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &AuthRetryableFetchError{AuthError: &AuthError{Name: "AuthRetryableFetchError", Message: err.Error(), Err: err}}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &AuthRetryableFetchError{AuthError: &AuthError{Name: "AuthRetryableFetchError", Message: err.Error(), Err: err}}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Header, decodeAuthError(raw, resp.StatusCode, resp.Status)
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.Header, &AuthError{Name: "AuthError", Message: err.Error(), Err: err}
		}
	}
	return resp.Header, nil
}

func (c *Client) saveSession(ctx context.Context, s *Session, event AuthChangeEvent) error {
	if s != nil && s.ExpiresAt == 0 && s.ExpiresIn > 0 {
		s.ExpiresAt = time.Now().Unix() + s.ExpiresIn
	}
	c.mu.Lock()
	c.session = s
	c.mu.Unlock()

	if c.persistSession && c.storage != nil {
		if s == nil {
			if err := c.storage.RemoveItem(ctx, c.storageKey); err != nil {
				return err
			}
		} else {
			raw, err := json.Marshal(s)
			if err != nil {
				return err
			}
			if err := c.storage.SetItem(ctx, c.storageKey, string(raw)); err != nil {
				return err
			}
		}
	}

	if event != "" {
		c.notifyListeners(event, s)
	}
	return nil
}

func (c *Client) loadSession(ctx context.Context) (*Session, error) {
	c.mu.RLock()
	s := c.session
	c.mu.RUnlock()
	if s != nil {
		return s, nil
	}
	if !c.persistSession || c.storage == nil {
		return nil, nil
	}
	raw, err := c.storage.GetItem(ctx, c.storageKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var loaded Session
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		return nil, nil
	}
	c.mu.Lock()
	c.session = &loaded
	c.mu.Unlock()
	return &loaded, nil
}

func (c *Client) requireAccessToken(ctx context.Context) (string, error) {
	s, err := c.GetSession(ctx)
	if err != nil {
		return "", err
	}
	if s == nil || s.AccessToken == "" {
		return "", ErrSessionMissing
	}
	return s.AccessToken, nil
}

func (c *Client) notifyListeners(event AuthChangeEvent, session *Session) {
	c.mu.RLock()
	cbs := make([]func(AuthChangeEvent, *Session), 0, len(c.listeners))
	for _, cb := range c.listeners {
		cbs = append(cbs, cb)
	}
	c.mu.RUnlock()
	for _, cb := range cbs {
		cb(event, session)
	}
}

// OnAuthStateChange subscribes to auth lifecycle events and immediately emits INITIAL_SESSION.
func (c *Client) OnAuthStateChange(cb func(event AuthChangeEvent, session *Session)) *Subscription {
	id := fmt.Sprintf("sub-%d", c.nextSubID.Add(1))
	c.mu.Lock()
	c.listeners[id] = cb
	cur := c.session
	c.mu.Unlock()

	cb(EventInitialSession, cur)

	return &Subscription{
		ID: id,
		Unsubscribe: func() {
			c.mu.Lock()
			delete(c.listeners, id)
			c.mu.Unlock()
		},
	}
}

// StartAutoRefresh starts a background goroutine that refreshes expiring sessions.
func (c *Client) StartAutoRefresh() {
	c.autoRefreshMu.Lock()
	defer c.autoRefreshMu.Unlock()
	if c.autoRefreshCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.autoRefreshCancel = cancel
	c.autoRefreshDone = done

	go func() {
		defer close(done)
		ticker := time.NewTicker(c.autoRefreshTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s, _ := c.loadSession(ctx)
				if s != nil && s.RefreshToken != "" && s.IsExpired(c.refreshMargin) {
					_, _ = c.RefreshSession(ctx, s.RefreshToken)
				}
			}
		}
	}()
}

// StopAutoRefresh stops the background auto-refresh goroutine and waits for it to exit.
func (c *Client) StopAutoRefresh() {
	c.autoRefreshMu.Lock()
	cancel := c.autoRefreshCancel
	done := c.autoRefreshDone
	c.autoRefreshCancel = nil
	c.autoRefreshDone = nil
	c.autoRefreshMu.Unlock()

	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
}

// --- Session & Identity Operations ---

// GetSession returns the current session, refreshing it under the configured Lock if expiring.
func (c *Client) GetSession(ctx context.Context) (*Session, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s, err := c.loadSession(ctx)
	if err != nil || s == nil {
		return s, err
	}
	if s.IsExpired(c.refreshMargin) && s.RefreshToken != "" {
		refreshed, err := c.RefreshSession(ctx, s.RefreshToken)
		if err != nil {
			return nil, err
		}
		return refreshed.Session, nil
	}
	return s, nil
}

// GetUser fetches the authenticated user from GoTrue `/user` using optional explicit jwt.
func (c *Client) GetUser(ctx context.Context, jwt ...string) (*User, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	token := ""
	if len(jwt) > 0 && jwt[0] != "" {
		token = jwt[0]
	} else {
		var err error
		token, err = c.requireAccessToken(ctx)
		if err != nil {
			return nil, err
		}
	}
	var user User
	if err := c.doJSON(ctx, http.MethodGet, "/user", nil, token, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetClaims verifies and returns JWT claims, validating asymmetric JWTs via JWKS locally
// and falling back to `/user` for symmetric (HS*) or missing-kid tokens.
func (c *Client) GetClaims(ctx context.Context, jwt ...string) (*ClaimsResponse, error) {
	opt := GetClaimsOptions{}
	if len(jwt) > 0 {
		opt.JWT = jwt[0]
	}
	return c.GetClaimsWithOptions(ctx, opt)
}

// GetClaimsWithOptions verifies and returns JWT claims with custom JWKS and AllowExpired controls.
func (c *Client) GetClaimsWithOptions(ctx context.Context, opts GetClaimsOptions) (*ClaimsResponse, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	token := opts.JWT
	if token == "" {
		var err error
		token, err = c.requireAccessToken(ctx)
		if err != nil {
			return nil, err
		}
	}

	header, claims, signingInput, sig, err := parseJWTParts(token)
	if err != nil {
		return nil, &AuthInvalidJwtError{AuthError: &AuthError{Name: "AuthInvalidJwtError", Message: err.Error(), Err: err}}
	}

	if !opts.AllowExpired {
		if expVal, ok := claims["exp"].(float64); ok {
			if time.Now().Unix() >= int64(expVal) {
				return nil, &AuthInvalidJwtError{AuthError: &AuthError{Name: "AuthInvalidJwtError", Message: "JWT has expired"}}
			}
		}
	}

	alg, _ := header["alg"].(string)
	kid, _ := header["kid"].(string)

	verifiedLocally := false
	// Asymmetric algorithms with kid (or custom JWKS): verify locally
	if (strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "ES") || alg == "EdDSA") && (kid != "" || opts.JWKS != nil) {
		var (
			key jwkKey
			ok  bool
		)
		if opts.JWKS != nil {
			for _, k := range opts.JWKS.Keys {
				if kid == "" || k.Kid == kid {
					key = k
					ok = true
					break
				}
			}
		} else {
			key, ok = c.jwks.get(kid)
			if !ok {
				keys, err := c.fetchJWKS(ctx)
				if err != nil {
					return nil, err
				}
				for _, k := range keys {
					if kid == "" || k.Kid == kid {
						key = k
						ok = true
						break
					}
				}
			}
		}
		if ok {
			if err := verifyAsymmetricJWT(alg, key, signingInput, sig); err != nil {
				return nil, &AuthInvalidJwtError{AuthError: &AuthError{Name: "AuthInvalidJwtError", Message: err.Error(), Err: err}}
			}
			verifiedLocally = true
		}
	}
	if !verifiedLocally {
		// Symmetric (HS256 etc.) or missing/unmatched kid: validate remotely via /user
		if _, err := c.GetUser(ctx, token); err != nil {
			return nil, err
		}
	}

	parts := strings.Split(token, ".")
	return &ClaimsResponse{
		Claims:    claims,
		Header:    header,
		Signature: parts[2],
	}, nil
}

// RefreshSession exchanges a refresh token for a new Session with single-flight deduplication,
// failure cooldown, and SignOut removal-epoch protection.
func (c *Client) RefreshSession(ctx context.Context, refreshToken ...string) (*AuthResponse, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Single-flight deduplication across concurrent callers
	c.refreshMu.Lock()
	if c.inflightRefresh != nil {
		call := c.inflightRefresh
		c.refreshMu.Unlock()
		select {
		case <-call.done:
			return call.resp, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &refreshCall{done: make(chan struct{})}
	c.inflightRefresh = call
	c.refreshMu.Unlock()

	defer func() {
		c.refreshMu.Lock()
		c.inflightRefresh = nil
		c.refreshMu.Unlock()
		close(call.done)
	}()

	var result *AuthResponse
	err := c.lock.Acquire(ctx, "lock:"+c.storageKey, func(ctx context.Context) error {
		rt := ""
		if len(refreshToken) > 0 && refreshToken[0] != "" {
			rt = refreshToken[0]
		} else {
			s, err := c.loadSession(ctx)
			if err != nil {
				return err
			}
			if s == nil || s.RefreshToken == "" {
				return ErrSessionMissing
			}
			rt = s.RefreshToken
		}

		// Failure cooldown check for permanently rejected refresh tokens
		c.refreshMu.Lock()
		if rt != "" && rt == c.lastFailedRT && time.Since(c.lastFailedRTAt) < c.refreshFailureCooldown {
			cachedErr := c.lastFailedRTErr
			c.refreshMu.Unlock()
			return cachedErr
		}
		c.refreshMu.Unlock()

		epoch := c.sessionEpoch.Load()

		var session Session
		if err := c.doJSON(ctx, http.MethodPost, "/token?grant_type=refresh_token", map[string]any{
			"refresh_token": rt,
		}, "", &session); err != nil {
			// Record cooldown on non-retryable API error (e.g. 400/401 invalid refresh token)
			var retryable *AuthRetryableFetchError
			if !errors.As(err, &retryable) {
				c.refreshMu.Lock()
				c.lastFailedRT = rt
				c.lastFailedRTAt = time.Now()
				c.lastFailedRTErr = err
				c.refreshMu.Unlock()
			}
			return err
		}

		// Discard refresh if SignOut occurred concurrently while refresh was in flight
		if c.sessionEpoch.Load() != epoch {
			return &AuthRefreshDiscardedError{
				AuthError: &AuthError{
					Name:    "AuthRefreshDiscardedError",
					Message: "session refresh discarded due to concurrent sign out",
				},
			}
		}

		c.refreshMu.Lock()
		if c.lastFailedRT == rt {
			c.lastFailedRT = ""
			c.lastFailedRTErr = nil
		}
		c.refreshMu.Unlock()

		if err := c.saveSession(ctx, &session, EventTokenRefreshed); err != nil {
			return err
		}
		result = &AuthResponse{User: session.User, Session: &session}
		return nil
	})
	call.resp = result
	call.err = err
	return result, err
}

// SetSession sets the active session from an access_token and refresh_token pair,
// automatically refreshing via refreshToken if accessToken is already expired.
func (c *Client) SetSession(ctx context.Context, accessToken, refreshToken string) (*AuthResponse, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	if accessToken == "" || refreshToken == "" {
		return nil, &AuthError{Name: "AuthSessionMissingError", Message: "access_token and refresh_token are required"}
	}
	now := time.Now().Unix()
	var expiresAt int64
	if _, claims, _, _, err := parseJWTParts(accessToken); err == nil {
		if exp, ok := claims["exp"].(float64); ok {
			expiresAt = int64(exp)
		}
	}
	if expiresAt > 0 && expiresAt <= now {
		return c.RefreshSession(ctx, refreshToken)
	}
	user, err := c.GetUser(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	var expiresIn int64
	if expiresAt > now {
		expiresIn = expiresAt - now
	}
	s := &Session{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "bearer",
		ExpiresIn:    expiresIn,
		ExpiresAt:    expiresAt,
		User:         user,
	}
	if err := c.saveSession(ctx, s, EventSignedIn); err != nil {
		return nil, err
	}
	return &AuthResponse{User: user, Session: s}, nil
}

// UpdateUser updates the currently authenticated user's attributes.
func (c *Client) UpdateUser(ctx context.Context, params UpdateUserParams) (*User, error) {
	token, err := c.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	path := "/user"
	if params.EmailRedirectTo != "" {
		path += "?redirect_to=" + url.QueryEscape(params.EmailRedirectTo)
	}
	body := map[string]any{}
	if params.Email != "" {
		body["email"] = params.Email
	}
	if params.Phone != "" {
		body["phone"] = params.Phone
	}
	if params.Password != "" {
		body["password"] = params.Password
	}
	if params.Data != nil {
		body["data"] = params.Data
	}
	if params.Nonce != "" {
		body["nonce"] = params.Nonce
	}
	var pkceKey string
	if c.flowType == FlowPKCE && params.Email != "" {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return nil, err
		}
		body["code_challenge"] = GeneratePKCEChallenge(verifier)
		body["code_challenge_method"] = "s256"
		if pkceKey, err = c.storePKCEVerifier(ctx, "", verifier, false); err != nil {
			return nil, err
		}
	}
	var user User
	if err := c.doJSON(ctx, http.MethodPut, path, body, token, &user); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return nil, err
	}
	c.mu.Lock()
	if c.session != nil {
		c.session.User = &user
	}
	cur := c.session
	c.mu.Unlock()
	if cur != nil {
		_ = c.saveSession(ctx, cur, EventUserUpdated)
	} else {
		c.notifyListeners(EventUserUpdated, nil)
	}
	return &user, nil
}

// SignOut terminates the user's session (default scope: ScopeGlobal).
func (c *Client) SignOut(ctx context.Context, scope ...SignOutScope) error {
	if err := c.checkEnabled(); err != nil {
		return err
	}
	sc := ScopeGlobal
	if len(scope) > 0 && scope[0] != "" {
		sc = scope[0]
	}

	s, _ := c.loadSession(ctx)

	if sc != ScopeOthers {
		c.sessionEpoch.Add(1)
		c.removeAllPKCEVerifiers(ctx)
	}

	if sc == ScopeLocal {
		if err := c.saveSession(ctx, nil, EventSignedOut); err != nil {
			return err
		}
		if s != nil && s.AccessToken != "" {
			_ = c.doJSON(ctx, http.MethodPost, "/logout?scope=local", nil, s.AccessToken, nil)
		}
		return nil
	}

	if s != nil && s.AccessToken != "" {
		path := "/logout?scope=" + url.QueryEscape(string(sc))
		if err := c.doJSON(ctx, http.MethodPost, path, nil, s.AccessToken, nil); err != nil {
			var apiErr *AuthApiError
			if !errors.As(err, &apiErr) || (apiErr.Status != http.StatusUnauthorized && apiErr.Status != http.StatusForbidden && apiErr.Status != http.StatusNotFound) {
				return err
			}
		}
	}

	if sc != ScopeOthers {
		return c.saveSession(ctx, nil, EventSignedOut)
	}
	return nil
}

// GetUserIdentities returns the identities linked to the currently authenticated user.
func (c *Client) GetUserIdentities(ctx context.Context) ([]UserIdentity, error) {
	u, err := c.GetUser(ctx)
	if err != nil {
		return nil, err
	}
	return u.Identities, nil
}

// LinkIdentity initiates linking a new OAuth identity to the current user.
func (c *Client) LinkIdentity(ctx context.Context, params SignInWithOAuthParams) (*OAuthResponse, error) {
	token, err := c.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("provider", params.Provider)
	q.Set("skip_http_redirect", "true")
	if params.RedirectTo != "" {
		q.Set("redirect_to", params.RedirectTo)
	}
	if params.Scopes != "" {
		q.Set("scopes", params.Scopes)
	}
	for k, v := range params.QueryParams {
		q.Set(k, v)
	}

	var pkceKey string
	if c.flowType == FlowPKCE {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return nil, err
		}
		q.Set("code_challenge", GeneratePKCEChallenge(verifier))
		q.Set("code_challenge_method", "s256")
		if pkceKey, err = c.storePKCEVerifier(ctx, params.FlowID, verifier, false); err != nil {
			return nil, err
		}
	}

	var resp struct {
		URL string `json:"url"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/user/identities/authorize?"+q.Encode(), nil, token, &resp); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return nil, err
	}
	return &OAuthResponse{Provider: params.Provider, URL: resp.URL, FlowID: params.FlowID}, nil
}

// LinkIdentityWithIdToken links an OIDC identity to the current user using an ID token.
func (c *Client) LinkIdentityWithIdToken(ctx context.Context, params SignInWithIdTokenParams) (*AuthResponse, error) {
	token, err := c.requireAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"provider":      params.Provider,
		"id_token":      params.Token,
		"link_identity": true,
	}
	if params.AccessToken != "" {
		body["access_token"] = params.AccessToken
	}
	if params.Nonce != "" {
		body["nonce"] = params.Nonce
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/token?grant_type=id_token", body, token, &raw); err != nil {
		return nil, err
	}
	return c.decodeSessionOrUser(ctx, raw, EventUserUpdated)
}

// UnlinkIdentity removes a linked external identity from the current user.
func (c *Client) UnlinkIdentity(ctx context.Context, identity UserIdentity) error {
	token, err := c.requireAccessToken(ctx)
	if err != nil {
		return err
	}
	id := identity.IdentityID
	if id == "" {
		id = identity.ID
	}
	return c.doJSON(ctx, http.MethodDelete, "/user/identities/"+url.PathEscape(id), nil, token, nil)
}

// --- All 14 Sign-In, Registration, and Recovery Flows ---

func (c *Client) decodeSessionOrUser(ctx context.Context, raw map[string]any, event AuthChangeEvent) (*AuthResponse, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if _, hasToken := raw["access_token"]; hasToken {
		var s Session
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
		if err := c.saveSession(ctx, &s, event); err != nil {
			return nil, err
		}
		return &AuthResponse{User: s.User, Session: &s}, nil
	}
	var u User
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, err
	}
	return &AuthResponse{User: &u}, nil
}

func (c *Client) SignUp(ctx context.Context, params SignUpParams) (*AuthResponse, error) {
	body := map[string]any{
		"password": params.Password,
	}
	if params.Email != "" {
		body["email"] = params.Email
	}
	if params.Phone != "" {
		body["phone"] = params.Phone
	}
	if params.Channel != "" {
		body["channel"] = params.Channel
	}
	if params.Data != nil {
		body["data"] = params.Data
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var pkceKey string
	if c.flowType == FlowPKCE && params.Email != "" {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return nil, err
		}
		body["code_challenge"] = GeneratePKCEChallenge(verifier)
		body["code_challenge_method"] = "s256"
		if pkceKey, err = c.storePKCEVerifier(ctx, "", verifier, false); err != nil {
			return nil, err
		}
	}
	path := "/signup"
	if params.EmailRedirectTo != "" {
		path += "?redirect_to=" + url.QueryEscape(params.EmailRedirectTo)
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, path, body, "", &raw); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return nil, err
	}
	return c.decodeSessionOrUser(ctx, raw, EventSignedIn)
}

func (c *Client) SignInWithPassword(ctx context.Context, params SignInWithPasswordParams) (*AuthResponse, error) {
	body := map[string]any{
		"password": params.Password,
	}
	if params.Email != "" {
		body["email"] = params.Email
	}
	if params.Phone != "" {
		body["phone"] = params.Phone
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/token?grant_type=password", body, "", &raw); err != nil {
		return nil, err
	}
	return c.decodeSessionOrUser(ctx, raw, EventSignedIn)
}

func (c *Client) SignInWithOtp(ctx context.Context, params SignInWithOtpParams) error {
	body := map[string]any{}
	if params.Email != "" {
		body["email"] = params.Email
	}
	if params.Phone != "" {
		body["phone"] = params.Phone
	}
	if params.Channel != "" {
		body["channel"] = params.Channel
	}
	if params.Data != nil {
		body["data"] = params.Data
	}
	if params.ShouldCreateUser != nil {
		body["create_user"] = *params.ShouldCreateUser
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var pkceKey string
	if c.flowType == FlowPKCE && params.Email != "" {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return err
		}
		body["code_challenge"] = GeneratePKCEChallenge(verifier)
		body["code_challenge_method"] = "s256"
		if pkceKey, err = c.storePKCEVerifier(ctx, "", verifier, false); err != nil {
			return err
		}
	}
	path := "/otp"
	if params.EmailRedirectTo != "" {
		path += "?redirect_to=" + url.QueryEscape(params.EmailRedirectTo)
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, "", nil); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return err
	}
	return nil
}

func (c *Client) VerifyOtp(ctx context.Context, params VerifyOtpParams) (*AuthResponse, error) {
	body := map[string]any{
		"type": params.Type,
	}
	if params.Email != "" {
		body["email"] = params.Email
	}
	if params.Phone != "" {
		body["phone"] = params.Phone
	}
	if params.Token != "" {
		body["token"] = params.Token
	}
	if params.TokenHash != "" {
		body["token_hash"] = params.TokenHash
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	path := "/verify"
	if params.RedirectTo != "" {
		path += "?redirect_to=" + url.QueryEscape(params.RedirectTo)
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, path, body, "", &raw); err != nil {
		return nil, err
	}
	ev := EventSignedIn
	if params.Type == "recovery" {
		ev = EventPasswordRecovery
	}
	return c.decodeSessionOrUser(ctx, raw, ev)
}

func (c *Client) SignInWithOAuth(ctx context.Context, params SignInWithOAuthParams) (*OAuthResponse, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("provider", params.Provider)
	if params.RedirectTo != "" {
		q.Set("redirect_to", params.RedirectTo)
	}
	if params.Scopes != "" {
		q.Set("scopes", params.Scopes)
	}
	for k, v := range params.QueryParams {
		q.Set(k, v)
	}

	flowID := params.FlowID
	if c.flowType == FlowPKCE {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return nil, err
		}
		challenge := GeneratePKCEChallenge(verifier)
		q.Set("code_challenge", challenge)
		q.Set("code_challenge_method", "s256")
		if _, err := c.storePKCEVerifier(ctx, flowID, verifier, false); err != nil {
			return nil, err
		}
	}

	return &OAuthResponse{
		Provider: params.Provider,
		URL:      c.url + "/authorize?" + q.Encode(),
		FlowID:   flowID,
	}, nil
}

func (c *Client) SignInWithSSO(ctx context.Context, params SignInWithSSOParams) (*SSOResponse, error) {
	body := map[string]any{
		"skip_http_redirect": true,
	}
	if params.ProviderID != "" {
		body["provider_id"] = params.ProviderID
	}
	if params.Domain != "" {
		body["domain"] = params.Domain
	}
	if params.RedirectTo != "" {
		body["redirect_to"] = params.RedirectTo
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var pkceKey string
	if c.flowType == FlowPKCE {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return nil, err
		}
		body["code_challenge"] = GeneratePKCEChallenge(verifier)
		body["code_challenge_method"] = "s256"
		if pkceKey, err = c.storePKCEVerifier(ctx, params.FlowID, verifier, false); err != nil {
			return nil, err
		}
	}
	var resp SSOResponse
	if err := c.doJSON(ctx, http.MethodPost, "/sso", body, "", &resp); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return nil, err
	}
	resp.FlowID = params.FlowID
	return &resp, nil
}

func (c *Client) SignInWithIdToken(ctx context.Context, params SignInWithIdTokenParams) (*AuthResponse, error) {
	body := map[string]any{
		"provider": params.Provider,
		"id_token": params.Token,
	}
	if params.AccessToken != "" {
		body["access_token"] = params.AccessToken
	}
	if params.Nonce != "" {
		body["nonce"] = params.Nonce
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/token?grant_type=id_token", body, "", &raw); err != nil {
		return nil, err
	}
	return c.decodeSessionOrUser(ctx, raw, EventSignedIn)
}

func (c *Client) SignInWithWeb3(ctx context.Context, params SignInWithWeb3Params) (*AuthResponse, error) {
	body := map[string]any{
		"chain":     params.Chain,
		"message":   params.Message,
		"signature": params.Signature,
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/token?grant_type=web3", body, "", &raw); err != nil {
		return nil, err
	}
	return c.decodeSessionOrUser(ctx, raw, EventSignedIn)
}

func (c *Client) SignInAnonymously(ctx context.Context, params ...SignInAnonymouslyParams) (*AuthResponse, error) {
	body := map[string]any{}
	if len(params) > 0 {
		if params[0].Data != nil {
			body["data"] = params[0].Data
		}
		if params[0].CaptchaToken != "" {
			body["gotrue_meta_security"] = map[string]string{"captcha_token": params[0].CaptchaToken}
		}
	}
	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/signup", body, "", &raw); err != nil {
		return nil, err
	}
	return c.decodeSessionOrUser(ctx, raw, EventSignedIn)
}

// ExchangeCodeForSession exchanges a PKCE authorization code for a Session.
// Pass an optional flowID to match a concurrent PKCE flow verifier.
func (c *Client) ExchangeCodeForSession(ctx context.Context, authCode string, flowID ...string) (*AuthResponse, error) {
	if err := c.checkEnabled(); err != nil {
		return nil, err
	}
	fid := ""
	if len(flowID) > 0 {
		fid = flowID[0]
	}
	key := pkceStorageKey(c.storageKey, fid)
	stored := ""
	if c.storage != nil {
		var err error
		stored, err = c.storage.GetItem(ctx, key)
		if err != nil {
			return nil, err
		}
	}
	if stored == "" {
		return nil, &AuthPKCECodeVerifierMissingError{
			AuthError: &AuthError{
				Name:    "AuthPKCECodeVerifierMissingError",
				Message: "PKCE code verifier not found in storage",
			},
		}
	}
	isRecovery := strings.HasSuffix(stored, "/recovery")
	verifier := strings.TrimSuffix(stored, "/recovery")

	var raw map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/token?grant_type=pkce", map[string]any{
		"auth_code":     authCode,
		"code_verifier": verifier,
	}, "", &raw); err != nil {
		return nil, err
	}
	if c.storage != nil {
		_ = c.storage.RemoveItem(ctx, key)
	}
	ev := EventSignedIn
	if isRecovery {
		ev = EventPasswordRecovery
	}
	return c.decodeSessionOrUser(ctx, raw, ev)
}

// GetSessionFromURL parses a server-received callback URL (PKCE code or implicit tokens)
// without requiring browser `window.location` and completes the session handshake.
func (c *Client) GetSessionFromURL(ctx context.Context, rawURL string, flowID ...string) (*AuthResponse, error) {
	params, err := ParseCallbackURL(rawURL)
	if err != nil {
		return nil, err
	}
	if params.Error != "" {
		return nil, &AuthError{Name: "AuthError", Code: params.ErrorCode, Message: params.Error + ": " + params.ErrorDescription}
	}
	if params.Code != "" {
		fid := params.FlowID
		if len(flowID) > 0 && flowID[0] != "" {
			fid = flowID[0]
		}
		return c.ExchangeCodeForSession(ctx, params.Code, fid)
	}
	if params.AccessToken != "" && params.RefreshToken != "" {
		user, err := c.GetUser(ctx, params.AccessToken)
		if err != nil {
			return nil, err
		}
		var expiresAt int64
		if _, claims, _, _, err := parseJWTParts(params.AccessToken); err == nil {
			if exp, ok := claims["exp"].(float64); ok {
				expiresAt = int64(exp)
			}
		}
		s := &Session{
			AccessToken:          params.AccessToken,
			RefreshToken:         params.RefreshToken,
			ProviderToken:        params.ProviderToken,
			ProviderRefreshToken: params.ProviderRefreshToken,
			TokenType:            "bearer",
			ExpiresIn:            params.ExpiresIn,
			ExpiresAt:            expiresAt,
			User:                 user,
		}
		ev := EventSignedIn
		if params.Type == "recovery" {
			ev = EventPasswordRecovery
		}
		if err := c.saveSession(ctx, s, ev); err != nil {
			return nil, err
		}
		return &AuthResponse{User: user, Session: s}, nil
	}
	return nil, ErrSessionMissing
}

func (c *Client) ResetPasswordForEmail(ctx context.Context, email string, opts ...ResetPasswordOptions) error {
	body := map[string]any{"email": email}
	path := "/recover"
	if len(opts) > 0 {
		if opts[0].RedirectTo != "" {
			path += "?redirect_to=" + url.QueryEscape(opts[0].RedirectTo)
		}
		if opts[0].CaptchaToken != "" {
			body["gotrue_meta_security"] = map[string]string{"captcha_token": opts[0].CaptchaToken}
		}
	}
	var pkceKey string
	if c.flowType == FlowPKCE {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return err
		}
		body["code_challenge"] = GeneratePKCEChallenge(verifier)
		body["code_challenge_method"] = "s256"
		if pkceKey, err = c.storePKCEVerifier(ctx, "", verifier, true); err != nil {
			return err
		}
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, "", nil); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return err
	}
	return nil
}

func (c *Client) Resend(ctx context.Context, params ResendParams) error {
	body := map[string]any{"type": params.Type}
	if params.Email != "" {
		body["email"] = params.Email
	}
	if params.Phone != "" {
		body["phone"] = params.Phone
	}
	if params.CaptchaToken != "" {
		body["gotrue_meta_security"] = map[string]string{"captcha_token": params.CaptchaToken}
	}
	var pkceKey string
	if c.flowType == FlowPKCE && params.Email != "" {
		verifier, err := GeneratePKCEVerifier()
		if err != nil {
			return err
		}
		body["code_challenge"] = GeneratePKCEChallenge(verifier)
		body["code_challenge_method"] = "s256"
		if pkceKey, err = c.storePKCEVerifier(ctx, "", verifier, false); err != nil {
			return err
		}
	}
	path := "/resend"
	if params.EmailRedirectTo != "" {
		path += "?redirect_to=" + url.QueryEscape(params.EmailRedirectTo)
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, "", nil); err != nil {
		if pkceKey != "" && c.storage != nil {
			_ = c.storage.RemoveItem(ctx, pkceKey)
		}
		return err
	}
	return nil
}

func (c *Client) Reauthenticate(ctx context.Context) error {
	token, err := c.requireAccessToken(ctx)
	if err != nil {
		return err
	}
	return c.doJSON(ctx, http.MethodGet, "/reauthenticate", nil, token, nil)
}
