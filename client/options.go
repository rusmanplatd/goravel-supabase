package client

import (
	"context"
	"net/http"
	"time"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/internal/transport"
	"github.com/rusmanplatd/goravel-supabase/realtime"
)

// Options holds full configuration for constructing a root Supabase Client.
type Options struct {
	Schema           string
	Headers          map[string]string
	HTTPClient       *http.Client
	Logger           transport.Logger
	TracePropagation *transport.TracePropagationOptions
	AccessToken      func(ctx context.Context) (string, error)

	// DB / PostgREST options
	DBTimeout        time.Duration
	DBRetry          *bool
	DBURLLengthLimit int

	// Auth options
	PersistSession   *bool
	AutoRefreshToken *bool
	StorageKey       string
	Storage          auth.Storage
	Lock             auth.Lock
	FlowType         auth.FlowType

	// Storage options
	UseNewHostname bool

	// Functions options
	FunctionsRegion functions.FunctionRegion

	// Realtime options
	RealtimeTransport         realtime.Transport
	RealtimeHeartbeatInterval time.Duration
	RealtimeVsn               string
}

// Option is a functional option for client.New.
type Option func(*Options)

func WithSchema(schema string) Option {
	return func(o *Options) { o.Schema = schema }
}

func WithHeaders(headers map[string]string) Option {
	return func(o *Options) {
		if o.Headers == nil {
			o.Headers = make(map[string]string)
		}
		for k, v := range headers {
			o.Headers[k] = v
		}
	}
}

func WithHTTPClient(hc *http.Client) Option {
	return func(o *Options) { o.HTTPClient = hc }
}

func WithLogger(l transport.Logger) Option {
	return func(o *Options) { o.Logger = l }
}

func WithTracePropagation(tp transport.TracePropagationOptions) Option {
	return func(o *Options) { o.TracePropagation = &tp }
}

// WithAccessToken enables third-party auth mode, disabling built-in Auth client methods.
func WithAccessToken(fn func(ctx context.Context) (string, error)) Option {
	return func(o *Options) { o.AccessToken = fn }
}

func WithAuthStorage(s auth.Storage) Option {
	return func(o *Options) { o.Storage = s }
}

func WithAuthLock(l auth.Lock) Option {
	return func(o *Options) { o.Lock = l }
}

func WithStorageKey(key string) Option {
	return func(o *Options) { o.StorageKey = key }
}

func WithPersistSession(persist bool) Option {
	return func(o *Options) { o.PersistSession = &persist }
}

func WithAutoRefreshToken(enabled bool) Option {
	return func(o *Options) { o.AutoRefreshToken = &enabled }
}

func WithFlowType(ft auth.FlowType) Option {
	return func(o *Options) { o.FlowType = ft }
}

func WithUseNewHostname(enabled bool) Option {
	return func(o *Options) { o.UseNewHostname = enabled }
}

func WithFunctionsRegion(region functions.FunctionRegion) Option {
	return func(o *Options) { o.FunctionsRegion = region }
}

func WithRealtimeTransport(t realtime.Transport) Option {
	return func(o *Options) { o.RealtimeTransport = t }
}

func WithDBTimeout(d time.Duration) Option {
	return func(o *Options) { o.DBTimeout = d }
}

func WithDBRetry(enabled bool) Option {
	return func(o *Options) { o.DBRetry = &enabled }
}
