package realtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rusmanplatd/goravel-supabase/realtime"
)

func TestRealtimePhase2TimeoutDeduplicationSystemAndPostgresBindings(t *testing.T) {
	s := realtime.NewSerializer()

	// 1. Public channel rejects Broadcast.Replay
	c0 := realtime.New("ws://localhost:1234/realtime/v1")
	pubReplay := c0.Channel("pub-replay", realtime.ChannelConfig{
		Broadcast: realtime.BroadcastConfig{Replay: map[string]any{"since": 100}},
	})
	if err := pubReplay.Subscribe(context.Background()); err == nil {
		t.Fatal("expected error for Broadcast.Replay on public channel")
	}
	// Channel topic deduplication
	if c0.Channel("pub-replay") != pubReplay || c0.Channel("realtime:pub-replay") != pubReplay {
		t.Fatal("expected Channel() to deduplicate by normalized topic")
	}

	// 2. Join timeout -> SubscribeTimedOut
	silentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer silentSrv.Close()

	tc := realtime.New("ws"+strings.TrimPrefix(silentSrv.URL, "http")+"/realtime/v1",
		realtime.WithHTTPClient(silentSrv.Client()),
		realtime.WithTimeout(30*time.Millisecond),
	)
	timedOut := make(chan realtime.SubscribeStatus, 1)
	if err := tc.Channel("slow").Subscribe(context.Background(), func(st realtime.SubscribeStatus, _ error) {
		timedOut <- st
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-timedOut:
		if st != realtime.SubscribeTimedOut {
			t.Fatalf("status = %q, want TIMED_OUT", st)
		}
	case <-time.After(time.Second):
		t.Fatal("expected SubscribeTimedOut")
	}
	_ = tc.Disconnect()

	// 3. Postgres Changes server binding IDs + column transformers + system/phx_error events
	pgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			msg, err := s.Decode(false, data)
			if err != nil {
				continue
			}
			if msg.Event == "phx_join" {
				_, reply, _ := s.Encode(realtime.Message{
					JoinRef: msg.JoinRef,
					Ref:     msg.Ref,
					Topic:   msg.Topic,
					Event:   "phx_reply",
					Payload: map[string]any{
						"status": "ok",
						"response": map[string]any{
							"postgres_changes": []any{
								map[string]any{"id": 101, "event": "INSERT", "schema": "public", "table": "items", "filter": "a=eq.1"},
								map[string]any{"id": 102, "event": "INSERT", "schema": "public", "table": "items", "filter": "a=eq.2"},
							},
						},
					},
				})
				_ = c.Write(ctx, websocket.MessageText, reply)

				// Send system event
				_, sysMsg, _ := s.Encode(realtime.Message{
					Topic:   msg.Topic,
					Event:   "system",
					Payload: map[string]any{"status": "ok", "message": "Subscribed"},
				})
				_ = c.Write(ctx, websocket.MessageText, sysMsg)

				// Send postgres_changes matching only ID 102 with typed Postgres columns
				_, pgMsg, _ := s.Encode(realtime.Message{
					Topic: msg.Topic,
					Event: "postgres_changes",
					Payload: map[string]any{
						"ids": []any{102},
						"data": map[string]any{
							"type":   "INSERT",
							"schema": "public",
							"table":  "items",
							"columns": []any{
								map[string]any{"name": "id", "type": "int4"},
								map[string]any{"name": "active", "type": "bool"},
								map[string]any{"name": "score", "type": "float8"},
								map[string]any{"name": "meta", "type": "jsonb"},
								map[string]any{"name": "tags", "type": "_int4"},
							},
							"record": map[string]any{
								"id":     "42",
								"active": "t",
								"score":  "3.5",
								"meta":   `{"k":"v"}`,
								"tags":   "{1,2,3}",
							},
						},
					},
				})
				_ = c.Write(ctx, websocket.MessageText, pgMsg)
			}
		}
	}))
	defer pgSrv.Close()

	pgClient := realtime.New("ws"+strings.TrimPrefix(pgSrv.URL, "http")+"/realtime/v1",
		realtime.WithHTTPClient(pgSrv.Client()),
	)
	ch := pgClient.Channel("pg-room")
	var id101Calls atomic.Int32
	id102Ch := make(chan map[string]any, 1)
	sysCh := make(chan map[string]any, 1)

	ch.On("system", "", func(p map[string]any) { sysCh <- p })
	ch.OnPostgresChanges(realtime.PostgresChangesFilter{
		Event: "INSERT", Schema: "public", Table: "items", Filter: "a=eq.1",
	}, func(map[string]any) { id101Calls.Add(1) })
	ch.OnPostgresChanges(realtime.PostgresChangesFilter{
		Event: "INSERT", Schema: "public", Table: "items", Filter: "a=eq.2",
	}, func(p map[string]any) { id102Ch <- p })

	if err := ch.Subscribe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !pgClient.IsConnected() || pgClient.IsConnecting() || pgClient.IsDisconnecting() {
		t.Fatalf("unexpected connection predicates: connected=%v", pgClient.IsConnected())
	}

	select {
	case sys := <-sysCh:
		if sys["message"] != "Subscribed" {
			t.Fatalf("system payload = %+v", sys)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for system event")
	}

	select {
	case ev := <-id102Ch:
		newRec, ok := ev["new"].(map[string]any)
		if !ok {
			t.Fatalf("missing transformed 'new' record: %+v", ev)
		}
		if newRec["id"] != int64(42) || newRec["active"] != true || newRec["score"] != float64(3.5) {
			t.Fatalf("unexpected converted record: %+v", newRec)
		}
		meta, ok := newRec["meta"].(map[string]any)
		if !ok || meta["k"] != "v" {
			t.Fatalf("unexpected jsonb conversion: %#v", newRec["meta"])
		}
		tags, ok := newRec["tags"].([]any)
		if !ok || len(tags) != 3 || tags[0] != int64(1) {
			t.Fatalf("unexpected _int4 array conversion: %#v", newRec["tags"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for postgres_changes ID 102")
	}
	if id101Calls.Load() != 0 {
		t.Fatalf("binding 101 should NOT have been called for ids=[102], calls=%d", id101Calls.Load())
	}
	_ = pgClient.Disconnect()
}
