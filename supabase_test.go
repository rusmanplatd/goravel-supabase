package supabase_test

import (
	"reflect"
	"testing"

	"github.com/goravel/framework/contracts/binding"
	"github.com/goravel/framework/contracts/config"

	supabase "github.com/rusmanplatd/goravel-supabase"
)

func setRowValue(dest any, val string) {
	rv := reflect.ValueOf(dest).Elem()
	elemType := rv.Type().Elem()
	row := reflect.New(elemType).Elem()
	row.FieldByName("Value").SetString(val)
	rv.Set(reflect.Append(rv, row))
}

type mapConfig struct {
	config.Config
	data map[string]any
}

func (c *mapConfig) GetString(path string, defaultValue ...string) string {
	if v, ok := c.data[path].(string); ok {
		return v
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return ""
}

func (c *mapConfig) GetBool(path string, defaultValue ...bool) bool {
	if v, ok := c.data[path].(bool); ok {
		return v
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return false
}

func (c *mapConfig) GetInt(path string, defaultValue ...int) int {
	if v, ok := c.data[path].(int); ok {
		return v
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return 0
}

func (c *mapConfig) Get(path string, defaultValue ...any) any {
	if v, ok := c.data[path]; ok {
		return v
	}
	if len(defaultValue) > 0 {
		return defaultValue[0]
	}
	return nil
}

func TestServiceProviderAndMultiConnectionManager(t *testing.T) {
	sp := &supabase.ServiceProvider{}
	rel := sp.Relationship()
	if len(rel.Bindings) != 1 || rel.Bindings[0] != supabase.Binding {
		t.Fatalf("Bindings = %v", rel.Bindings)
	}
	wantDeps := map[string]bool{binding.Config: true, binding.DB: true, binding.Log: true}
	for _, d := range rel.Dependencies {
		delete(wantDeps, d)
	}
	if len(wantDeps) != 0 {
		t.Fatalf("missing dependencies: %v", wantDeps)
	}

	cfg := &mapConfig{data: map[string]any{
		"supabase.default":                                            "default",
		"supabase.connections.default.url":                            "https://default.supabase.co",
		"supabase.connections.default.key":                            "sb_publishable_default",
		"supabase.connections.default.auth.storage_driver":            "database",
		"supabase.connections.default.db.timeout":                     5000,
		"supabase.connections.default.db.retry":                       true,
		"supabase.connections.default.db.url_length_limit":            4096,
		"supabase.connections.default.headers":                        map[string]string{"X-App": "goravel"},
		"supabase.connections.default.realtime.heartbeat_interval":    15000,
		"supabase.connections.default.realtime.vsn":                   "2.0.0",
		"supabase.connections.default.trace_propagation.enabled":      true,
		"supabase.connections.default.trace_propagation.targets":      []string{"default.supabase.co"},
		"supabase.connections.analytics.url":                          "https://analytics.supabase.co",
		"supabase.connections.analytics.key":                          "sb_publishable_analytics",
		"supabase.connections.analytics.auth.storage_driver":          "memory",
		"database.default":                                            "postgres",
		"database.connections.postgres.driver":                        "postgres",
	}}

	fdb := newFakeDB()
	mgr := supabase.NewManager(cfg, fdb, nil)

	defConn, err := mgr.Connection("")
	if err != nil {
		t.Fatalf("default connection failed: %v", err)
	}
	if defConn.Auth() == nil || defConn.Rest() == nil || defConn.Storage() == nil || defConn.Functions() == nil || defConn.Realtime() == nil {
		t.Fatal("expected all 5 service clients on default connection")
	}

	anConn, err := defConn.Connection("analytics")
	if err != nil {
		t.Fatalf("analytics connection failed: %v", err)
	}
	if anConn == defConn {
		t.Fatal("expected distinct client for analytics connection")
	}

	// Memory driver works with nil DB
	memMgr := supabase.NewManager(cfg, nil, nil)
	if _, err := memMgr.Connection("analytics"); err != nil {
		t.Fatalf("memory connection with nil DB failed: %v", err)
	}

	// Per-request derived scoping
	scoped := defConn.WithToken("user-jwt").WithSchema("custom")
	if scoped == defConn {
		t.Fatal("expected derived client")
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("mgr.Close() error: %v", err)
	}
}
