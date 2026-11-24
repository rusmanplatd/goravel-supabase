package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// ChannelState represents the lifecycle state of a RealtimeChannel.
type ChannelState string

const (
	ChannelClosed  ChannelState = "closed"
	ChannelJoining ChannelState = "joining"
	ChannelJoined  ChannelState = "joined"
	ChannelLeaving ChannelState = "leaving"
	ChannelErrored ChannelState = "errored"
)

// SubscribeStatus is passed to Channel.Subscribe callbacks.
type SubscribeStatus string

const (
	SubscribeSubscribed   SubscribeStatus = "SUBSCRIBED"
	SubscribeTimedOut     SubscribeStatus = "TIMED_OUT"
	SubscribeClosed       SubscribeStatus = "CLOSED"
	SubscribeChannelError SubscribeStatus = "CHANNEL_ERROR"
)

// BroadcastConfig configures channel broadcast behavior.
type BroadcastConfig struct {
	Ack    bool           `json:"ack,omitempty"`
	Self   bool           `json:"self,omitempty"`
	Replay map[string]any `json:"replay,omitempty"`
}

// PresenceConfig configures channel presence key.
type PresenceConfig struct {
	Key string `json:"key,omitempty"`
}

// ChannelConfig configures a RealtimeChannel.
type ChannelConfig struct {
	Broadcast BroadcastConfig `json:"broadcast"`
	Presence  PresenceConfig  `json:"presence"`
	Private   bool            `json:"private,omitempty"`
}

// PostgresChangesFilter specifies a postgres_changes subscription binding.
type PostgresChangesFilter struct {
	Event  string `json:"event"`            // "*", "INSERT", "UPDATE", "DELETE"
	Schema string `json:"schema,omitempty"` // e.g. "public"
	Table  string `json:"table,omitempty"`
	Filter string `json:"filter,omitempty"`
}

// SendPayload is sent via Channel.Send or Channel.HTTPSend.
type SendPayload struct {
	Type       string         `json:"type"` // "broadcast" | "presence" | "postgres_changes"
	Event      string         `json:"event"`
	Payload    any            `json:"payload,omitempty"`
	Persist    bool           `json:"persist,omitempty"`
	PersistTTL int            `json:"-"`
	Extra      map[string]any `json:"-"`
}

// HTTPSendOptions configures Channel.HTTPSend.
type HTTPSendOptions struct {
	Persist    bool
	PersistTTL int
}

type channelBinding struct {
	kind           string // "broadcast", "presence", "postgres_changes", "system"
	event          string
	postgresFilter *PostgresChangesFilter
	callback       func(payload map[string]any)
}

// Channel represents a Phoenix Realtime channel supporting Broadcast, Presence, and Postgres Changes.
type Channel struct {
	client *Client
	topic  string
	config ChannelConfig

	mu          sync.RWMutex
	state       ChannelState
	joinRef     string
	bindings    []channelBinding
	presence    *RealtimePresence
	subscribeCb func(status SubscribeStatus, err error)
}

func newChannel(client *Client, name string, cfg ChannelConfig) *Channel {
	topic := name
	if !strings.HasPrefix(topic, "realtime:") {
		topic = "realtime:" + topic
	}
	return &Channel{
		client:   client,
		topic:    topic,
		config:   cfg,
		state:    ChannelClosed,
		presence: newRealtimePresence(),
	}
}

// Topic returns the full Phoenix channel topic (`realtime:<name>`).
func (ch *Channel) Topic() string { return ch.topic }

// State returns the current channel state.
func (ch *Channel) State() ChannelState {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.state
}

// PresenceState returns a snapshot of the channel's current presence state.
func (ch *Channel) PresenceState() PresenceState {
	return ch.presence.State()
}

// OnBroadcast registers a listener for broadcast events matching event (or "*" for all).
func (ch *Channel) OnBroadcast(event string, cb func(payload map[string]any)) *Channel {
	ch.mu.Lock()
	ch.bindings = append(ch.bindings, channelBinding{kind: "broadcast", event: event, callback: cb})
	ch.mu.Unlock()
	return ch
}

// OnPresence registers a listener for presence events ("sync", "join", "leave").
func (ch *Channel) OnPresence(event string, cb func(ev PresenceDiffEvent)) *Channel {
	ch.mu.Lock()
	ch.bindings = append(ch.bindings, channelBinding{
		kind:  "presence",
		event: event,
		callback: func(p map[string]any) {
			if ev, ok := p["_presence_event"].(PresenceDiffEvent); ok {
				cb(ev)
			}
		},
	})
	ch.mu.Unlock()
	return ch
}

// OnPostgresChanges registers a Postgres Changes listener (may be called multiple times for multi-filter channels).
func (ch *Channel) OnPostgresChanges(filter PostgresChangesFilter, cb func(payload map[string]any)) *Channel {
	if filter.Event == "" {
		filter.Event = "*"
	}
	if filter.Schema == "" {
		filter.Schema = "public"
	}
	fCopy := filter
	ch.mu.Lock()
	ch.bindings = append(ch.bindings, channelBinding{
		kind:           "postgres_changes",
		event:          filter.Event,
		postgresFilter: &fCopy,
		callback:       cb,
	})
	ch.mu.Unlock()
	return ch
}

// On registers a generic event listener ("broadcast", "presence", "postgres_changes", "system").
func (ch *Channel) On(kind, event string, cb func(payload map[string]any)) *Channel {
	ch.mu.Lock()
	ch.bindings = append(ch.bindings, channelBinding{kind: kind, event: event, callback: cb})
	ch.mu.Unlock()
	return ch
}

// Subscribe connects the socket if needed and sends `phx_join` for this channel.
func (ch *Channel) Subscribe(ctx context.Context, cb ...func(status SubscribeStatus, err error)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(cb) > 0 && cb[0] != nil {
		ch.mu.Lock()
		ch.subscribeCb = cb[0]
		ch.mu.Unlock()
	}

	if ch.client.ConnectionState() != StateOpen {
		if err := ch.client.Connect(ctx); err != nil {
			return err
		}
	}

	return ch.sendJoin(ctx)
}

func (ch *Channel) sendJoin(ctx context.Context) error {
	ref := ch.client.nextRef()

	ch.mu.Lock()
	ch.state = ChannelJoining
	ch.joinRef = ref
	var pgChanges []PostgresChangesFilter
	for _, b := range ch.bindings {
		if b.kind == "postgres_changes" && b.postgresFilter != nil {
			pgChanges = append(pgChanges, *b.postgresFilter)
		}
	}
	cfg := ch.config
	ch.mu.Unlock()

	if pgChanges == nil {
		pgChanges = []PostgresChangesFilter{}
	}

	joinPayload := map[string]any{
		"config": map[string]any{
			"broadcast":        cfg.Broadcast,
			"presence":         cfg.Presence,
			"postgres_changes": pgChanges,
			"private":          cfg.Private,
		},
	}
	if tok := ch.client.resolveToken(ctx); tok != "" {
		joinPayload["access_token"] = tok
	}

	return ch.client.push(ctx, Message{
		JoinRef: ref,
		Ref:     ref,
		Topic:   ch.topic,
		Event:   "phx_join",
		Payload: joinPayload,
	})
}

// Unsubscribe sends `phx_leave` and marks the channel closed.
func (ch *Channel) Unsubscribe(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ch.mu.Lock()
	joinRef := ch.joinRef
	ch.state = ChannelLeaving
	cb := ch.subscribeCb
	ch.mu.Unlock()

	_ = ch.client.push(ctx, Message{
		JoinRef: joinRef,
		Ref:     ch.client.nextRef(),
		Topic:   ch.topic,
		Event:   "phx_leave",
		Payload: map[string]any{},
	})

	ch.mu.Lock()
	ch.state = ChannelClosed
	ch.mu.Unlock()
	if cb != nil {
		cb(SubscribeClosed, nil)
	}
	return nil
}

// Send transmits a broadcast or presence message over WebSocket (or falls back to HTTPSend for
// broadcast when the channel is not joined).
func (ch *Channel) Send(ctx context.Context, msg SendPayload) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg.Type == "" {
		msg.Type = BroadcastEvent
	}

	ch.mu.RLock()
	state := ch.state
	joinRef := ch.joinRef
	ch.mu.RUnlock()

	if msg.Type == BroadcastEvent && state != ChannelJoined {
		return ch.HTTPSend(ctx, msg.Event, msg.Payload, HTTPSendOptions{
			Persist:    msg.Persist,
			PersistTTL: msg.PersistTTL,
		})
	}

	payload := map[string]any{
		"type":    msg.Type,
		"event":   msg.Event,
		"payload": msg.Payload,
	}
	if msg.Persist {
		payload["persist"] = true
	}
	for k, v := range msg.Extra {
		payload[k] = v
	}

	return ch.client.push(ctx, Message{
		JoinRef: joinRef,
		Ref:     ch.client.nextRef(),
		Topic:   ch.topic,
		Event:   msg.Type,
		Payload: payload,
	})
}

// HTTPSend dispatches a broadcast message via the Realtime REST endpoint
// `/api/broadcast/{subTopic}/events/{event}`.
func (ch *Channel) HTTPSend(ctx context.Context, event string, payload any, opts ...HTTPSendOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if payload == nil {
		return errors.New("realtime: HTTPSend payload must not be nil")
	}
	var opt HTTPSendOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if (opt.Persist || opt.PersistTTL > 0) && !ch.config.Private {
		return errors.New("realtime: persist is only supported on private channels")
	}

	subTopic := strings.TrimPrefix(ch.topic, "realtime:")
	endpoint := ch.client.httpBroadcastURL() + "/" + url.PathEscape(subTopic) + "/events/" + url.PathEscape(event)
	q := url.Values{}
	if ch.config.Private {
		q.Set("private", "true")
	}
	if opt.PersistTTL > 0 {
		q.Set("persist[ttl]", strconv.Itoa(opt.PersistTTL))
	} else if opt.Persist {
		q.Set("persist", "true")
	}
	if enc := q.Encode(); enc != "" {
		endpoint += "?" + enc
	}

	var (
		bodyBytes   []byte
		contentType string
		err         error
	)
	if bin, ok := payload.([]byte); ok {
		bodyBytes = bin
		contentType = "application/octet-stream"
	} else {
		bodyBytes, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		contentType = "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if ch.client.apiKey != "" {
		req.Header.Set("apikey", ch.client.apiKey)
	}
	if tok := ch.client.resolveToken(ctx); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range ch.client.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := ch.client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("realtime: HTTPSend endpoint not found (404, check server version): %s", string(raw))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("realtime: HTTPSend failed (%d): %s", resp.StatusCode, string(raw))
	}
	return nil
}

// Track publishes presence metadata for the current client on this channel.
func (ch *Channel) Track(ctx context.Context, payload map[string]any) error {
	ch.mu.RLock()
	state := ch.state
	joinRef := ch.joinRef
	ch.mu.RUnlock()
	if state != ChannelJoined {
		return errors.New("realtime: cannot track presence before channel is joined")
	}
	return ch.client.push(ctx, Message{
		JoinRef: joinRef,
		Ref:     ch.client.nextRef(),
		Topic:   ch.topic,
		Event:   "presence",
		Payload: map[string]any{
			"type":    "presence",
			"event":   "track",
			"payload": payload,
		},
	})
}

// Untrack removes this client's presence from the channel.
func (ch *Channel) Untrack(ctx context.Context) error {
	ch.mu.RLock()
	joinRef := ch.joinRef
	ch.mu.RUnlock()
	return ch.client.push(ctx, Message{
		JoinRef: joinRef,
		Ref:     ch.client.nextRef(),
		Topic:   ch.topic,
		Event:   "presence",
		Payload: map[string]any{
			"type":  "presence",
			"event": "untrack",
		},
	})
}

func (ch *Channel) pushAccessToken(ctx context.Context, token string) {
	ch.mu.RLock()
	state := ch.state
	joinRef := ch.joinRef
	ch.mu.RUnlock()
	if state != ChannelJoined {
		return
	}
	_ = ch.client.push(ctx, Message{
		JoinRef: joinRef,
		Ref:     ch.client.nextRef(),
		Topic:   ch.topic,
		Event:   "access_token",
		Payload: map[string]any{"access_token": token},
	})
}

func (ch *Channel) handleMessage(msg *Message) {
	switch msg.Event {
	case "phx_reply":
		status, _ := msg.Payload["status"].(string)
		ch.mu.Lock()
		isJoinReply := msg.Ref != "" && msg.Ref == ch.joinRef
		cb := ch.subscribeCb
		if isJoinReply {
			if status == "ok" {
				ch.state = ChannelJoined
			} else {
				ch.state = ChannelErrored
			}
		}
		ch.mu.Unlock()
		if isJoinReply && cb != nil {
			if status == "ok" {
				cb(SubscribeSubscribed, nil)
			} else {
				cb(SubscribeChannelError, fmt.Errorf("realtime: join failed: %v", msg.Payload["response"]))
			}
		}

	case BroadcastEvent:
		userEvent, _ := msg.Payload["event"].(string)
		ch.dispatchBindings("broadcast", userEvent, msg.Payload)

	case "presence_state":
		events := ch.presence.ApplyState(msg.Payload)
		for _, ev := range events {
			ch.dispatchBindings("presence", ev.Event, map[string]any{"_presence_event": ev})
		}

	case "presence_diff":
		events := ch.presence.ApplyDiff(msg.Payload)
		for _, ev := range events {
			ch.dispatchBindings("presence", ev.Event, map[string]any{"_presence_event": ev})
		}

	case "postgres_changes":
		data, _ := msg.Payload["data"].(map[string]any)
		evType, _ := data["type"].(string)
		ch.dispatchBindings("postgres_changes", evType, msg.Payload)
	}
}

func (ch *Channel) dispatchBindings(kind, event string, payload map[string]any) {
	ch.mu.RLock()
	var matched []func(map[string]any)
	for _, b := range ch.bindings {
		if b.kind != kind {
			continue
		}
		if b.event == "*" || b.event == "" || strings.EqualFold(b.event, event) {
			matched = append(matched, b.callback)
		}
	}
	ch.mu.RUnlock()
	for _, fn := range matched {
		if fn != nil {
			fn(payload)
		}
	}
}
