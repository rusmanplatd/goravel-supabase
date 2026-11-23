# Goravel Supabase (`github.com/rusmanplatd/goravel-supabase`)

A full-featured, 1:1 `supabase-js` compliant Supabase SDK for **Go** and **Goravel**, covering all 257 canonical capabilities across **Auth (GoTrue)**, **PostgREST**, **Storage (Files, Analytics Iceberg REST Catalog, Vectors)**, **Realtime (Phoenix Channels + Binary v2)**, and **Edge Functions**.

## Architecture

- **Pure-Go Core SDK** (`client`, `auth`, `postgrest`, `storage`, `realtime`, `functions`, `internal/transport`): Zero `github.com/goravel/framework` imports. Can be imported directly in any Go project.
- **Thin Goravel Integration Layer** (`service_provider.go`, `supabase.go`, `db_storage.go`, `contracts/`, `facades/`, `setup/`): Provides `facades.Supabase()`, multi-connection resolution, concurrency-safe per-request derived scoping, and dialect-aware raw-SQL persistent auth session storage via `facades.DB()`.

## Installation (Goravel)

```bash
go get github.com/rusmanplatd/goravel-supabase
./artisan package:install github.com/rusmanplatd/goravel-supabase
```

Set your project credentials in `.env`:

```env
SUPABASE_URL=https://xyzcompany.supabase.co
SUPABASE_KEY=sb_publishable_...
```

## Goravel Facade Usage

```go
package controllers

import (
	"context"

	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/facades"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func ListUsers(ctx http.Context) http.Response {
	// Derive a concurrency-safe per-request client bound to the caller's JWT
	jwt := ctx.Request().Header("Authorization")
	sb := facades.Supabase().WithToken(jwt)

	users, _, err := postgrest.Execute[[]User](context.Background(),
		sb.From("users").Select("id, name").Eq("active", true).Order("id"),
	)
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	return ctx.Response().Success().Json(users)
}
```

### Named Connections & Persistent Auth Storage

- Call `facades.Supabase("analytics")` to resolve a named connection from `config/supabase.go`.
- By default (`auth.storage_driver: "database"`), Goravel persists GoTrue sessions in `supabase_auth_sessions` using `facades.DB()` raw SQL with dialect-aware placeholders (`$1` PostgreSQL, `?` MySQL/SQLite, `@p1` SQL Server).
- Set `SUPABASE_AUTH_STORAGE_DRIVER=memory` to use in-memory session storage without requiring a database connection.

## Standalone Pure-Go Usage

```go
package main

import (
	"context"
	"fmt"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

func main() {
	sb, err := client.New(
		"https://xyzcompany.supabase.co",
		"sb_publishable_...",
		client.WithSchema("public"),
	)
	if err != nil {
		panic(err)
	}
	defer sb.Close()

	// PostgREST query
	rows, _, err := postgrest.Execute[[]map[string]any](context.Background(),
		sb.From("profiles").Select("*").Limit(10),
	)
	fmt.Println(rows, err)

	// Edge Function invocation
	res, err := sb.Functions.Invoke(context.Background(), "hello-world", functions.InvokeOptions{
		Region: functions.RegionUsEast1,
		Body:   map[string]any{"name": "Ada"},
	})
	fmt.Println(res, err)
}
```

## SDK Compliance

See [sdk-compliance.yaml](sdk-compliance.yaml) for the complete 257-capability parity matrix mapped to exported Go symbols (verified automatically by `go test ./...`).
