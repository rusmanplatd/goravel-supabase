package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/internal/transport"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
	"github.com/rusmanplatd/goravel-supabase/realtime"
	"github.com/rusmanplatd/goravel-supabase/storage"
)

// Client is the root pure-Go Supabase SDK client orchestrating Auth, PostgREST,
// Storage, Realtime, and Edge Functions.
type Client struct {
	supabaseURL string
	supabaseKey string
	baseHTTP    *http.Client
	opts        Options
	ctx         context.Context

	Auth      *auth.Client
	Rest      *postgrest.Client
	Storage   *storage.Client
	Functions *functions.Client
	Realtime  *realtime.Client

	authSub *auth.Subscription
}

// New validates supabaseURL and supabaseKey and constructs a wired root Supabase Client.
func New(supabaseURL, supabaseKey string, optionFns ...Option) (*Client, error) {
	trimmedURL := strings.TrimSpace(supabaseURL)
	if trimmedURL == "" {
		return nil, errors.New("supabase: supabaseUrl is required")
	}
	parsed, err := url.Parse(trimmedURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("supabase: invalid supabaseUrl %q: must be a valid HTTP or HTTPS URL", supabaseURL)
	}
	trimmedKey := strings.TrimSpace(supabaseKey)
	if trimmedKey == "" {
		return nil, errors.New("supabase: supabaseKey is required")
	}

	opts := Options{
		Schema:  "public",
		Headers: map[string]string{"X-Client-Info": "goravel-supabase-go/1.0.0"},
	}
	for _, fn := range optionFns {
		if fn != nil {
			fn(&opts)
		}
	}

	baseURL := strings.TrimRight(parsed.String(), "/")
	baseHTTP := opts.HTTPClient
	if baseHTTP == nil {
		baseHTTP = &http.Client{}
	}

	c := &Client{
		supabaseURL: baseURL,
		supabaseKey: trimmedKey,
		baseHTTP:    baseHTTP,
		opts:        opts,
		ctx:         context.Background(),
	}

	// Build Auth client
	authHTTP := transport.NewHTTPClient(baseHTTP, transport.Config{
		SupabaseURL:      baseURL,
		APIKey:           trimmedKey,
		Headers:          opts.Headers,
		TracePropagation: opts.TracePropagation,
		Logger:           opts.Logger,
	})
	authOpts := []auth.Option{
		auth.WithHTTPClient(authHTTP),
		auth.WithDisabled(opts.AccessToken != nil),
	}
	if opts.Storage != nil {
		authOpts = append(authOpts, auth.WithStorage(opts.Storage))
	}
	if opts.Lock != nil {
		authOpts = append(authOpts, auth.WithLock(opts.Lock))
	}
	storageKey := opts.StorageKey
	if storageKey == "" {
		if host := parsed.Hostname(); host != "" {
			if ref := strings.Split(host, ".")[0]; ref != "" {
				storageKey = "sb-" + ref + "-auth-token"
			}
		}
	}
	if storageKey != "" {
		authOpts = append(authOpts, auth.WithStorageKey(storageKey))
	}
	if opts.PersistSession != nil {
		authOpts = append(authOpts, auth.WithPersistSession(*opts.PersistSession))
	}
	if opts.AutoRefreshToken != nil {
		authOpts = append(authOpts, auth.WithAutoRefreshToken(*opts.AutoRefreshToken))
	}
	if opts.FlowType != "" {
		authOpts = append(authOpts, auth.WithFlowType(opts.FlowType))
	}
	c.Auth = auth.New(baseURL+"/auth/v1", authOpts...)

	// Token provider shared across PostgREST, Storage, Functions, and Realtime
	tokenProvider := opts.AccessToken
	if tokenProvider == nil {
		tokenProvider = func(ctx context.Context) (string, error) {
			s, err := c.Auth.GetSession(ctx)
			if err != nil || s == nil {
				return "", nil
			}
			return s.AccessToken, nil
		}
	}

	c.wireServices(tokenProvider, opts.Schema)

	// Cross-client token sync: Auth state changes -> Realtime.SetAuth (deduplicated)
	if opts.AccessToken != nil {
		if tok, err := opts.AccessToken(context.Background()); err == nil && tok != "" {
			c.Realtime.SetAuth(tok)
		}
	} else {
		var lastMu sync.Mutex
		var lastToken string
		c.authSub = c.Auth.OnAuthStateChange(func(event auth.AuthChangeEvent, session *auth.Session) {
			var next string
			switch event {
			case auth.EventInitialSession, auth.EventSignedIn, auth.EventTokenRefreshed:
				if session == nil || session.AccessToken == "" {
					return
				}
				next = session.AccessToken
			case auth.EventSignedOut:
				next = ""
			default:
				return
			}
			lastMu.Lock()
			if next == lastToken && event != auth.EventSignedOut {
				lastMu.Unlock()
				return
			}
			lastToken = next
			lastMu.Unlock()
			c.Realtime.SetAuth(next)
		})
	}

	return c, nil
}

func (c *Client) wireServices(tokenProvider transport.TokenProvider, schema string) {
	serviceCfg := transport.Config{
		SupabaseURL:      c.supabaseURL,
		APIKey:           c.supabaseKey,
		Headers:          c.opts.Headers,
		GetAccessToken:   tokenProvider,
		TracePropagation: c.opts.TracePropagation,
		Logger:           c.opts.Logger,
	}
	serviceHTTP := transport.NewHTTPClient(c.baseHTTP, serviceCfg)

	functionsHTTP := transport.NewHTTPClient(c.baseHTTP, transport.Config{
		SupabaseURL:        c.supabaseURL,
		APIKey:             c.supabaseKey,
		Headers:            c.opts.Headers,
		GetAccessToken:     tokenProvider,
		OmitAPIKeyAsBearer: true,
		TracePropagation:   c.opts.TracePropagation,
		Logger:             c.opts.Logger,
	})

	pgOpts := []postgrest.Option{
		postgrest.WithHTTPClient(serviceHTTP),
		postgrest.WithHTTPClientWrapper(func(custom *http.Client) *http.Client {
			return transport.NewHTTPClient(custom, serviceCfg)
		}),
		postgrest.WithSchema(schema),
	}
	if c.opts.DBTimeout > 0 {
		pgOpts = append(pgOpts, postgrest.WithTimeout(c.opts.DBTimeout))
	}
	if c.opts.DBRetry != nil {
		pgOpts = append(pgOpts, postgrest.WithRetry(*c.opts.DBRetry))
	}
	if c.opts.DBRetryBaseDelay > 0 {
		pgOpts = append(pgOpts, postgrest.WithRetryBaseDelay(c.opts.DBRetryBaseDelay))
	}
	if c.opts.DBURLLengthLimit > 0 {
		pgOpts = append(pgOpts, postgrest.WithURLLengthLimit(c.opts.DBURLLengthLimit))
	}
	c.Rest = postgrest.New(c.supabaseURL+"/rest/v1", pgOpts...)

	c.Storage = storage.New(c.supabaseURL+"/storage/v1",
		storage.WithHTTPClient(serviceHTTP),
		storage.WithUseNewHostname(c.opts.UseNewHostname),
	)

	fnOpts := []functions.Option{functions.WithHTTPClient(functionsHTTP)}
	if c.opts.FunctionsRegion != "" {
		fnOpts = append(fnOpts, functions.WithRegion(c.opts.FunctionsRegion))
	}
	c.Functions = functions.New(c.supabaseURL+"/functions/v1", fnOpts...)

	rtURL := strings.Replace(c.supabaseURL, "https://", "wss://", 1)
	rtURL = strings.Replace(rtURL, "http://", "ws://", 1) + "/realtime/v1"
	rtOpts := []realtime.Option{
		realtime.WithAPIKey(c.supabaseKey),
		realtime.WithHTTPClient(serviceHTTP),
		realtime.WithHeaders(c.opts.Headers),
		realtime.WithAccessToken(tokenProvider),
	}
	if c.opts.RealtimeTransport != nil {
		rtOpts = append(rtOpts, realtime.WithTransport(c.opts.RealtimeTransport))
	}
	if c.opts.RealtimeHeartbeatInterval > 0 {
		rtOpts = append(rtOpts, realtime.WithHeartbeatInterval(c.opts.RealtimeHeartbeatInterval))
	}
	if c.opts.RealtimeVsn != "" {
		rtOpts = append(rtOpts, realtime.WithVsn(c.opts.RealtimeVsn))
	}
	if c.opts.RealtimeReconnectAfterMs != nil {
		rtOpts = append(rtOpts, realtime.WithReconnectAfterMs(c.opts.RealtimeReconnectAfterMs))
	}
	if c.opts.RealtimeDeferredDisconnect > 0 {
		rtOpts = append(rtOpts, realtime.WithDeferredDisconnect(c.opts.RealtimeDeferredDisconnect))
	}
	if c.opts.Logger != nil {
		rtOpts = append(rtOpts, realtime.WithLogger(c.opts.Logger))
	}
	c.Realtime = realtime.New(rtURL, rtOpts...)
}

// --- Top-Level Delegates ---

func (c *Client) From(relation string, opts ...postgrest.QueryBuilderOptions) *postgrest.QueryBuilder {
	return c.Rest.From(relation, opts...)
}

func (c *Client) Schema(schema string) *postgrest.Client {
	return c.Rest.Schema(schema)
}

func (c *Client) RPC(fn string, args map[string]any, opts ...postgrest.RPCOptions) *postgrest.FilterBuilder {
	return c.Rest.RPC(fn, args, opts...)
}

func (c *Client) GetOpenApiSpec(ctx context.Context) (postgrest.OpenApiSpec, *postgrest.Response, error) {
	if ctx == nil {
		ctx = c.ctx
	}
	return c.Rest.GetOpenApiSpec(ctx)
}

func (c *Client) Channel(name string, cfg ...realtime.ChannelConfig) *realtime.Channel {
	return c.Realtime.Channel(name, cfg...)
}

func (c *Client) GetChannels() []*realtime.Channel {
	return c.Realtime.GetChannels()
}

func (c *Client) RemoveChannel(ctx context.Context, ch *realtime.Channel) error {
	if ctx == nil {
		ctx = c.ctx
	}
	return c.Realtime.RemoveChannel(ctx, ch)
}

func (c *Client) RemoveAllChannels(ctx context.Context) error {
	if ctx == nil {
		ctx = c.ctx
	}
	return c.Realtime.RemoveAllChannels(ctx)
}

// --- Concurrency-Safe Per-Request Derived Client Scoping ---

// WithToken returns a lightweight derived Client bound to the given bearer JWT,
// sharing the parent's underlying HTTP connection pool without mutating the parent.
func (c *Client) WithToken(token string) *Client {
	return c.WithAccessToken(func(context.Context) (string, error) {
		return token, nil
	})
}

// WithAccessToken returns a lightweight derived Client resolving bearer tokens via fn,
// sharing the parent's underlying HTTP connection pool without mutating the parent.
func (c *Client) WithAccessToken(fn func(ctx context.Context) (string, error)) *Client {
	derived := &Client{
		supabaseURL: c.supabaseURL,
		supabaseKey: c.supabaseKey,
		baseHTTP:    c.baseHTTP,
		opts:        c.opts,
		ctx:         c.ctx,
		Auth:        c.Auth,
	}
	derived.wireServices(fn, c.opts.Schema)
	return derived
}

// WithContext returns a shallow-derived Client carrying ctx as its default context.
func (c *Client) WithContext(ctx context.Context) *Client {
	cp := *c
	if ctx != nil {
		cp.ctx = ctx
	}
	return &cp
}

// Context returns the default context associated with this client.
func (c *Client) Context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// WithSchema returns a shallow-derived Client whose default PostgREST schema is set to schema.
func (c *Client) WithSchema(schema string) *Client {
	cp := *c
	cp.opts.Schema = schema
	cp.Rest = c.Rest.Schema(schema)
	return &cp
}

// Close releases background resources (auto-refresh timer, auth listener, realtime socket).
func (c *Client) Close() error {
	if c.authSub != nil {
		c.authSub.Unsubscribe()
	}
	if c.Auth != nil {
		c.Auth.StopAutoRefresh()
	}
	if c.Realtime != nil {
		return c.Realtime.Disconnect()
	}
	return nil
}
