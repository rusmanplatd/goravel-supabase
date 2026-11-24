package realtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/rusmanplatd/goravel-supabase/internal/transport"
	"github.com/rusmanplatd/goravel-supabase/realtime"
)

func TestSerializerBinaryV2AndUTF8(t *testing.T) {
	s := realtime.NewSerializer()

	// Encode kind 3 with multi-byte UTF-8 topic & event + persist metadata + ignored smuggled key
	isBin, frame, err := s.Encode(realtime.Message{
		JoinRef: "1",
		Ref:     "2",
		Topic:   "realtime:café-🚀",
		Event:   "broadcast",
		Payload: map[string]any{
			"event":    "msg-日本語",
			"payload":  map[string]any{"hello": "world"},
			"persist":  true,
			"smuggled": "dropped",
		},
	})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if !isBin || frame[0] != realtime.KindUserBroadcastPush {
		t.Fatalf("expected binary kind 3, got isBin=%v kind=%d", isBin, frame[0])
	}
	// Verify UTF-8 byte lengths
	topicBytes := []byte("realtime:café-🚀")
	eventBytes := []byte("msg-日本語")
	metaBytes := []byte(`{"persist":true}`)
	if int(frame[3]) != len(topicBytes) || int(frame[4]) != len(eventBytes) || int(frame[5]) != len(metaBytes) {
		t.Fatalf("header lengths mismatch: got %d,%d,%d want %d,%d,%d",
			frame[3], frame[4], frame[5], len(topicBytes), len(eventBytes), len(metaBytes))
	}
	if frame[6] != realtime.JSONEncoding {
		t.Fatalf("encoding = %d, want JSON (1)", frame[6])
	}

	// Reject header field > 255 UTF-8 bytes
	_, _, err = s.Encode(realtime.Message{
		Topic: "realtime:" + strings.Repeat("é", 130), // 260+ UTF-8 bytes
		Event: "broadcast",
		Payload: map[string]any{
			"event":   "ev",
			"payload": map[string]any{},
		},
	})
	if err == nil {
		t.Fatal("expected error for >255 UTF-8 byte topic")
	}

	// Decode kind 4 (server -> client binary userBroadcast) with binary payload
	rawBinPayload := []byte{0xde, 0xad, 0xbe, 0xef}
	kind4 := append([]byte{
		realtime.KindUserBroadcast,
		byte(len(topicBytes)),
		byte(len(eventBytes)),
		byte(len(metaBytes)),
		realtime.BinaryEncoding,
	}, topicBytes...)
	kind4 = append(kind4, eventBytes...)
	kind4 = append(kind4, metaBytes...)
	kind4 = append(kind4, rawBinPayload...)

	decoded, err := s.Decode(true, kind4)
	if err != nil {
		t.Fatalf("Decode kind 4 error: %v", err)
	}
	if decoded.Topic != "realtime:café-🚀" || decoded.Event != "broadcast" {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Payload["event"] != "msg-日本語" {
		t.Fatalf("event = %v", decoded.Payload["event"])
	}
	if !bytes.Equal(decoded.Payload["payload"].([]byte), rawBinPayload) {
		t.Fatalf("binary payload mismatch")
	}
	meta, ok := decoded.Payload["meta"].(map[string]any)
	if !ok || meta["persist"] != true {
		t.Fatalf("meta = %#v", decoded.Payload["meta"])
	}
}

func TestPostgresFilterBuilderAll14Operators(t *testing.T) {
	cases := []struct {
		got  string
		want string
	}{
		{realtime.NewPostgresFilterBuilder().Eq("id", 1).Build(), "id=eq.1"},
		{realtime.NewPostgresFilterBuilder().Neq("id", 1).String(), "id=neq.1"},
		{realtime.NewPostgresFilterBuilder().Gt("age", 18).Build(), "age=gt.18"},
		{realtime.NewPostgresFilterBuilder().Gte("age", 18).Build(), "age=gte.18"},
		{realtime.NewPostgresFilterBuilder().Lt("age", 65).Build(), "age=lt.65"},
		{realtime.NewPostgresFilterBuilder().Lte("age", 65).Build(), "age=lte.65"},
		{realtime.NewPostgresFilterBuilder().In("status", []any{"a", "b"}).Build(), "status=in.(a,b)"},
		{realtime.NewPostgresFilterBuilder().Like("name", "A%").Build(), "name=like.A%"},
		{realtime.NewPostgresFilterBuilder().ILike("name", "a%").Build(), "name=ilike.a%"},
		{realtime.NewPostgresFilterBuilder().Match("name", "^A").Build(), "name=match.^A"},
		{realtime.NewPostgresFilterBuilder().IMatch("name", "^a").Build(), "name=imatch.^a"},
		{realtime.NewPostgresFilterBuilder().Is("deleted_at", nil).Build(), "deleted_at=is.null"},
		{realtime.NewPostgresFilterBuilder().IsDistinct("flag", true).Build(), "flag=isdistinct.true"},
		{realtime.NewPostgresFilterBuilder().Not("status", "eq", "archived").Build(), "status=not.eq.archived"},
		// Chaining, quoting, In deduplication, Not(in/is)
		{realtime.NewPostgresFilterBuilder().Eq("a", 1).Lt("b", 2).Build(), "a=eq.1,b=lt.2"},
		{realtime.NewPostgresFilterBuilder().Eq("name", "a,b").Build(), `name=eq."a,b"`},
		{realtime.NewPostgresFilterBuilder().Eq("name", " spaced ").Build(), `name=eq." spaced "`},
		{realtime.NewPostgresFilterBuilder().In("status", []any{"x", "y", "x", "a,b"}).Build(), `status=in.(x,y,"a,b")`},
		{realtime.NewPostgresFilterBuilder().Not("status", "in", []any{"x", "y", "x"}).Build(), "status=not.in.(x,y)"},
		{realtime.NewPostgresFilterBuilder().Not("deleted_at", "is", nil).Build(), "deleted_at=not.is.null"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestRealtimeEndToEndWebSocketAndHTTPSend(t *testing.T) {
	s := realtime.NewSerializer()

	var (
		mu              sync.Mutex
		seenJoinPayload map[string]any
		seenAccessToken string
		seenHTTPBody    map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realtime/v1/api/broadcast/room1/events/chat" {
			if r.URL.Query().Get("private") != "true" || r.URL.Query().Get("persist") != "true" {
				t.Errorf("HTTPSend query = %q, want private=true&persist=true", r.URL.RawQuery)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			seenHTTPBody = body
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.URL.Path == "/realtime/v1/api/broadcast/room1/events/bin" {
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("binary HTTPSend Content-Type = %q", r.Header.Get("Content-Type"))
			}
			if r.URL.Query().Get("persist[ttl]") != "60" {
				t.Errorf("binary HTTPSend persist[ttl] = %q, want 60", r.URL.Query().Get("persist[ttl]"))
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()

		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			isBin := typ == websocket.MessageBinary
			if isBin {
				// Echo back as kind 4 userBroadcast and send phx_reply ack
				// Frame kind 3: [3, joinRefLen, refLen, topicLen, eventLen, metaLen, enc, ...]
				jLen, rLen, tLen, eLen, mLen := int(data[1]), int(data[2]), int(data[3]), int(data[4]), int(data[5])
				enc := data[6]
				jRef := string(data[7 : 7+jLen])
				ref := string(data[7+jLen : 7+jLen+rLen])
				off := 7 + jLen + rLen
				topic := data[off : off+tLen]
				off += tLen
				ev := data[off : off+eLen]
				off += eLen
				meta := data[off : off+mLen]
				off += mLen
				payload := data[off:]

				_, ackReply, _ := s.Encode(realtime.Message{
					JoinRef: jRef,
					Ref:     ref,
					Topic:   string(topic),
					Event:   "phx_reply",
					Payload: map[string]any{"status": "ok", "response": map[string]any{}},
				})
				_ = c.Write(ctx, websocket.MessageText, ackReply)

				resp := append([]byte{realtime.KindUserBroadcast, byte(tLen), byte(eLen), byte(mLen), enc}, topic...)
				resp = append(resp, ev...)
				resp = append(resp, meta...)
				resp = append(resp, payload...)
				_ = c.Write(ctx, websocket.MessageBinary, resp)
				continue
			}

			msg, err := s.Decode(false, data)
			if err != nil {
				continue
			}
			switch msg.Event {
			case "heartbeat":
				_, reply, _ := s.Encode(realtime.Message{
					Ref:     msg.Ref,
					Topic:   "phoenix",
					Event:   "phx_reply",
					Payload: map[string]any{"status": "ok", "response": map[string]any{}},
				})
				_ = c.Write(ctx, websocket.MessageText, reply)
			case "phx_join":
				mu.Lock()
				seenJoinPayload = msg.Payload
				mu.Unlock()
				_, reply, _ := s.Encode(realtime.Message{
					JoinRef: msg.JoinRef,
					Ref:     msg.Ref,
					Topic:   msg.Topic,
					Event:   "phx_reply",
					Payload: map[string]any{"status": "ok", "response": map[string]any{}},
				})
				_ = c.Write(ctx, websocket.MessageText, reply)

				// Push presence_state then presence_diff
				_, ps, _ := s.Encode(realtime.Message{
					Topic: msg.Topic,
					Event: "presence_state",
					Payload: map[string]any{
						"user1": map[string]any{"metas": []any{map[string]any{"phx_ref": "r1", "online": true}}},
					},
				})
				_ = c.Write(ctx, websocket.MessageText, ps)

				_, pd, _ := s.Encode(realtime.Message{
					Topic: msg.Topic,
					Event: "presence_diff",
					Payload: map[string]any{
						"joins":  map[string]any{"user2": map[string]any{"metas": []any{map[string]any{"phx_ref": "r2"}}}},
						"leaves": map[string]any{"user1": map[string]any{"metas": []any{map[string]any{"phx_ref": "r1"}}}},
					},
				})
				_ = c.Write(ctx, websocket.MessageText, pd)
			case "access_token":
				mu.Lock()
				seenAccessToken, _ = msg.Payload["access_token"].(string)
				mu.Unlock()
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/realtime/v1"
	client := realtime.New(wsURL,
		realtime.WithAPIKey("test-key"),
		realtime.WithHTTPClient(srv.Client()),
		realtime.WithHeartbeatInterval(30*time.Millisecond),
		realtime.WithLogger(transport.NopLogger{}),
	)
	if !client.HasLogger() {
		t.Fatal("expected HasLogger() == true")
	}

	hbOK := make(chan struct{}, 1)
	client.OnHeartbeat(func(status string) {
		if status == "ok" {
			select {
			case hbOK <- struct{}{}:
			default:
			}
		}
	})

	// Test HTTPSend before subscribing
	ch := client.Channel("room1", realtime.ChannelConfig{
		Broadcast: realtime.BroadcastConfig{Ack: true, Self: true},
		Presence:  realtime.PresenceConfig{Key: "user1"},
		Private:   true,
	})
	if err := ch.Send(context.Background(), realtime.SendPayload{
		Type:    "broadcast",
		Event:   "chat",
		Payload: map[string]any{"text": "hi via http"},
		Persist: true,
	}); err != nil {
		t.Fatalf("HTTP fallback Send error: %v", err)
	}
	mu.Lock()
	if seenHTTPBody == nil || seenHTTPBody["text"] != "hi via http" {
		t.Fatalf("expected HTTPSend to hit /realtime/v1/api/broadcast/room1/events/chat, got %+v", seenHTTPBody)
	}
	mu.Unlock()

	// Binary HTTPSend with TTL
	if err := ch.HTTPSend(context.Background(), "bin", []byte{1, 2, 3}, realtime.HTTPSendOptions{PersistTTL: 60}); err != nil {
		t.Fatalf("binary HTTPSend error: %v", err)
	}
	// Public channel must reject persist
	pubCh := client.Channel("public-room")
	if err := pubCh.HTTPSend(context.Background(), "ev", map[string]any{"a": 1}, realtime.HTTPSendOptions{Persist: true}); err == nil {
		t.Fatal("expected error for persist on public channel")
	}
	_ = client.RemoveChannel(context.Background(), pubCh)

	// Register two postgres_changes filters + broadcast + presence listeners
	ch.OnPostgresChanges(realtime.PostgresChangesFilter{
		Event:  "INSERT",
		Schema: "public",
		Table:  "messages",
		Filter: realtime.NewPostgresFilterBuilder().Eq("room_id", 1).Build(),
	}, func(map[string]any) {})
	ch.OnPostgresChanges(realtime.PostgresChangesFilter{
		Event:  "UPDATE",
		Schema: "public",
		Table:  "messages",
		Filter: realtime.NewPostgresFilterBuilder().Neq("status", "deleted").Build(),
	}, func(map[string]any) {})

	broadcastCh := make(chan map[string]any, 1)
	ch.OnBroadcast("ping", func(p map[string]any) {
		select {
		case broadcastCh <- p:
		default:
		}
	})

	presenceSynced := make(chan struct{}, 4)
	ch.OnPresence("sync", func(ev realtime.PresenceDiffEvent) {
		select {
		case presenceSynced <- struct{}{}:
		default:
		}
	})

	subscribedCh := make(chan realtime.SubscribeStatus, 1)
	if err := ch.Subscribe(context.Background(), func(status realtime.SubscribeStatus, err error) {
		if status == realtime.SubscribeSubscribed {
			select {
			case subscribedCh <- status:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("Subscribe error: %v", err)
	}

	select {
	case <-subscribedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SUBSCRIBED")
	}

	// Verify multi-filter postgres_changes in join payload
	mu.Lock()
	cfgMap := seenJoinPayload["config"].(map[string]any)
	pgList := cfgMap["postgres_changes"].([]any)
	mu.Unlock()
	if len(pgList) != 2 {
		t.Fatalf("expected 2 postgres_changes filters, got %d", len(pgList))
	}

	// Wait for presence state + diff
	<-presenceSynced
	<-presenceSynced
	ps := ch.PresenceState()
	if _, hasU1 := ps["user1"]; hasU1 {
		t.Fatalf("expected user1 to have left, state=%+v", ps)
	}
	if _, hasU2 := ps["user2"]; !hasU2 {
		t.Fatalf("expected user2 in presence state, state=%+v", ps)
	}
	if ps["user2"][0]["presence_ref"] != "r2" {
		t.Fatalf("expected presence_ref=r2, got %+v", ps["user2"][0])
	}
	if _, hasPhxRef := ps["user2"][0]["phx_ref"]; hasPhxRef {
		t.Fatalf("expected phx_ref stripped, got %+v", ps["user2"][0])
	}

	// Send binary v2 broadcast over WS and receive echo
	if err := ch.Send(context.Background(), realtime.SendPayload{
		Type:    "broadcast",
		Event:   "ping",
		Payload: map[string]any{"n": 1},
		Persist: true,
	}); err != nil {
		t.Fatalf("WS Send error: %v", err)
	}
	select {
	case got := <-broadcastCh:
		if got["event"] != "ping" {
			t.Fatalf("broadcast got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast echo")
	}

	// SetAuth pushes access_token to joined channel
	client.SetAuth("refreshed-jwt")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		tok := seenAccessToken
		mu.Unlock()
		if tok == "refreshed-jwt" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if seenAccessToken != "refreshed-jwt" {
		t.Fatalf("seenAccessToken = %q, want refreshed-jwt", seenAccessToken)
	}
	mu.Unlock()

	// Wait for heartbeat ok
	select {
	case <-hbOK:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat ok")
	}

	if err := client.RemoveAllChannels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if client.ConnectionState() != realtime.StateClosed {
		t.Fatalf("ConnectionState = %q, want closed", client.ConnectionState())
	}
}

type flakyTransport struct {
	mu       sync.Mutex
	attempts int
	failN    int
	conns    []*fakeConn
}

type fakeConn struct {
	readCh chan []byte
	closed chan struct{}
	once   sync.Once
}

func newFakeConn() *fakeConn {
	return &fakeConn{readCh: make(chan []byte, 8), closed: make(chan struct{})}
}

func (f *fakeConn) ReadMessage(ctx context.Context) (bool, []byte, error) {
	select {
	case <-ctx.Done():
		return false, nil, ctx.Err()
	case <-f.closed:
		return false, nil, context.Canceled
	case b := <-f.readCh:
		return false, b, nil
	}
}

func (f *fakeConn) WriteMessage(_ context.Context, _ bool, _ []byte) error { return nil }
func (f *fakeConn) Close(_ int, _ string) error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (ft *flakyTransport) Dial(_ context.Context, _ string, _ http.Header) (realtime.Conn, error) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.attempts++
	if ft.attempts > 1 && ft.attempts <= 1+ft.failN {
		return nil, context.DeadlineExceeded
	}
	fc := newFakeConn()
	ft.conns = append(ft.conns, fc)
	return fc, nil
}

func TestRealtimeV1FramingReconnectDeferredDisconnectAndPresence(t *testing.T) {
	// 1. Invalid vsn rejected
	bad := realtime.New("ws://localhost:1234/realtime/v1", realtime.WithVsn("9.9.9"))
	if err := bad.Connect(context.Background()); err == nil {
		t.Fatal("expected error for unsupported vsn")
	}

	// 2. Phoenix v1.0.0 JSON object framing round-trip
	var v1GotObj map[string]any
	v1Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		typ, data, err := c.Read(r.Context())
		if err != nil || typ != websocket.MessageText {
			return
		}
		_ = json.Unmarshal(data, &v1GotObj)
		reply, _ := json.Marshal(map[string]any{
			"topic":    v1GotObj["topic"],
			"event":    "phx_reply",
			"ref":      v1GotObj["ref"],
			"join_ref": v1GotObj["join_ref"],
			"payload":  map[string]any{"status": "ok", "response": map[string]any{}},
		})
		_ = c.Write(r.Context(), websocket.MessageText, reply)
		time.Sleep(100 * time.Millisecond)
	}))
	defer v1Srv.Close()

	v1Client := realtime.New("ws"+strings.TrimPrefix(v1Srv.URL, "http")+"/realtime/v1",
		realtime.WithVsn("1.0.0"),
		realtime.WithHTTPClient(v1Srv.Client()),
	)
	v1Joined := make(chan struct{}, 1)
	v1Ch := v1Client.Channel("v1room")
	if err := v1Ch.Subscribe(context.Background(), func(s realtime.SubscribeStatus, _ error) {
		if s == realtime.SubscribeSubscribed {
			v1Joined <- struct{}{}
		}
	}); err != nil {
		t.Fatalf("v1 Subscribe error: %v", err)
	}
	select {
	case <-v1Joined:
	case <-time.After(2 * time.Second):
		t.Fatal("v1 join timed out")
	}
	if v1GotObj["event"] != "phx_join" || v1GotObj["topic"] != "realtime:v1room" {
		t.Fatalf("v1 wire object = %+v", v1GotObj)
	}
	_ = v1Client.Disconnect()

	// 3. Multi-attempt reconnect loop + deferred disconnect on empty channels
	ft := &flakyTransport{failN: 2}
	var triesSeen []int
	var triesMu sync.Mutex
	rc := realtime.New("ws://fake/realtime/v1",
		realtime.WithTransport(ft),
		realtime.WithDeferredDisconnect(40*time.Millisecond),
		realtime.WithReconnectAfterMs(func(tries int) time.Duration {
			triesMu.Lock()
			triesSeen = append(triesSeen, tries)
			triesMu.Unlock()
			return 5 * time.Millisecond
		}),
	)
	if err := rc.Connect(context.Background()); err != nil {
		t.Fatalf("initial Connect: %v", err)
	}
	// Drop initial connection to trigger 2 failed retries + 3rd succeeding
	ft.mu.Lock()
	firstConn := ft.conns[0]
	ft.mu.Unlock()
	_ = firstConn.Close(1006, "drop")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rc.ConnectionState() == realtime.StateOpen {
			ft.mu.Lock()
			n := len(ft.conns)
			ft.mu.Unlock()
			if n == 2 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	triesMu.Lock()
	if len(triesSeen) < 3 || triesSeen[0] != 1 || triesSeen[1] != 2 || triesSeen[2] != 3 {
		t.Fatalf("expected reconnect tries [1 2 3], got %v", triesSeen)
	}
	triesMu.Unlock()

	// Deferred disconnect: removing last channel schedules disconnect; adding new channel cancels it
	ch1 := rc.Channel("c1")
	_ = rc.RemoveChannel(context.Background(), ch1)
	time.Sleep(10 * time.Millisecond)
	ch2 := rc.Channel("c2") // cancels timer
	time.Sleep(50 * time.Millisecond)
	if rc.ConnectionState() != realtime.StateOpen {
		t.Fatalf("expected timer cancelled by new channel, state=%s", rc.ConnectionState())
	}
	_ = rc.RemoveChannel(context.Background(), ch2) // now empty -> disconnects after 40ms
	time.Sleep(80 * time.Millisecond)
	if rc.ConnectionState() != realtime.StateClosed {
		t.Fatalf("expected deferred disconnect after last channel removed, state=%s", rc.ConnectionState())
	}

	// 4. Presence buffers early diff before initial state and emits leaves on re-sync
	var rp realtime.RealtimePresence
	if evs := rp.ApplyDiff(map[string]any{
		"joins": map[string]any{"u2": map[string]any{"metas": []any{map[string]any{"phx_ref": "r2"}}}},
	}); len(evs) != 0 {
		t.Fatalf("expected early diff buffered before state, got %+v", evs)
	}
	_ = rp.ApplyState(map[string]any{
		"u1": map[string]any{"metas": []any{map[string]any{"phx_ref": "r1"}}},
	})
	st := rp.State()
	if len(st["u1"]) != 1 || len(st["u2"]) != 1 || st["u1"][0]["presence_ref"] != "r1" || st["u2"][0]["presence_ref"] != "r2" {
		t.Fatalf("expected buffered u2 + synced u1 with presence_ref, got %+v", st)
	}
	resyncEvents := rp.ApplyState(map[string]any{
		"u2": map[string]any{"metas": []any{map[string]any{"phx_ref": "r2"}}},
	})
	foundLeave := false
	for _, ev := range resyncEvents {
		if ev.Event == "leave" && ev.Key == "u1" {
			foundLeave = true
		}
	}
	if !foundLeave {
		t.Fatalf("expected leave event for u1 on re-sync, got %+v", resyncEvents)
	}
}
