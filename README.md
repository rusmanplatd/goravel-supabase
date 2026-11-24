# Goravel Supabase (`github.com/rusmanplatd/goravel-supabase`)

A full-featured, 1:1 `supabase-js` compliant Supabase SDK for **Go** and **Goravel**, covering all 257 canonical capabilities across **Auth (GoTrue)**, **PostgREST**, **Storage (Files, Analytics Iceberg REST Catalog, Vectors)**, **Realtime (Phoenix Channels + Binary v2)**, and **Edge Functions**.

## Documentation

| Package | Guide & Full Examples | Description |
| --- | --- | --- |
| **Client & Goravel Facade** | [docs/client.md](docs/client.md) | Root client setup, `client.Option` helpers, Goravel `facades.Supabase()`, named connections, per-request scoping, and third-party auth mode. |
| **Auth (GoTrue)** | [docs/auth.md](docs/auth.md) | Password, OTP/magic link, OAuth + PKCE, identity linking, SSO, Web3, anonymous sign-in, MFA, recovery codes, Passkeys, OAuth Server consent, JWKS claims, and Admin API. |
| **PostgREST** | [docs/postgrest.md](docs/postgrest.md) | Fluent `SELECT`/`INSERT`/`UPSERT`/`UPDATE`/`DELETE`, `Single`/`MaybeSingle`/`Head`, filters, full-text search, `CSV`/`GeoJSON`/`Explain`, `RPC`, and schemas. |
| **Storage** | [docs/storage.md](docs/storage.md) | Buckets & lifecycle rules, file uploads/downloads/streaming, image transforms, public & signed URLs, Analytics Iceberg REST Catalog, and Vector Buckets/Indexes. |
| **Realtime** | [docs/realtime.md](docs/realtime.md) | Phoenix Channels WebSocket Broadcast, Postgres Changes (`RealtimePostgresFilterBuilder`), Presence (`Track`/`Untrack`), and stateless HTTP Broadcast (`HTTPSend`). |
| **Edge Functions** | [docs/functions.md](docs/functions.md) | JSON, binary, `MultipartBody` uploads, `text/event-stream` SSE streaming, regional routing (`FunctionRegion`), and typed error handling. |

## Architecture

- **Pure-Go Core SDK** (`client`, `auth`, `postgrest`, `storage`, `realtime`, `functions`, `internal/transport`): Zero `github.com/goravel/framework` imports. Can be imported directly in any Go project.
- **Thin Goravel Integration Layer** (`service_provider.go`, `supabase.go`, `db_storage.go`, `contracts/`, `facades/`, `setup/`): Provides `facades.Supabase()`, `contracts.Supabase`, multi-connection resolution, concurrency-safe per-request derived scoping (`WithToken`, `WithAccessToken`, `WithContext`, `WithSchema`), and dialect-aware raw-SQL persistent auth session storage via `facades.DB()`.

## Installation (Goravel)

```bash
go get github.com/rusmanplatd/goravel-supabase
./artisan package:install github.com/rusmanplatd/goravel-supabase
```

Set your project credentials in `.env`:

```env
SUPABASE_CONNECTION=default
SUPABASE_URL=https://xyzcompany.supabase.co
SUPABASE_KEY=sb_publishable_...
```

## Goravel Facade Usage

```go
package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/facades"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func ListUsers(ctx http.Context) http.Response {
	// Derive a concurrency-safe per-request client bound to the caller's JWT and context
	jwt := ctx.Request().Header("Authorization")
	sb := facades.Supabase().WithContext(ctx.Context()).WithToken(jwt)

	users, _, err := postgrest.Execute[[]User](ctx.Context(),
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
	"log"

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
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// PostgREST query
	rows, _, err := postgrest.Execute[[]map[string]any](ctx,
		sb.From("profiles").Select("*").Limit(10),
	)
	fmt.Println(rows, err)

	// Edge Function invocation
	res, err := sb.Functions.Invoke(ctx, "hello-world", functions.InvokeOptions{
		Region: functions.RegionUsEast1,
		Body:   map[string]any{"name": "Ada"},
	})
	fmt.Println(res, err)
}
```

## Testing & SDK Compliance

Run the full test suite (including the automated 257-capability compliance check against [sdk-compliance.yaml](sdk-compliance.yaml)):

```bash
go test ./...
```

## Author & Links

- **Email:** [rusmanplatd@gmail.com](mailto:rusmanplatd@gmail.com)
- **Website:** [https://rusmanplatd.id](https://rusmanplatd.id)
- **Instagram:** [@rusmanplatd](https://instagram.com/rusmanplatd)
- **LinkedIn:** [linkedin.com/in/rusmanplatd](https://linkedin.com/in/rusmanplatd)
- **Twitter / X:** [@rusmanplatd](https://twitter.com/rusmanplatd)
