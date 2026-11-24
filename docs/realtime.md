# Realtime (`realtime`)

The `realtime` package implements a pure-Go Phoenix Channels WebSocket client (supporting JSON v1 and Binary v2 framing) for **Broadcast**, **Presence**, and **Postgres Changes**, plus stateless REST **HTTP Broadcast** (`HTTPSend`).

- **Package:** `github.com/rusmanplatd/goravel-supabase/realtime`
- **Access via Client:** `sb.Channel(...)`, `sb.GetChannels()`, `sb.RemoveChannel(...)`, `sb.RemoveAllChannels(...)`, or `sb.Realtime` / `facades.Supabase().Realtime()`

---

## 1. WebSocket Broadcast (Full Example)

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/realtime"
)

func main() {
	sb, err := client.New(
		"https://xyzcompany.supabase.co",
		"sb_publishable_...",
		client.WithRealtimeHeartbeatInterval(25*time.Second),
		client.WithRealtimeVsn("2.0.0"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// Monitor WebSocket heartbeats
	sb.Realtime.OnHeartbeat(func(status string) {
		fmt.Println("Heartbeat:", status)
	})

	ch := sb.Channel("room:lobby", realtime.ChannelConfig{
		Broadcast: realtime.BroadcastConfig{
			Self: true,
			Ack:  true,
		},
		Private: false,
	})

	// Register broadcast listener before calling Subscribe
	ch.OnBroadcast("cursor-move", func(payload map[string]any) {
		fmt.Println("Cursor update:", payload)
	})

	if err := ch.Subscribe(ctx, func(status realtime.SubscribeStatus, err error) {
		fmt.Printf("Channel %s status: %s (err=%v)\n", ch.Topic(), status, err)
	}); err != nil {
		log.Fatal(err)
	}

	// Send broadcast message over WebSocket
	err = ch.Send(ctx, realtime.SendPayload{
		Type:    "broadcast",
		Event:   "cursor-move",
		Payload: map[string]any{"user": "ada", "x": 120, "y": 240},
	})
	if err != nil {
		log.Fatal(err)
	}

	time.Sleep(2 * time.Second)
	_ = sb.RemoveChannel(ctx, ch)
}
```

---

## 2. Postgres Changes (`OnPostgresChanges` + `RealtimePostgresFilterBuilder`)

Subscribe to database `INSERT`, `UPDATE`, `DELETE`, or `*` events:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/realtime"
)

func main() {
	sb, err := client.New("https://xyzcompany.supabase.co", "sb_publishable_...")
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	filter := realtime.NewPostgresFilterBuilder().
		Eq("room_id", 42).
		Build()

	ch := sb.Channel("db-messages")
	ch.OnPostgresChanges(realtime.PostgresChangesFilter{
		Event:  "INSERT",
		Schema: "public",
		Table:  "messages",
		Filter: filter,
	}, func(payload map[string]any) {
		fmt.Println("New message inserted:", payload)
	})

	ch.OnPostgresChanges(realtime.PostgresChangesFilter{
		Event:  "UPDATE",
		Schema: "public",
		Table:  "messages",
	}, func(payload map[string]any) {
		fmt.Println("Message updated:", payload)
	})

	if err := ch.Subscribe(ctx); err != nil {
		log.Fatal(err)
	}

	<-ctx.Done()
	_ = sb.RemoveAllChannels(context.Background())
}
```

---

## 3. Presence (`Track`, `Untrack`, `OnPresence`, `PresenceState`)

```go
ctx := context.Background()

ch := sb.Channel("room:presence", realtime.ChannelConfig{
	Presence: realtime.PresenceConfig{Key: "user-ada"},
})

ch.OnPresence("sync", func(ev realtime.PresenceDiffEvent) {
	fmt.Println("Synced presence state:", ch.PresenceState())
})

ch.OnPresence("join", func(ev realtime.PresenceDiffEvent) {
	fmt.Printf("Joined %s: %+v\n", ev.Key, ev.NewPresences)
})

ch.OnPresence("leave", func(ev realtime.PresenceDiffEvent) {
	fmt.Printf("Left %s: %+v\n", ev.Key, ev.LeftPresences)
})

if err := ch.Subscribe(ctx); err != nil {
	log.Fatal(err)
}

// Track local presence metadata
if err := ch.Track(ctx, map[string]any{
	"username":  "ada",
	"online_at": time.Now().UTC().Format(time.RFC3339),
}); err != nil {
	log.Fatal(err)
}

// Later, untrack when leaving
_ = ch.Untrack(ctx)
```

---

## 4. Stateless HTTP Broadcast (`HTTPSend` in Goravel)

Send broadcast events from HTTP controllers or background jobs without maintaining a WebSocket connection:

```go
package controllers

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/rusmanplatd/goravel-supabase/facades"
	"github.com/rusmanplatd/goravel-supabase/realtime"
)

type NotificationController struct{}

func (c *NotificationController) Broadcast(ctx http.Context) http.Response {
	sb := facades.Supabase()
	ch := sb.Channel("room:announcements", realtime.ChannelConfig{
		Private: true,
	})

	err := ch.HTTPSend(ctx.Context(), "system_alert", map[string]any{
		"title":   "Maintenance Window",
		"message": "Scheduled upgrade at 02:00 UTC",
	}, realtime.HTTPSendOptions{
		Persist:    true,
		PersistTTL: 3600,
	})
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	return ctx.Response().Success().Json(http.Json{"broadcasted": true})
}
```
