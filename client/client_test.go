package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rusmanplatd/goravel-supabase/auth"
	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/postgrest"
)

func TestClientValidationAndThirdPartyAccessTokenMode(t *testing.T) {
	if _, err := client.New("", "key"); err == nil {
		t.Fatal("expected error for empty url")
	}
	if _, err := client.New("ftp://example.com", "key"); err == nil {
		t.Fatal("expected error for non-http scheme")
	}
	if _, err := client.New("https://example.supabase.co", ""); err == nil {
		t.Fatal("expected error for empty key")
	}

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, err := client.New(srv.URL, "sb_publishable_key",
		client.WithHTTPClient(srv.Client()),
		client.WithAccessToken(func(ctx context.Context) (string, error) {
			return "third-party-jwt", nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Built-in Auth is disabled in AccessToken mode
	if _, err := c.Auth.GetSession(context.Background()); !errors.Is(err, auth.ErrAccessTokenMode) {
		t.Fatalf("expected ErrAccessTokenMode, got %v", err)
	}

	// PostgREST uses the AccessToken callback
	if _, err := c.From("items").Select("*").ExecuteRaw(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer third-party-jwt" {
		t.Fatalf("Authorization = %q, want Bearer third-party-jwt", gotAuth)
	}
}

func TestClientConcurrentWithTokenIsolation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Echo back the received Authorization header as JSON
		_, _ = w.Write([]byte(`{"auth":"` + r.Header.Get("Authorization") + `","profile":"` + r.Header.Get("Accept-Profile") + `"}`))
	}))
	defer srv.Close()

	root, err := client.New(srv.URL, "anon-key", client.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	var wg sync.WaitGroup
	tokens := []string{"tok-alice", "tok-bob", "tok-carol", "tok-dave"}
	for _, tok := range tokens {
		tok := tok
		wg.Add(1)
		go func() {
			defer wg.Done()
			scoped := root.WithToken(tok).WithSchema("tenant_" + tok)
			for i := 0; i < 10; i++ {
				var out map[string]string
				if _, err := scoped.From("users").Select("*").Execute(context.Background(), &out); err != nil {
					t.Errorf("scoped request failed: %v", err)
					return
				}
				if out["auth"] != "Bearer "+tok {
					t.Errorf("token leak! got %q, want Bearer %s", out["auth"], tok)
				}
				if out["profile"] != "tenant_"+tok {
					t.Errorf("schema leak! got %q, want tenant_%s", out["profile"], tok)
				}
			}
		}()
	}
	wg.Wait()

	// Root client remains untouched with anon-key and public schema
	var rootOut map[string]string
	if _, err := root.From("users").Select("*").Execute(context.Background(), &rootOut); err != nil {
		t.Fatal(err)
	}
	if rootOut["auth"] != "Bearer anon-key" || rootOut["profile"] != "public" {
		t.Fatalf("root mutated: %+v", rootOut)
	}
}

func TestClientFromCustomHTTPClientAndProjectScopedStorageKey(t *testing.T) {
	var gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("apikey")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/auth/v1/user" {
			_, _ = w.Write([]byte(`{"id":"u1"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, err := client.New(srv.URL, "test-apikey",
		client.WithHTTPClient(srv.Client()),
		client.WithAccessToken(func(context.Context) (string, error) { return "custom-jwt", nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Per-request custom HTTPClient in From() must retain apikey and Authorization
	customHC := &http.Client{Transport: srv.Client().Transport}
	if _, err := c.From("items", postgrest.QueryBuilderOptions{HTTPClient: customHC}).Select("*").ExecuteRaw(context.Background()); err != nil {
		t.Fatalf("From with custom HTTPClient failed: %v", err)
	}
	if gotAPIKey != "test-apikey" || gotAuth != "Bearer custom-jwt" {
		t.Fatalf("custom HTTPClient lost Supabase headers: apikey=%q auth=%q", gotAPIKey, gotAuth)
	}

	// Two clients targeting different projects sharing one Storage get distinct project-ref storage keys
	sharedStorage := auth.NewMemoryStorage()
	cA, err := client.New("https://proj-a.supabase.co", "key-a",
		client.WithHTTPClient(srv.Client()),
		client.WithAuthStorage(sharedStorage),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cA.Close()
	cB, err := client.New("https://proj-b.supabase.co", "key-b",
		client.WithHTTPClient(srv.Client()),
		client.WithAuthStorage(sharedStorage),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cB.Close()

	// Verify distinct storage keys by pre-populating sb-proj-a-auth-token and sb-proj-b-auth-token
	ctx := context.Background()
	_ = sharedStorage.SetItem(ctx, "sb-proj-a-auth-token", `{"access_token":"tok-a","refresh_token":"rt-a","expires_at":9999999999}`)
	_ = sharedStorage.SetItem(ctx, "sb-proj-b-auth-token", `{"access_token":"tok-b","refresh_token":"rt-b","expires_at":9999999999}`)

	sA, err := cA.Auth.GetSession(ctx)
	if err != nil || sA == nil || sA.AccessToken != "tok-a" {
		t.Fatalf("proj-a session = %+v, err = %v", sA, err)
	}
	sB, err := cB.Auth.GetSession(ctx)
	if err != nil || sB == nil || sB.AccessToken != "tok-b" {
		t.Fatalf("proj-b session = %+v, err = %v", sB, err)
	}
}
