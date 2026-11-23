package supabase

import (
	"context"
	"fmt"
	"sync"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/database/db"
	"github.com/goravel/framework/contracts/log"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/contracts"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/internal/transport"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
	"github.com/rusmanplatd/goravel-supabase/realtime"
	"github.com/rusmanplatd/goravel-supabase/storage"
)

type goravelLoggerAdapter struct {
	log log.Log
}

func (l goravelLoggerAdapter) Debug(msg string, args ...any) {
	if l.log != nil {
		l.log.Debugf(msg+" %v", args)
	}
}
func (l goravelLoggerAdapter) Info(msg string, args ...any) {
	if l.log != nil {
		l.log.Infof(msg+" %v", args)
	}
}
func (l goravelLoggerAdapter) Warn(msg string, args ...any) {
	if l.log != nil {
		l.log.Warningf(msg+" %v", args)
	}
}
func (l goravelLoggerAdapter) Error(msg string, args ...any) {
	if l.log != nil {
		l.log.Errorf(msg+" %v", args)
	}
}

// Manager manages named Supabase connections configured in config/supabase.go.
type Manager struct {
	cfg    config.Config
	db     db.DB
	logger log.Log

	mu      sync.RWMutex
	clients map[string]*ClientAdapter
}

// NewManager creates a new Goravel Supabase Manager.
func NewManager(cfg config.Config, database db.DB, logger log.Log) *Manager {
	return &Manager{
		cfg:     cfg,
		db:      database,
		logger:  logger,
		clients: make(map[string]*ClientAdapter),
	}
}

// Connection resolves and caches the named Supabase connection (or the default connection if empty).
func (m *Manager) Connection(name string) (contracts.Supabase, error) {
	if name == "" {
		name = m.cfg.GetString("supabase.default", "default")
	}

	m.mu.RLock()
	if existing, ok := m.clients[name]; ok {
		m.mu.RUnlock()
		return existing, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.clients[name]; ok {
		return existing, nil
	}

	prefix := "supabase.connections." + name
	sbURL := m.cfg.GetString(prefix + ".url")
	sbKey := m.cfg.GetString(prefix + ".key")
	if sbURL == "" || sbKey == "" {
		return nil, fmt.Errorf("supabase: connection %q is missing url or key in config/supabase.go", name)
	}

	var opts []client.Option
	if schema := m.cfg.GetString(prefix+".db.schema", "public"); schema != "" {
		opts = append(opts, client.WithSchema(schema))
	}
	if m.logger != nil {
		opts = append(opts, client.WithLogger(goravelLoggerAdapter{log: m.logger}))
	}
	if m.cfg.GetBool(prefix+".storage.use_new_hostname", false) {
		opts = append(opts, client.WithUseNewHostname(true))
	}
	if region := m.cfg.GetString(prefix + ".functions.region"); region != "" {
		opts = append(opts, client.WithFunctionsRegion(functions.FunctionRegion(region)))
	}
	if m.cfg.GetBool(prefix+".trace_propagation.enabled", false) {
		respect := m.cfg.GetBool(prefix+".trace_propagation.respect_sampling_decision", true)
		opts = append(opts, client.WithTracePropagation(transport.TracePropagationOptions{
			Enabled:                 true,
			RespectSamplingDecision: &respect,
		}))
	}

	// Auth storage driver: defaults to "database" in Goravel, or "memory" to bypass DB.
	storageDriver := m.cfg.GetString(prefix+".auth.storage_driver", "database")
	if storageDriver == "database" && m.db != nil {
		dbConnName := m.cfg.GetString(prefix + ".auth.db_connection")
		driverName := m.resolveDBDriver(dbConnName)
		table := m.cfg.GetString(prefix+".auth.table", DefaultAuthSessionsTable)
		autoMigrate := m.cfg.GetBool(prefix+".auth.auto_migrate", true)
		store, err := NewDBStorage(m.db, DBStorageOptions{
			Connection:  dbConnName,
			Table:       table,
			Dialect:     NormalizeDialect(driverName),
			AutoMigrate: autoMigrate,
		})
		if err != nil {
			return nil, err
		}
		opts = append(opts, client.WithAuthStorage(store))
	} else {
		opts = append(opts, client.WithAuthStorage(auth.NewMemoryStorage()))
	}

	if storageKey := m.cfg.GetString(prefix + ".auth.storage_key"); storageKey != "" {
		opts = append(opts, client.WithStorageKey(storageKey))
	}
	if flow := m.cfg.GetString(prefix + ".auth.flow_type"); flow != "" {
		opts = append(opts, client.WithFlowType(auth.FlowType(flow)))
	}
	opts = append(opts, client.WithPersistSession(m.cfg.GetBool(prefix+".auth.persist_session", true)))
	opts = append(opts, client.WithAutoRefreshToken(m.cfg.GetBool(prefix+".auth.auto_refresh_token", false)))

	raw, err := client.New(sbURL, sbKey, opts...)
	if err != nil {
		return nil, err
	}

	adapter := &ClientAdapter{manager: m, client: raw}
	m.clients[name] = adapter
	return adapter, nil
}

func (m *Manager) resolveDBDriver(connName string) string {
	if connName == "" {
		connName = m.cfg.GetString("database.default", "sqlite")
	}
	driver := m.cfg.GetString("database.connections." + connName + ".driver")
	if driver == "" {
		return connName
	}
	return driver
}

// ClientAdapter wraps a pure-Go *client.Client to satisfy contracts.Supabase.
type ClientAdapter struct {
	manager *Manager
	client  *client.Client
}

// WrapClient wraps an existing pure-Go *client.Client as a contracts.Supabase.
func WrapClient(c *client.Client) contracts.Supabase {
	return &ClientAdapter{client: c}
}

func (a *ClientAdapter) Auth() *auth.Client           { return a.client.Auth }
func (a *ClientAdapter) Rest() *postgrest.Client      { return a.client.Rest }
func (a *ClientAdapter) Storage() *storage.Client     { return a.client.Storage }
func (a *ClientAdapter) Functions() *functions.Client { return a.client.Functions }
func (a *ClientAdapter) Realtime() *realtime.Client   { return a.client.Realtime }
func (a *ClientAdapter) RawClient() *client.Client    { return a.client }

func (a *ClientAdapter) Connection(name string) (contracts.Supabase, error) {
	if a.manager == nil {
		return nil, fmt.Errorf("supabase: connection manager is not initialized")
	}
	return a.manager.Connection(name)
}

func (a *ClientAdapter) From(relation string, opts ...postgrest.QueryBuilderOptions) *postgrest.QueryBuilder {
	return a.client.From(relation, opts...)
}

func (a *ClientAdapter) Schema(schema string) *postgrest.Client {
	return a.client.Schema(schema)
}

func (a *ClientAdapter) RPC(fn string, args map[string]any, opts ...postgrest.RPCOptions) *postgrest.FilterBuilder {
	return a.client.RPC(fn, args, opts...)
}

func (a *ClientAdapter) GetOpenApiSpec(ctx context.Context) (postgrest.OpenApiSpec, *postgrest.Response, error) {
	return a.client.GetOpenApiSpec(ctx)
}

func (a *ClientAdapter) Channel(name string, cfg ...realtime.ChannelConfig) *realtime.Channel {
	return a.client.Channel(name, cfg...)
}

func (a *ClientAdapter) GetChannels() []*realtime.Channel {
	return a.client.GetChannels()
}

func (a *ClientAdapter) RemoveChannel(ctx context.Context, ch *realtime.Channel) error {
	return a.client.RemoveChannel(ctx, ch)
}

func (a *ClientAdapter) RemoveAllChannels(ctx context.Context) error {
	return a.client.RemoveAllChannels(ctx)
}

func (a *ClientAdapter) WithToken(token string) contracts.Supabase {
	return &ClientAdapter{manager: a.manager, client: a.client.WithToken(token)}
}

func (a *ClientAdapter) WithAccessToken(fn func(ctx context.Context) (string, error)) contracts.Supabase {
	return &ClientAdapter{manager: a.manager, client: a.client.WithAccessToken(fn)}
}

func (a *ClientAdapter) WithContext(ctx context.Context) contracts.Supabase {
	return &ClientAdapter{manager: a.manager, client: a.client.WithContext(ctx)}
}

func (a *ClientAdapter) WithSchema(schema string) contracts.Supabase {
	return &ClientAdapter{manager: a.manager, client: a.client.WithSchema(schema)}
}
