package postgrest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

func boolPtr(b bool) *bool { return &b }

func TestPostgrestSchemaRPCAndOpenApi(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	var gotHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/v1/":
			_, _ = w.Write([]byte(`{"swagger":"2.0","paths":{"/users":{}}}`))
		case "/rest/v1/rpc/search_items":
			w.Header().Set("Content-Range", "0-1/42")
			_, _ = w.Write([]byte(`[{"id":1}]`))
		default:
			_, _ = w.Write([]byte(`[{"id":1,"name":"Ada"}]`))
		}
	}))
	defer srv.Close()

	c := postgrest.New(srv.URL+"/rest/v1", postgrest.WithHTTPClient(srv.Client()))

	// Schema GET -> Accept-Profile
	type User struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	users, _, err := postgrest.Execute[[]User](context.Background(), c.Schema("analytics").From("events").Select("*"))
	if err != nil {
		t.Fatalf("select error: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Ada" {
		t.Fatalf("users = %+v", users)
	}
	if gotHeaders.Get("Accept-Profile") != "analytics" {
		t.Fatalf("Accept-Profile = %q", gotHeaders.Get("Accept-Profile"))
	}

	// Schema POST -> Content-Profile
	_, err = c.Schema("analytics").From("events").Insert(map[string]any{"name": "click"}).ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("insert error: %v", err)
	}
	if gotHeaders.Get("Content-Profile") != "analytics" {
		t.Fatalf("Content-Profile = %q", gotHeaders.Get("Content-Profile"))
	}

	// RPC GET with count=exact
	resp, err := c.RPC("search_items", map[string]any{"q": "book"}, postgrest.RPCOptions{
		Get:   true,
		Count: postgrest.CountExact,
	}).ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("rpc error: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/rest/v1/rpc/search_items" || gotQuery != "q=book" {
		t.Fatalf("rpc got %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	if resp.Count == nil || *resp.Count != 42 {
		t.Fatalf("count = %v, want 42", resp.Count)
	}
	if !strings.Contains(strings.Join(gotHeaders.Values("Prefer"), ","), "count=exact") {
		t.Fatalf("Prefer = %v", gotHeaders.Values("Prefer"))
	}

	// OpenAPI spec
	spec, _, err := c.GetOpenApiSpec(context.Background())
	if err != nil {
		t.Fatalf("GetOpenApiSpec error: %v", err)
	}
	if spec["swagger"] != "2.0" {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestPostgrestMutationsBulkColumnsAndPreferences(t *testing.T) {
	var gotQuery string
	var gotPrefer []string
	var gotMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		gotPrefer = r.Header.Values("Prefer")
		w.Header().Set("Content-Range", "0-1/2")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`[{"name":"a"},{"name":"b","role":"admin"}]`))
	}))
	defer srv.Close()

	c := postgrest.New(srv.URL+"/rest/v1", postgrest.WithHTTPClient(srv.Client()))

	rows := []map[string]any{
		{"name": "a"},
		{"name": "b", "role": "admin"},
	}
	resp, err := c.From("users").Insert(rows, postgrest.InsertOptions{
		Count:         postgrest.CountExact,
		DefaultToNull: boolPtr(false),
	}).Select("*").ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("insert error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
	q, _ := url.ParseQuery(gotQuery)
	if q.Get("columns") != `"name","role"` {
		t.Fatalf("columns = %q, want %q", q.Get("columns"), `"name","role"`)
	}
	prefer := strings.Join(gotPrefer, ",")
	for _, want := range []string{"count=exact", "missing=default", "return=representation"} {
		if !strings.Contains(prefer, want) {
			t.Fatalf("Prefer = %q missing %q", prefer, want)
		}
	}
	if resp.Count == nil || *resp.Count != 2 {
		t.Fatalf("count = %v", resp.Count)
	}

	// Upsert with onConflict and ignoreDuplicates
	_, err = c.From("users").Upsert(rows, postgrest.UpsertOptions{
		OnConflict:       "email",
		IgnoreDuplicates: true,
	}).ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("upsert error: %v", err)
	}
	q, _ = url.ParseQuery(gotQuery)
	if q.Get("on_conflict") != "email" {
		t.Fatalf("on_conflict = %q", q.Get("on_conflict"))
	}
	if !strings.Contains(strings.Join(gotPrefer, ","), "resolution=ignore-duplicates") {
		t.Fatalf("Prefer = %v", gotPrefer)
	}
}

func TestPostgrestAll31FilterOperators(t *testing.T) {
	c := postgrest.New("https://example.supabase.co/rest/v1")
	fb := c.From("items").Select("*").
		Eq("a", 1).
		Neq("b", 2).
		Gt("c", 3).
		Gte("d", 4).
		Lt("e", 5).
		Lte("f", 6).
		Like("g", "%foo%").
		ILike("h", "%bar%").
		LikeAllOf("i", []string{"a%", "%b"}).
		LikeAnyOf("j", []string{"c%", "%d"}).
		ILikeAllOf("k", []string{"e%", "%f"}).
		ILikeAnyOf("l", []string{"g%", "%h"}).
		Is("m", nil).
		IsDistinct("n", true).
		In("o", []any{"x", "y,z"}).
		NotIn("p", []any{1, 2}).
		Contains("q", []string{"tag1", "tag2"}).
		ContainedBy("r", []string{"tag1"}).
		RangeGt("s", "[1,2]").
		RangeGte("t", "[1,2]").
		RangeLt("u", "[1,2]").
		RangeLte("v", "[1,2]").
		RangeAdjacent("w", "[1,2]").
		Overlaps("x", []string{"a", "b"}).
		TextSearch("y", "fat cat", postgrest.TextSearchOptions{Type: "websearch", Config: "english"}).
		Match(map[string]any{"z": "ok"}).
		Not("aa", "eq", "bad").
		Not("aa_nil", "is", nil).
		Or("a.eq.1,b.eq.2").
		Filter("ab", "gt", 10).
		Filter("ab_nil", "is", nil).
		Contains("int_arr", []int{1, 2, 3}).
		ContainedBy("int64_arr", []int64{4, 5}).
		Overlaps("f64_arr", []float64{1.5, 2.5}).
		RegexMatch("ac", "^foo").
		RegexIMatch("ad", "bar$")

	u, err := url.Parse(fb.URL())
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	expected := map[string]string{
		"a":  "eq.1",
		"b":  "neq.2",
		"c":  "gt.3",
		"d":  "gte.4",
		"e":  "lt.5",
		"f":  "lte.6",
		"g":  "like.%foo%",
		"h":  "ilike.%bar%",
		"i":  "like(all).{a%,%b}",
		"j":  "like(any).{c%,%d}",
		"k":  "ilike(all).{e%,%f}",
		"l":  "ilike(any).{g%,%h}",
		"m":  "is.null",
		"n":  "isdistinct.true",
		"o":  `in.(x,"y,z")`,
		"p":  "not.in.(1,2)",
		"q":  "cs.{tag1,tag2}",
		"r":  "cd.{tag1}",
		"s":  "sr.[1,2]",
		"t":  "nxl.[1,2]",
		"u":  "sl.[1,2]",
		"v":  "nxr.[1,2]",
		"w":  "adj.[1,2]",
		"x":  "ov.{a,b}",
		"y":  "wfts(english).fat cat",
		"z":  "eq.ok",
		"aa":        "not.eq.bad",
		"aa_nil":    "not.is.null",
		"or":        "(a.eq.1,b.eq.2)",
		"ab":        "gt.10",
		"ab_nil":    "is.null",
		"int_arr":   "cs.{1,2,3}",
		"int64_arr": "cd.{4,5}",
		"f64_arr":   "ov.{1.5,2.5}",
		"ac":        "match.^foo",
		"ad":        "imatch.bar$",
	}
	for k, want := range expected {
		if got := q.Get(k); got != want {
			t.Errorf("param %q = %q, want %q", k, got, want)
		}
	}
}

func TestPostgrestModifiersAndCardinality(t *testing.T) {
	var gotHeaders http.Header
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/rest/v1/empty":
			_, _ = w.Write([]byte(`[]`))
		case "/rest/v1/multi":
			_, _ = w.Write([]byte(`[{"id":1},{"id":2}]`))
		case "/rest/v1/single_err":
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(`{"code":"PGRST116","message":"JSON object requested","details":"Results contain 0 rows"}`))
		case "/rest/v1/single_v12_err":
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(`{"code":"PGRST116","message":"JSON object requested","details":"The result contains 0 rows"}`))
		case "/rest/v1/one_row":
			_, _ = w.Write([]byte(`[{"id":7}]`))
		case "/rest/v1/rpc/nested_head":
			_, _ = w.Write([]byte(``))
		default:
			_, _ = w.Write([]byte(`{"id":1}`))
		}
	}))
	defer srv.Close()

	c := postgrest.New(srv.URL+"/rest/v1", postgrest.WithHTTPClient(srv.Client()))

	// MaybeSingle with 0 rows -> nil data, nil error
	resp, err := c.From("empty").Select("*").MaybeSingle().ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("MaybeSingle empty error: %v", err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected empty data, got %q", string(resp.Data))
	}

	// MaybeSingle with >1 rows -> PostgrestError
	_, err = c.From("multi").Select("*").MaybeSingle().ExecuteRaw(context.Background())
	var pgErr *postgrest.PostgrestError
	if !errors.As(err, &pgErr) || pgErr.Code != "PGRST116" {
		t.Fatalf("expected PGRST116 error, got %v", err)
	}

	// MaybeSingle on POST (non-GET) must NOT send Accept: application/vnd.pgrst.object+json and must unwrap 0/1/>1 client-side
	resp, err = c.From("one_row").Insert(map[string]any{"x": 1}).Select("*").MaybeSingle().ExecuteRaw(context.Background())
	if err != nil || string(resp.Data) != `{"id":7}` {
		t.Fatalf("POST MaybeSingle 1-row = %q, %v", string(resp.Data), err)
	}
	if gotHeaders.Get("Accept") == "application/vnd.pgrst.object+json" {
		t.Fatalf("POST MaybeSingle must not send object+json Accept header")
	}
	resp, err = c.From("empty").Insert(map[string]any{"x": 1}).Select("*").MaybeSingle().ExecuteRaw(context.Background())
	if err != nil || len(resp.Data) != 0 {
		t.Fatalf("POST MaybeSingle 0-row = %q, %v", string(resp.Data), err)
	}
	_, err = c.From("multi").Insert(map[string]any{"x": 1}).Select("*").MaybeSingle().ExecuteRaw(context.Background())
	if !errors.As(err, &pgErr) || pgErr.Code != "PGRST116" {
		t.Fatalf("POST MaybeSingle >1-row expected PGRST116, got %v", err)
	}
	// MaybeSingle recognizes PostgREST v12+ "The result contains 0 rows"
	resp, err = c.From("single_v12_err").Select("*").MaybeSingle().ExecuteRaw(context.Background())
	if err != nil || len(resp.Data) != 0 {
		t.Fatalf("MaybeSingle v12 0-row = %q, %v", string(resp.Data), err)
	}
	// RPC HEAD with nested object falls back to POST + Prefer: count=exact,return=minimal
	_, err = c.RPC("nested_head", map[string]any{"filter": map[string]any{"a": 1}}, postgrest.RPCOptions{
		Head:  true,
		Count: postgrest.CountExact,
	}).ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("RPC HEAD nested object error: %v", err)
	}
	if !strings.Contains(strings.Join(gotHeaders.Values("Prefer"), ","), "return=minimal") || !strings.Contains(strings.Join(gotHeaders.Values("Prefer"), ","), "count=exact") {
		t.Fatalf("RPC HEAD nested object Prefer = %v", gotHeaders.Values("Prefer"))
	}
	// RPC GET with typed []int slice formats as Postgres array {1,2}
	rpcURL := c.RPC("by_ids", map[string]any{"ids": []int{1, 2}}, postgrest.RPCOptions{Get: true}).URL()
	if u, _ := url.Parse(rpcURL); u.Query().Get("ids") != "{1,2}" {
		t.Fatalf("RPC GET typed slice query = %q, want {1,2}", u.Query().Get("ids"))
	}

	// Single with 0 rows -> PostgrestError
	_, err = c.From("single_err").Select("*").Single().ExecuteRaw(context.Background())
	if !errors.As(err, &pgErr) || pgErr.Code != "PGRST116" {
		t.Fatalf("expected PGRST116 on Single, got %v", err)
	}

	// Delete + MaxAffected + Rollback
	_, err = c.From("users").Delete().Eq("status", "draft").MaxAffected(5).Rollback().ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}
	prefer := strings.Join(gotHeaders.Values("Prefer"), ",")
	for _, want := range []string{"handling=strict", "max-affected=5", "tx=rollback"} {
		if !strings.Contains(prefer, want) {
			t.Fatalf("Prefer = %q missing %q", prefer, want)
		}
	}

	// Order, Range, StripNulls, Explain
	_, err = c.From("users").Select("id, profile(name)").
		Order("id", postgrest.OrderOptions{Ascending: boolPtr(false), NullsFirst: boolPtr(true)}).
		Range(10, 19).
		Single().
		StripNulls().
		ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	q, _ := url.ParseQuery(gotQuery)
	if q.Get("order") != "id.desc.nullsfirst" || q.Get("offset") != "10" || q.Get("limit") != "10" {
		t.Fatalf("query = %s", gotQuery)
	}
	if gotHeaders.Get("Accept") != "application/vnd.pgrst.object+json;nulls=stripped" {
		t.Fatalf("Accept = %q", gotHeaders.Get("Accept"))
	}

	// Explain
	_, err = c.From("users").Select("*").Explain(postgrest.ExplainOptions{Analyze: true, Format: "json"}).ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("explain error: %v", err)
	}
	if !strings.Contains(gotHeaders.Get("Accept"), `application/vnd.pgrst.plan+json; for="application/json"; options=analyze;`) {
		t.Fatalf("Explain Accept = %q", gotHeaders.Get("Accept"))
	}

	// CSV + StripNulls rejected
	_, err = c.From("users").Select("*").CSV().StripNulls().ExecuteRaw(context.Background())
	if err == nil {
		t.Fatal("expected error combining CSV and StripNulls")
	}

	// GeoJSON
	_, err = c.From("places").Select("*").GeoJSON().ExecuteRaw(context.Background())
	if err != nil || gotHeaders.Get("Accept") != "application/geo+json" {
		t.Fatalf("GeoJSON Accept = %q, err = %v", gotHeaders.Get("Accept"), err)
	}
}

func TestPostgrestRetryAndURLLengthLimit(t *testing.T) {
	var attempts atomic.Int32
	var retryCounts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		retryCounts = append(retryCounts, r.Header.Get("X-Retry-Count"))
		if r.Method == http.MethodGet && n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(520)
			_, _ = w.Write([]byte("cloudflare 520"))
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(520)
			_, _ = w.Write([]byte(`{"message":"server error"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"ok":true}]`))
	}))
	defer srv.Close()

	c := postgrest.New(srv.URL+"/rest/v1",
		postgrest.WithHTTPClient(srv.Client()),
		postgrest.WithRetryBaseDelay(time.Millisecond),
		postgrest.WithURLLengthLimit(120),
	)

	// GET retries on 520 and sends X-Retry-Count: 1
	resp, err := c.From("items").Select("*").ExecuteRaw(context.Background())
	if err != nil {
		t.Fatalf("GET retry failed: %v", err)
	}
	if resp.Status != 200 || attempts.Load() != 2 {
		t.Fatalf("status=%d attempts=%d", resp.Status, attempts.Load())
	}
	if len(retryCounts) != 2 || retryCounts[0] != "" || retryCounts[1] != "1" {
		t.Fatalf("retryCounts = %v", retryCounts)
	}

	// POST does NOT retry on 520
	attempts.Store(0)
	_, err = c.From("items").Insert(map[string]any{"x": 1}).ExecuteRaw(context.Background())
	if err == nil {
		t.Fatal("expected error for POST 520")
	}
	if attempts.Load() != 1 {
		t.Fatalf("POST attempted %d times, want 1", attempts.Load())
	}

	// URLLengthLimit does NOT hard-block when server accepts the long URL
	longVal := strings.Repeat("a", 200)
	longResp, err := c.From("items").Select("*").Eq("name", longVal).ExecuteRaw(context.Background())
	if err != nil || longResp.Status != 200 {
		t.Fatalf("expected long URL to succeed when server accepts it, got resp=%+v err=%v", longResp, err)
	}
	// URLLengthLimit enriches error Hint when server rejects or transport fails
	_, err = c.From("items").Insert(map[string]any{"x": 1}).Eq("name", longVal).ExecuteRaw(context.Background())
	var pgErr *postgrest.PostgrestError
	if !errors.As(err, &pgErr) || !strings.Contains(pgErr.Hint, "URL length") {
		t.Fatalf("expected URL length hint on failed long-URL request, got %+v", err)
	}

	// Ensure JSON decoding works on valid payload
	var decoded []map[string]any
	if err := json.Unmarshal(resp.Data, &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("decoded = %v, err = %v", decoded, err)
	}
}
