package contracts

import (
	"context"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
	"github.com/rusmanplatd/goravel-supabase/realtime"
	"github.com/rusmanplatd/goravel-supabase/storage"
)

// Supabase defines the Goravel contract for interacting with Supabase services
// and deriving concurrency-safe per-request scoped clients.
type Supabase interface {
	// Service clients
	Auth() *auth.Client
	Rest() *postgrest.Client
	Storage() *storage.Client
	Functions() *functions.Client
	Realtime() *realtime.Client
	RawClient() *client.Client

	// Multi-connection resolution
	Connection(name string) (Supabase, error)

	// Top-level PostgREST & Realtime delegates
	From(relation string, opts ...postgrest.QueryBuilderOptions) *postgrest.QueryBuilder
	Schema(schema string) *postgrest.Client
	RPC(fn string, args map[string]any, opts ...postgrest.RPCOptions) *postgrest.FilterBuilder
	GetOpenApiSpec(ctx context.Context) (postgrest.OpenApiSpec, *postgrest.Response, error)
	Channel(name string, cfg ...realtime.ChannelConfig) *realtime.Channel
	GetChannels() []*realtime.Channel
	RemoveChannel(ctx context.Context, ch *realtime.Channel) error
	RemoveAllChannels(ctx context.Context) error

	// Concurrency-safe per-request derived scoping
	WithToken(token string) Supabase
	WithAccessToken(fn func(ctx context.Context) (string, error)) Supabase
	WithContext(ctx context.Context) Supabase
	Context() context.Context
	WithSchema(schema string) Supabase

	// Lifecycle
	Close() error
}
