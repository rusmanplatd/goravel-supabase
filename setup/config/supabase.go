package config

import (
	"github.com/goravel/framework/facades"
)

func init() {
	config := facades.Config()
	config.Add("supabase", map[string]any{
		// Default Supabase connection name.
		"default": config.Env("SUPABASE_CONNECTION", "default"),

		// Named Supabase connections.
		"connections": map[string]any{
			"default": map[string]any{
				"url": config.Env("SUPABASE_URL", ""),
				"key": config.Env("SUPABASE_KEY", ""),
				"db": map[string]any{
					"schema": config.Env("SUPABASE_DB_SCHEMA", "public"),
				},
				"auth": map[string]any{
					// "database" (default, backed by facades.DB raw SQL) or "memory"
					"storage_driver":     config.Env("SUPABASE_AUTH_STORAGE_DRIVER", "database"),
					"db_connection":      config.Env("SUPABASE_AUTH_DB_CONNECTION", ""),
					"table":              config.Env("SUPABASE_AUTH_TABLE", "supabase_auth_sessions"),
					"storage_key":        config.Env("SUPABASE_AUTH_STORAGE_KEY", ""),
					"auto_migrate":       config.Env("SUPABASE_AUTH_AUTO_MIGRATE", true),
					"persist_session":    true,
					"auto_refresh_token": false,
					"flow_type":          "pkce",
				},
				"storage": map[string]any{
					"use_new_hostname": config.Env("SUPABASE_STORAGE_USE_NEW_HOSTNAME", false),
				},
				"functions": map[string]any{
					"region": config.Env("SUPABASE_FUNCTIONS_REGION", "any"),
				},
				"trace_propagation": map[string]any{
					"enabled":                   config.Env("SUPABASE_TRACE_PROPAGATION", false),
					"respect_sampling_decision": true,
				},
			},
		},
	})
}
