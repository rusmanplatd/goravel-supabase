# PostgREST (`postgrest`)

The `postgrest` package provides a fluent, type-safe PostgREST query builder supporting `SELECT`, `INSERT`, `UPSERT`, `UPDATE`, `DELETE`, `RPC`, rich filtering, ordering, pagination, count modes, single-row extraction, CSV/GeoJSON/Explain responses, and automatic retries.

- **Package:** `github.com/rusmanplatd/goravel-supabase/postgrest`
- **Access via Client:** `sb.From(...)`, `sb.Schema(...)`, `sb.RPC(...)`, or `sb.Rest` / `facades.Supabase().Rest()`

---

## 1. Full CRUD & Count Example

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

type Country struct {
	ID   int    `json:"id,omitempty"`
	Name string `json:"name"`
	ISO2 string `json:"iso2"`
}

func main() {
	sb, err := client.New("https://xyzcompany.supabase.co", "sb_publishable_...")
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// 1. INSERT and return created rows
	created, _, err := postgrest.Execute[[]Country](ctx,
		sb.From("countries").
			Insert([]Country{
				{Name: "Wakanda", ISO2: "WK"},
			}, postgrest.InsertOptions{Count: postgrest.CountExact}).
			Select("id, name, iso2"),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Inserted:", created)

	// 2. SELECT with exact count, ordering, and pagination
	asc := true
	list, resp, err := postgrest.Execute[[]Country](ctx,
		sb.From("countries").
			Select("id, name, iso2", postgrest.SelectOptions{Count: postgrest.CountExact}).
			Neq("iso2", "AQ").
			Order("name", postgrest.OrderOptions{Ascending: &asc}).
			Range(0, 9),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Fetched %d of %d total rows\n", len(list), *resp.Count)

	// 3. UPSERT with conflict resolution
	upserted, _, err := postgrest.Execute[[]Country](ctx,
		sb.From("countries").
			Upsert(Country{ID: 1, Name: "Indonesia", ISO2: "ID"}, postgrest.UpsertOptions{
				OnConflict:       "id",
				IgnoreDuplicates: false,
			}).
			Select(),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Upserted:", upserted)

	// 4. UPDATE with filter
	updated, _, err := postgrest.Execute[[]Country](ctx,
		sb.From("countries").
			Update(map[string]any{"name": "Republic of Indonesia"}).
			Eq("iso2", "ID").
			Select(),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Updated:", updated)

	// 5. DELETE with count
	_, delResp, err := postgrest.Execute[[]Country](ctx,
		sb.From("countries").
			Delete(postgrest.DeleteOptions{Count: postgrest.CountExact}).
			Eq("iso2", "WK"),
	)
	if err != nil {
		var pgErr *postgrest.PostgrestError
		if errors.As(err, &pgErr) {
			log.Fatalf("PostgREST %d [%s]: %s (hint: %s)", pgErr.Status, pgErr.Code, pgErr.Message, pgErr.Hint)
		}
		log.Fatal(err)
	}
	fmt.Println("Deleted count:", *delResp.Count)
}
```

---

## 2. `Single`, `MaybeSingle`, and `Head` Count-Only Queries

```go
ctx := context.Background()

// Expect exactly 1 row (returns PostgrestError if 0 or >1 rows)
one, _, err := postgrest.Execute[Country](ctx,
	sb.From("countries").Select("*").Eq("iso2", "ID").Single(),
)

// Expect 0 or 1 row (returns nil pointer if 0 rows)
maybeOne, _, err := postgrest.Execute[*Country](ctx,
	sb.From("countries").Select("*").Eq("iso2", "ZZ").MaybeSingle(),
)

// Count-only HEAD request (no response body transferred)
_, headResp, err := postgrest.Execute[[]Country](ctx,
	sb.From("countries").Select("*", postgrest.SelectOptions{
		Count: postgrest.CountExact,
		Head:  true,
	}),
)
fmt.Println("Total rows:", *headResp.Count, "one:", one.Name, "maybeOne:", maybeOne)
```

---

## 3. Filtering, Pattern Matching, Full-Text Search & Logical Combinators

```go
ctx := context.Background()

// Comparison, IN, IS NULL, and pattern matching
rows, _, err := postgrest.Execute[[]map[string]any](ctx,
	sb.From("products").
		Select("id, title, price, tags").
		Gte("price", 10).
		Lte("price", 100).
		In("category", []any{"books", "electronics"}).
		Is("deleted_at", nil).
		ILike("title", "%goravel%"),
)

// Full-text search
searchRows, _, err := postgrest.Execute[[]map[string]any](ctx,
	sb.From("articles").
		Select("id, title").
		TextSearch("body", "supabase & goravel", postgrest.TextSearchOptions{
			Type:   "websearch",
			Config: "english",
		}),
)

// Array / JSONB containment & overlaps
taggedRows, _, err := postgrest.Execute[[]map[string]any](ctx,
	sb.From("articles").
		Select("id, title, tags").
		Contains("tags", []string{"go", "supabase"}).
		Overlaps("tags", []string{"backend", "api"}),
)

// Logical OR, NOT, and Match map
orRows, _, err := postgrest.Execute[[]map[string]any](ctx,
	sb.From("users").
		Select("id, email, role").
		Or("role.eq.admin,active.eq.true").
		Not("status", "eq", "banned").
		Match(map[string]any{"org_id": 42}),
)
```

---

## 4. Output Formats (`CSV`, `GeoJSON`, `Explain`) & Safety Modifiers

```go
ctx := context.Background()

// CSV export via ExecuteRaw
csvResp, err := sb.From("countries").Select("id, name, iso2").CSV().ExecuteRaw(ctx)
if err != nil {
	log.Fatal(err)
}
fmt.Println("CSV:\n" + string(csvResp.Data))

// GeoJSON response
geoResp, err := sb.From("locations").Select("id, name, geom").GeoJSON().ExecuteRaw(ctx)
_ = geoResp

// PostgreSQL EXPLAIN plan
analyze := true
explainResp, err := sb.From("countries").
	Select("*").
	Explain(postgrest.ExplainOptions{Analyze: &analyze, Format: "text"}).
	ExecuteRaw(ctx)
fmt.Println("Query plan:\n" + string(explainResp.Data))

// Safety & per-query modifiers: Rollback, MaxAffected, StripNulls, Timeout, Retry
_, _, err = postgrest.Execute[[]Country](ctx,
	sb.From("countries").
		Update(map[string]any{"name": "Test"}).
		Eq("iso2", "ID").
		MaxAffected(1).
		Rollback().
		StripNulls().
		Timeout(5*time.Second).
		Retry(false),
)
```

---

## 5. `RPC`, Non-Default Schemas, and Goravel Controller Example

```go
// Call a PostgreSQL function (RPC)
rpcRows, _, err := postgrest.Execute[[]map[string]any](ctx,
	sb.RPC("search_posts", map[string]any{"keyword": "goravel"}).
		Limit(10),
)

// Query a different schema
analyticsRows, _, err := postgrest.Execute[[]map[string]any](ctx,
	sb.Schema("analytics").From("page_views").Select("*").Limit(50),
)
```

### Goravel Controller Example

```go
package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/facades"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

type PostController struct{}

func (c *PostController) Index(ctx http.Context) http.Response {
	sb := facades.Supabase().WithToken(ctx.Request().Header("Authorization"))

	posts, resp, err := postgrest.Execute[[]map[string]any](ctx.Context(),
		sb.From("posts").
			Select("id, title, created_at", postgrest.SelectOptions{Count: postgrest.CountExact}).
			Order("created_at").
			Range(0, 19),
	)
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	return ctx.Response().Success().Json(http.Json{
		"data":  posts,
		"total": resp.Count,
	})
}
```
