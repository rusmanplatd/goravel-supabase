package postgrest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

func TestPostgrestPhase2ValidationDedup404UpdateAndRetryEdges(t *testing.T) {
	var attempts atomic.Int32
	var gotMethod, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/rest/v1/notfound_arr":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`[]`))
		case "/rest/v1/notfound_empty":
			w.WriteHeader(http.StatusNotFound)
		case "/rest/v1/badjson":
			_, _ = w.Write([]byte(`{broken`))
		case "/rest/v1/retry503":
			if attempts.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`[{"ok":true}]`))
		case "/rest/v1/users":
			_, _ = w.Write([]byte(`[{"id":1,"name":"updated"}]`))
		case "/rest/v1/err500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"XX000","message":"boom","details":"d","hint":"h"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	c := postgrest.New(srv.URL+"/rest/v1", postgrest.WithHTTPClient(srv.Client()), postgrest.WithRetryBaseDelay(time.Millisecond))
	ctx := context.Background()

	// Empty relation returns *PostgrestError
	if _, err := c.From("   ").Select("*").ExecuteRaw(ctx); err == nil {
		t.Fatal("expected error for empty relation name")
	}

	// In/NotIn dedup + Match skips nil
	fb := c.From("users").Select("*").
		In("id", []any{1, 2, 1, 2}).
		NotIn("role", []any{"a", "a", "b"}).
		Match(map[string]any{"active": true, "ignored": nil}).
		Limit(5, postgrest.LimitOptions{ReferencedTable: "posts"}).
		Order("created_at", postgrest.OrderOptions{ReferencedTable: "posts", NullsFirst: boolPtr(false)})
	u, _ := url.Parse(fb.URL())
	q := u.Query()
	if q.Get("id") != "in.(1,2)" || q.Get("role") != "not.in.(a,b)" || q.Get("ignored") != "" || q.Get("posts.limit") != "5" || q.Get("posts.order") != "created_at.asc.nullslast" {
		t.Fatalf("unexpected query params: %s", u.RawQuery)
	}

	// 404 empty-result normalization (#295)
	rArr, err := c.From("notfound_arr").Select("*").ExecuteRaw(ctx)
	if err != nil || rArr.Status != 200 || string(rArr.Data) != "[]" {
		t.Fatalf("404 [] normalization = %+v, %v", rArr, err)
	}
	rEmpty, err := c.From("notfound_empty").Select("*").ExecuteRaw(ctx)
	if err != nil || rEmpty.Status != 204 || len(rEmpty.Data) != 0 {
		t.Fatalf("404 empty normalization = %+v, %v", rEmpty, err)
	}

	// Generic Execute[T] wraps JSON unmarshal failure in *PostgrestError
	_, _, err = postgrest.Execute[[]map[string]any](ctx, c.From("badjson").Select("*"))
	var pgErr *postgrest.PostgrestError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected *PostgrestError from Execute[T], got %T (%v)", err, err)
	}

	// Update mutation
	if _, err := c.From("users").Update(map[string]any{"name": "updated"}, postgrest.UpdateOptions{Count: postgrest.CountExact}).Eq("id", 1).ExecuteRaw(ctx); err != nil || gotMethod != http.MethodPatch || gotQuery != "id=eq.1" {
		t.Fatalf("Update got method=%s query=%s err=%v", gotMethod, gotQuery, err)
	}

	// 503 retry + QueryBuilderOptions Retry: false override
	attempts.Store(0)
	if _, err := c.From("retry503").Select("*").ExecuteRaw(ctx); err != nil || attempts.Load() != 2 {
		t.Fatalf("503 retry attempts=%d err=%v", attempts.Load(), err)
	}
	attempts.Store(0)
	if _, err := c.From("retry503", postgrest.QueryBuilderOptions{Retry: boolPtr(false)}).Select("*").ExecuteRaw(ctx); err == nil || attempts.Load() != 1 {
		t.Fatalf("QueryBuilderOptions Retry:false attempts=%d err=%v", attempts.Load(), err)
	}

	// Context cancellation aborts immediately
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.From("users").Select("*").ExecuteRaw(cctx); err == nil {
		t.Fatal("expected error for cancelled context")
	}

	// decodePostgrestError fields
	_, err = c.From("err500").Select("*").ExecuteRaw(ctx)
	if !errors.As(err, &pgErr) || pgErr.Code != "XX000" || pgErr.Details != "d" || pgErr.Hint != "h" {
		t.Fatalf("decodePostgrestError = %+v, err=%v", pgErr, err)
	}
}
