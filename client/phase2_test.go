package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/realtime"
)

func TestClientPhase2OptionsEagerRealtimeAuthAndDelegates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/v1/":
			_, _ = w.Write([]byte(`{"swagger":"2.0"}`))
		case "/rest/v1/rpc/ping":
			_, _ = w.Write([]byte(`{"pong":true}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	c, err := client.New(srv.URL, "anon-key",
		client.WithHTTPClient(srv.Client()),
		client.WithDBURLLengthLimit(4096),
		client.WithDBRetryBaseDelay(time.Millisecond),
		client.WithRealtimeHeartbeatInterval(50*time.Millisecond),
		client.WithRealtimeVsn("2.0.0"),
		client.WithRealtimeReconnectAfterMs(func(int) time.Duration { return time.Millisecond }),
		client.WithRealtimeDeferredDisconnect(20*time.Millisecond),
		client.WithAccessToken(func(context.Context) (string, error) { return "eager-jwt", nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// WithContext / Context
	type ctxKey string
	customCtx := context.WithValue(context.Background(), ctxKey("k"), "v")
	withCtx := c.WithContext(customCtx)
	if withCtx.Context().Value(ctxKey("k")) != "v" {
		t.Fatalf("Context() did not preserve custom context value")
	}

	// RPC & GetOpenApiSpec delegates
	if _, err := c.RPC("ping", nil).ExecuteRaw(context.Background()); err != nil {
		t.Fatalf("RPC delegate error: %v", err)
	}
	if spec, _, err := c.GetOpenApiSpec(context.Background()); err != nil || spec["swagger"] != "2.0" {
		t.Fatalf("GetOpenApiSpec delegate = %+v, %v", spec, err)
	}

	// Channel / GetChannels / RemoveChannel / RemoveAllChannels delegates
	ch := c.Channel("room-a", realtime.ChannelConfig{Private: true})
	if len(c.GetChannels()) != 1 {
		t.Fatalf("GetChannels = %d, want 1", len(c.GetChannels()))
	}
	if err := c.RemoveChannel(context.Background(), ch); err != nil || len(c.GetChannels()) != 0 {
		t.Fatalf("RemoveChannel err=%v len=%d", err, len(c.GetChannels()))
	}
	_ = c.Channel("room-b")
	if err := c.RemoveAllChannels(context.Background()); err != nil || len(c.GetChannels()) != 0 {
		t.Fatalf("RemoveAllChannels err=%v len=%d", err, len(c.GetChannels()))
	}
}
