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
