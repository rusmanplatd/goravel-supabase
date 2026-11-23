package realtime

import (
	"context"
	"net/http"

	"github.com/coder/websocket"
)

// Conn represents an active WebSocket connection used by RealtimeClient.
type Conn interface {
	ReadMessage(ctx context.Context) (isBinary bool, data []byte, err error)
	WriteMessage(ctx context.Context, isBinary bool, data []byte) error
	Close(code int, reason string) error
}

// Transport dials a WebSocket connection for RealtimeClient.
type Transport interface {
	Dial(ctx context.Context, rawURL string, headers http.Header) (Conn, error)
}

// CoderWebSocketTransport is the default Transport powered by github.com/coder/websocket.
type CoderWebSocketTransport struct {
	HTTPClient *http.Client
}

type coderConn struct {
	c *websocket.Conn
}

func (t *CoderWebSocketTransport) Dial(ctx context.Context, rawURL string, headers http.Header) (Conn, error) {
	c, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{
		HTTPClient: t.HTTPClient,
		HTTPHeader: headers,
	})
	if err != nil {
		return nil, err
	}
	return &coderConn{c: c}, nil
}

func (cc *coderConn) ReadMessage(ctx context.Context) (bool, []byte, error) {
	typ, data, err := cc.c.Read(ctx)
	if err != nil {
		return false, nil, err
	}
	return typ == websocket.MessageBinary, data, nil
}

func (cc *coderConn) WriteMessage(ctx context.Context, isBinary bool, data []byte) error {
	typ := websocket.MessageText
	if isBinary {
		typ = websocket.MessageBinary
	}
	return cc.c.Write(ctx, typ, data)
}

func (cc *coderConn) Close(code int, reason string) error {
	return cc.c.Close(websocket.StatusCode(code), reason)
}
