# Client (`client` & Goravel Facade)

The `client` package provides the root pure-Go Supabase SDK client (`*client.Client`) that wires together **Auth**, **PostgREST**, **Storage**, **Edge Functions**, and **Realtime**. In Goravel applications, `facades.Supabase()` wraps this client with configuration management, named connections, persistent database session storage, and concurrency-safe per-request scoping.

- **Pure-Go Package:** `github.com/rusmanplatd/goravel-supabase/client`
- **Goravel Facade:** `github.com/rusmanplatd/goravel-supabase/facades`
- **Goravel Contract:** `github.com/rusmanplatd/goravel-supabase/contracts` (`contracts.Supabase`)

---

## 1. Goravel Integration

### Installation & Configuration

```bash
go get github.com/rusmanplatd/goravel-supabase
./artisan package:install github.com/rusmanplatd/goravel-supabase
```

Configure `.env`:

```env
SUPABASE_CONNECTION=default
SUPABASE_URL=https://xyzcompany.supabase.co
SUPABASE_KEY=sb_publishable_...
SUPABASE_DB_SCHEMA=public
SUPABASE_AUTH_STORAGE_DRIVER=database
SUPABASE_AUTH_DB_CONNECTION=
SUPABASE_AUTH_TABLE=supabase_auth_sessions
SUPABASE_AUTH_AUTO_MIGRATE=true
SUPABASE_STORAGE_USE_NEW_HOSTNAME=false
SUPABASE_FUNCTIONS_REGION=any
SUPABASE_TRACE_PROPAGATION=false
```

To add a named connection (e.g. `"analytics"` or `"admin"`), add an entry under `connections` in `config/supabase.go`:

```go
"connections": map[string]any{
	"default": map[string]any{
		"url": config.Env("SUPABASE_URL", ""),
		"key": config.Env("SUPABASE_KEY", ""),
		// ...
	},
	"admin": map[string]any{
		"url": config.Env("SUPABASE_URL", ""),
		"key": config.Env("SUPABASE_SERVICE_ROLE_KEY", ""),
		"auth": map[string]any{
			"storage_driver":  "memory",
			"persist_session": false,
		},
	},
},
```

### Full Goravel Controller Example (Per-Request Scoping & Named Connections)

```go
package controllers

import (
	"context"

	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/facades"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

type ProfileController struct{}

type Profile struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bio      string `json:"bio"`
}

// Show demonstrates concurrency-safe per-request scoping with WithContext, WithToken, and WithSchema.
func (c *ProfileController) Show(ctx http.Context) http.Response {
	jwt := ctx.Request().Header("Authorization")

	// Derive a lightweight client scoped to this request's context and JWT
	// without mutating the shared singleton.
	sb := facades.Supabase().
		WithContext(ctx.Context()).
		WithToken(jwt).
		WithSchema("public")

	user, err := sb.Auth().GetUser(ctx.Context(), jwt)
	if err != nil {
		return ctx.Response().Json(401, http.Json{"error": "unauthorized"})
	}

	profile, _, err := postgrest.Execute[Profile](ctx.Context(),
		sb.From("profiles").Select("id, username, bio").Eq("id", user.ID).Single(),
	)
	if err != nil {
		return ctx.Response().Json(404, http.Json{"error": err.Error()})
	}

	return ctx.Response().Success().Json(profile)
}

// DynamicToken demonstrates WithAccessToken for dynamic/lazy token resolution.
func (c *ProfileController) DynamicToken(ctx http.Context) http.Response {
	sb := facades.Supabase().WithAccessToken(func(reqCtx context.Context) (string, error) {
		return ctx.Request().Header("X-Supabase-JWT"), nil
	})

	res, err := sb.Functions().Invoke(ctx.Context(), "whoami", functions.InvokeOptions{})
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	return ctx.Response().Success().Json(res.Data)
}

// AdminAction demonstrates resolving a named connection from config/supabase.go.
func (c *ProfileController) AdminAction(ctx http.Context) http.Response {
	adminSB := facades.Supabase("admin")

	users, err := adminSB.Auth().Admin.ListUsers(ctx.Context())
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	return ctx.Response().Success().Json(users)
}
```

---

## 2. Standalone Pure-Go Full Example

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/functions"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

func main() {
	sb, err := client.New(
		"https://xyzcompany.supabase.co",
		"sb_publishable_...",
		client.WithSchema("public"),
		client.WithHeaders(map[string]string{"X-App-Name": "my-go-service"}),
		client.WithHTTPClient(&http.Client{Timeout: 15 * time.Second}),
		client.WithDBTimeout(10*time.Second),
		client.WithDBRetry(true),
		client.WithDBRetryBaseDelay(250*time.Millisecond),
		client.WithDBURLLengthLimit(8000),
		client.WithFlowType(auth.FlowPKCE),
		client.WithPersistSession(true),
		client.WithAutoRefreshToken(true),
		client.WithStorageKey("my-app-auth-token"),
		client.WithUseNewHostname(false),
		client.WithFunctionsRegion(functions.RegionUsEast1),
		client.WithRealtimeHeartbeatInterval(25*time.Second),
		client.WithRealtimeVsn("2.0.0"),
		client.WithRealtimeDeferredDisconnect(2*time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// 1. Top-level PostgREST delegate
	rows, _, err := postgrest.Execute[[]map[string]any](ctx,
		sb.From("todos").Select("id, title").Limit(10),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Todos:", rows)

	// 2. Fetch OpenAPI specification for the active schema
	spec, _, err := sb.GetOpenApiSpec(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("OpenAPI paths count:", len(spec))

	// 3. Derive a schema-scoped client without mutating the root client
	analyticsRows, _, err := postgrest.Execute[[]map[string]any](ctx,
		sb.WithSchema("analytics").From("events").Select("*").Limit(5),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Analytics events:", analyticsRows)
}
```

---

## 3. Third-Party Auth Mode (`WithAccessToken`)

When integrating an external identity provider (Clerk, Auth0, Firebase, custom JWT issuer), pass `client.WithAccessToken`. Built-in `sb.Auth` methods are disabled (`auth.ErrUnsupported`), and the callback supplies the bearer token for PostgREST, Storage, Functions, and Realtime:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

func main() {
	sb, err := client.New(
		"https://xyzcompany.supabase.co",
		"sb_publishable_...",
		client.WithAccessToken(func(ctx context.Context) (string, error) {
			// Return a fresh JWT from your external auth provider
			return "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...", nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// Built-in Auth is intentionally disabled in third-party auth mode
	_, err = sb.Auth.GetSession(ctx)
	if errors.Is(err, auth.ErrUnsupported) {
		fmt.Println("Built-in auth disabled as expected")
	}

	// PostgREST, Storage, Functions, and Realtime automatically use the external token
	rows, _, err := postgrest.Execute[[]map[string]any](ctx,
		sb.From("notes").Select("*"),
	)
	fmt.Println(rows, err)
}
```

---

## 4. Available `client.Option` Helpers

| Option | Description |
| --- | --- |
| `WithSchema(schema)` | Sets default PostgREST schema (default `"public"`). |
| `WithHeaders(headers)` | Adds default headers sent on every request. |
| `WithHTTPClient(hc)` | Sets the base `*http.Client` for all services. |
| `WithLogger(l)` | Attaches a `transport.Logger` for SDK diagnostics. |
| `WithTracePropagation(tp)` | Configures W3C Trace Context propagation. |
| `WithAccessToken(fn)` | Enables third-party auth token provider mode. |
| `WithAuthStorage(s)` / `WithAuthLock(l)` | Custom `auth.Storage` and `auth.Lock` implementations. |
| `WithStorageKey(key)` | Custom storage key for persisted auth sessions. |
| `WithPersistSession(bool)` | Enables/disables session persistence. |
| `WithAutoRefreshToken(bool)` | Enables/disables background token auto-refresh. |
| `WithFlowType(ft)` | Sets `auth.FlowPKCE` or `auth.FlowImplicit`. |
| `WithDBTimeout(d)` | Default timeout for PostgREST requests. |
| `WithDBRetry(bool)` / `WithDBRetryBaseDelay(d)` | Enables/configures PostgREST transient retries. |
| `WithDBURLLengthLimit(limit)` | Sets PostgREST URL length threshold. |
| `WithUseNewHostname(bool)` | Uses `.storage.supabase.co` hostname for Storage. |
| `WithFunctionsRegion(region)` | Default `functions.FunctionRegion` for Edge Functions. |
| `WithRealtimeTransport(t)` | Custom WebSocket `realtime.Transport`. |
| `WithRealtimeHeartbeatInterval(d)` | Phoenix heartbeat interval. |
| `WithRealtimeVsn(vsn)` | Realtime protocol version (e.g. `"2.0.0"`). |
| `WithRealtimeReconnectAfterMs(fn)` | Custom reconnect backoff function. |
| `WithRealtimeDeferredDisconnect(d)` | Delay before closing idle Realtime socket. |
