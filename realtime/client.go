package realtime

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rusmanplatd/goravel-supabase/internal/transport"
)

// ConnectionState represents the WebSocket connection state.
type ConnectionState string

const (
	StateConnecting ConnectionState = "connecting"
	StateOpen       ConnectionState = "open"
	StateClosing    ConnectionState = "closing"
	StateClosed     ConnectionState = "closed"
)

// Client is a pure-Go Supabase Realtime Phoenix Channels WebSocket client.
type Client struct {
	endpointURL       string
	apiKey            string
	vsn               string
	headers           map[string]string
	httpClient        *http.Client
	transport         Transport
	serializer        *Serializer
	heartbeatInterval time.Duration
	reconnectAfterMs  func(tries int) time.Duration
	disconnectDelay   time.Duration
	accessTokenFn     func(ctx context.Context) (string, error)
	logger            transport.Logger
	hasCustomLogger   bool

	mu                  sync.RWMutex
	conn                Conn
	state               ConnectionState
	accessToken         string
	channels            []*Channel
	onHeartbeat         func(status string)
	writeMu             sync.Mutex
	refCounter          atomic.Uint64
	loopCancel          context.CancelFunc
	loopWg              sync.WaitGroup
	disconnectTimer     *time.Timer
	intentionalClose    bool
	reconnectTries      int
	pendingHeartbeatRef string
}

// Option configures a Realtime Client.
type Option func(*Client)

func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

func WithHeaders(h map[string]string) Option {
	return func(c *Client) {
		for k, v := range h {
			c.headers[k] = v
		}
	}
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

func WithTransport(t Transport) Option {
	return func(c *Client) {
		if t != nil {
			c.transport = t
		}
	}
}

func WithVsn(vsn string) Option {
	return func(c *Client) {
		if vsn != "" {
			c.vsn = vsn
		}
	}
}

func WithHeartbeatInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.heartbeatInterval = d
		}
	}
}

func WithReconnectAfterMs(fn func(tries int) time.Duration) Option {
	return func(c *Client) {
		if fn != nil {
			c.reconnectAfterMs = fn
		}
	}
}

func WithDeferredDisconnect(delay time.Duration) Option {
	return func(c *Client) { c.disconnectDelay = delay }
}

func WithAccessToken(fn func(ctx context.Context) (string, error)) Option {
	return func(c *Client) { c.accessTokenFn = fn }
}

func WithLogger(l transport.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.logger = l
			c.hasCustomLogger = true
		}
	}
}

func defaultReconnectBackoff(tries int) time.Duration {
	steps := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	if tries < 1 {
		return steps[0]
	}
	if tries > len(steps) {
		return steps[len(steps)-1]
	}
	return steps[tries-1]
}

// New creates a new Realtime Client targeting endpointURL (e.g. "wss://xyz.supabase.co/realtime/v1").
func New(endpointURL string, opts ...Option) *Client {
	c := &Client{
		endpointURL:       strings.TrimRight(endpointURL, "/"),
		vsn:               "2.0.0",
		headers:           make(map[string]string),
		httpClient:        &http.Client{},
		serializer:        NewSerializer(),
		heartbeatInterval: 25 * time.Second,
		reconnectAfterMs:  defaultReconnectBackoff,
		logger:            transport.NopLogger{},
		state:             StateClosed,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.transport == nil {
		c.transport = &CoderWebSocketTransport{HTTPClient: c.httpClient}
	}
	return c
}

// HasLogger reports whether a custom logger was configured on this client.
func (c *Client) HasLogger() bool {
	return c.hasCustomLogger
}

// OnHeartbeat registers a callback invoked with heartbeat status ("sent", "ok", "timeout", "disconnected").
func (c *Client) OnHeartbeat(cb func(status string)) {
	c.mu.Lock()
	c.onHeartbeat = cb
	c.mu.Unlock()
}

// ConnectionState returns the current WebSocket connection state.
func (c *Client) ConnectionState() ConnectionState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) nextRef() string {
	return strconv.FormatUint(c.refCounter.Add(1), 10)
}

func (c *Client) resolveToken(ctx context.Context) string {
	c.mu.RLock()
	fn := c.accessTokenFn
	tok := c.accessToken
	c.mu.RUnlock()
	if fn != nil {
		if t, err := fn(ctx); err == nil && t != "" {
			return t
		}
	}
	return tok
}

// SetAuth updates the auth token and pushes `access_token` control frames to all joined channels.
func (c *Client) SetAuth(token string) {
	c.mu.Lock()
	c.accessToken = token
	chs := append([]*Channel(nil), c.channels...)
	c.mu.Unlock()

	for _, ch := range chs {
		ch.pushAccessToken(context.Background(), token)
	}
}

func (c *Client) wsEndpointURL() string {
	base := c.endpointURL
	if !strings.HasSuffix(base, "/websocket") {
		base += "/websocket"
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	if c.apiKey != "" {
		q.Set("apikey", c.apiKey)
	}
	if c.vsn != "" {
		q.Set("vsn", c.vsn)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) httpBroadcastURL() string {
	base := c.endpointURL
	base = strings.TrimSuffix(base, "/websocket")
	base = strings.Replace(base, "wss://", "https://", 1)
	base = strings.Replace(base, "ws://", "http://", 1)
	return base + "/api/broadcast"
}

// Connect establishes the WebSocket connection and starts the read and heartbeat goroutines.
func (c *Client) Connect(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.disconnectTimer != nil {
		c.disconnectTimer.Stop()
		c.disconnectTimer = nil
	}
	if c.state == StateOpen && c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	c.intentionalClose = false
	c.state = StateConnecting
	hdrs := make(http.Header)
	for k, v := range c.headers {
		hdrs.Set(k, v)
	}
	c.mu.Unlock()

	conn, err := c.transport.Dial(ctx, c.wsEndpointURL(), hdrs)
	if err != nil {
		c.mu.Lock()
		c.state = StateClosed
		c.mu.Unlock()
		return err
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.conn = conn
	c.state = StateOpen
	c.loopCancel = cancel
	c.reconnectTries = 0
	c.mu.Unlock()

	c.loopWg.Add(2)
	go c.readLoop(loopCtx, conn)
	go c.heartbeatLoop(loopCtx)

	return nil
}

// Disconnect closes the WebSocket connection (honoring deferred disconnect delay if configured).
func (c *Client) Disconnect(code ...int) error {
	closeCode := 1000
	if len(code) > 0 && code[0] > 0 {
		closeCode = code[0]
	}
	c.mu.Lock()
	if c.disconnectDelay > 0 && c.conn != nil {
		if c.disconnectTimer != nil {
			c.disconnectTimer.Stop()
		}
		c.disconnectTimer = time.AfterFunc(c.disconnectDelay, func() {
			_ = c.disconnectNow(closeCode)
		})
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.disconnectNow(closeCode)
}

func (c *Client) disconnectNow(closeCode int) error {
	c.mu.Lock()
	c.intentionalClose = true
	if c.disconnectTimer != nil {
		c.disconnectTimer.Stop()
		c.disconnectTimer = nil
	}
	cancel := c.loopCancel
	c.loopCancel = nil
	conn := c.conn
	c.conn = nil
	c.state = StateClosing
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	var err error
	if conn != nil {
		err = conn.Close(closeCode, "normal closure")
	}
	c.loopWg.Wait()

	c.mu.Lock()
	c.state = StateClosed
	c.mu.Unlock()
	return err
}

func (c *Client) push(ctx context.Context, msg Message) error {
	c.mu.RLock()
	conn := c.conn
	state := c.state
	c.mu.RUnlock()
	if conn == nil || state != StateOpen {
		return nil
	}
	isBin, data, err := c.serializer.Encode(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteMessage(ctx, isBin, data)
}

func (c *Client) readLoop(ctx context.Context, conn Conn) {
	defer c.loopWg.Done()
	for {
		isBin, data, err := conn.ReadMessage(ctx)
		if err != nil {
			c.mu.Lock()
			intentional := c.intentionalClose
			if c.conn == conn {
				c.conn = nil
				c.state = StateClosed
			}
			tries := c.reconnectTries + 1
			c.reconnectTries = tries
			c.mu.Unlock()

			if intentional || ctx.Err() != nil {
				return
			}
			// Schedule reconnect using configured backoff.
			delay := c.reconnectAfterMs(tries)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				go func() {
					if err := c.Connect(context.Background()); err == nil {
						c.mu.RLock()
						chs := append([]*Channel(nil), c.channels...)
						c.mu.RUnlock()
						for _, ch := range chs {
							_ = ch.sendJoin(context.Background())
						}
					}
				}()
			}
			return
		}

		msg, err := c.serializer.Decode(isBin, data)
		if err != nil {
			c.logger.Warn("realtime: decode error", "err", err)
			continue
		}

		if msg.Topic == "phoenix" && msg.Event == "phx_reply" {
			c.mu.Lock()
			if msg.Ref == c.pendingHeartbeatRef {
				c.pendingHeartbeatRef = ""
				cb := c.onHeartbeat
				c.mu.Unlock()
				if cb != nil {
					cb("ok")
				}
				continue
			}
			c.mu.Unlock()
		}

		c.mu.RLock()
		chs := append([]*Channel(nil), c.channels...)
		c.mu.RUnlock()
		for _, ch := range chs {
			if ch.topic == msg.Topic {
				ch.handleMessage(msg)
			}
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	defer c.loopWg.Done()
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			if c.state != StateOpen {
				cb := c.onHeartbeat
				c.mu.Unlock()
				if cb != nil {
					cb("disconnected")
				}
				continue
			}
			timedOut := c.pendingHeartbeatRef != ""
			ref := c.nextRef()
			c.pendingHeartbeatRef = ref
			cb := c.onHeartbeat
			c.mu.Unlock()

			if timedOut && cb != nil {
				cb("timeout")
			}
			_ = c.push(ctx, Message{
				Ref:     ref,
				Topic:   "phoenix",
				Event:   "heartbeat",
				Payload: map[string]any{},
			})
			if cb != nil {
				cb("sent")
			}
		}
	}
}

// Channel creates (or registers) a new RealtimeChannel on this client.
func (c *Client) Channel(name string, cfg ...ChannelConfig) *Channel {
	var cc ChannelConfig
	if len(cfg) > 0 {
		cc = cfg[0]
	}
	ch := newChannel(c, name, cc)
	c.mu.Lock()
	c.channels = append(c.channels, ch)
	c.mu.Unlock()
	return ch
}

// GetChannels returns all registered channels.
func (c *Client) GetChannels() []*Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]*Channel(nil), c.channels...)
}

// RemoveChannel unsubscribes and removes a single channel.
func (c *Client) RemoveChannel(ctx context.Context, ch *Channel) error {
	if ch == nil {
		return nil
	}
	err := ch.Unsubscribe(ctx)
	c.mu.Lock()
	filtered := c.channels[:0]
	for _, existing := range c.channels {
		if existing != ch {
			filtered = append(filtered, existing)
		}
	}
	c.channels = filtered
	c.mu.Unlock()
	return err
}

// RemoveAllChannels unsubscribes and removes all channels.
func (c *Client) RemoveAllChannels(ctx context.Context) error {
	c.mu.Lock()
	chs := append([]*Channel(nil), c.channels...)
	c.channels = nil
	c.mu.Unlock()
	var firstErr error
	for _, ch := range chs {
		if err := ch.Unsubscribe(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
