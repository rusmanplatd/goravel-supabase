package supabase_test

import (
	"context"
	"testing"

	"github.com/rusmanplatd/goravel-supabase/client"
	supabase "github.com/rusmanplatd/goravel-supabase"
)

func TestGoravelLifecycleContextAndWrapClient(t *testing.T) {
	raw, err := client.New("https://demo.supabase.co", "anon-key")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := supabase.WrapClient(raw)
	if _, err := wrapped.Connection("default"); err == nil {
		t.Fatal("expected error calling Connection on WrapClient without manager")
	}
	type ctxKey string
	c2 := wrapped.WithContext(context.WithValue(context.Background(), ctxKey("req"), "123"))
	if c2.Context().Value(ctxKey("req")) != "123" {
		t.Fatal("Context() did not return derived context")
	}
	if err := wrapped.Close(); err != nil {
		t.Fatalf("WrapClient.Close() error: %v", err)
	}

	mgr := supabase.NewManager(nil, nil, nil)
	if err := mgr.Close(); err != nil {
		t.Fatalf("Manager.Close() error: %v", err)
	}
}
