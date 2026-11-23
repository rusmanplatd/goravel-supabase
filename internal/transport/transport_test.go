package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rusmanplatd/goravel-supabase/internal/transport"
)

type captureLogger struct {
	mu    sync.Mutex
	warns []string
}

func (l *captureLogger) Debug(string, ...any) {}
func (l *captureLogger) Info(string, ...any)  {}
func (l *captureLogger) Error(string, ...any) {}
func (l *captureLogger) Warn(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}

func TestAPIKeyAndBearerHeaderPrecedence(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Run("legacy JWT key falls back as Bearer", func(t *testing.T) {
		client := transport.NewHTTPClient(srv.Client(), transport.Config{
			SupabaseURL:        srv.URL,
			APIKey:             "eyJhbGciOiJIUzI1NiJ9.legacy.sig",
			OmitAPIKeyAsBearer: true,
			Headers:            map[string]string{"X-Client-Info": "goravel-supabase"},
		})
		resp, err := client.Get(srv.URL + "/functions/v1/hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if got := gotHeaders.Get("apikey"); got != "eyJhbGciOiJIUzI1NiJ9.legacy.sig" {
			t.Fatalf("apikey = %q, want legacy JWT", got)
		}
		if got := gotHeaders.Get("Authorization"); got != "Bearer eyJhbGciOiJIUzI1NiJ9.legacy.sig" {
			t.Fatalf("Authorization = %q, want Bearer legacy JWT", got)
		}
		if got := gotHeaders.Get("X-Client-Info"); got != "goravel-supabase" {
			t.Fatalf("X-Client-Info = %q", got)
		}
	})

	t.Run("sb_publishable key omitted as Bearer on Edge Functions without session", func(t *testing.T) {
		client := transport.NewHTTPClient(srv.Client(), transport.Config{
			SupabaseURL:        srv.URL,
			APIKey:             "sb_publishable_abc123",
			OmitAPIKeyAsBearer: true,
		})
		resp, err := client.Get(srv.URL + "/functions/v1/hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if got := gotHeaders.Get("apikey"); got != "sb_publishable_abc123" {
			t.Fatalf("apikey = %q", got)
		}
		if got := gotHeaders.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
	})

	t.Run("sb_secret key omitted as Bearer on Edge Functions without session, but uses session token when present", func(t *testing.T) {
		client := transport.NewHTTPClient(srv.Client(), transport.Config{
			SupabaseURL:        srv.URL,
			APIKey:             "sb_secret_xyz",
			OmitAPIKeyAsBearer: true,
			GetAccessToken: func(ctx context.Context) (string, error) {
				return "user-jwt-token", nil
			},
		})
		resp, err := client.Get(srv.URL + "/functions/v1/hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if got := gotHeaders.Get("Authorization"); got != "Bearer user-jwt-token" {
			t.Fatalf("Authorization = %q, want Bearer user-jwt-token", got)
		}
	})

	t.Run("request headers override global headers and auth", func(t *testing.T) {
		client := transport.NewHTTPClient(srv.Client(), transport.Config{
			SupabaseURL: srv.URL,
			APIKey:      "default-key",
			Headers:     map[string]string{"X-Custom": "global"},
		})
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/rest/v1/users", nil)
		req.Header.Set("X-Custom", "per-request")
		req.Header.Set("Authorization", "Bearer custom-bearer")
		req.Header.Set("apikey", "custom-key")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if gotHeaders.Get("X-Custom") != "per-request" {
			t.Fatalf("X-Custom = %q", gotHeaders.Get("X-Custom"))
		}
		if gotHeaders.Get("Authorization") != "Bearer custom-bearer" {
			t.Fatalf("Authorization = %q", gotHeaders.Get("Authorization"))
		}
		if gotHeaders.Get("apikey") != "custom-key" {
			t.Fatalf("apikey = %q", gotHeaders.Get("apikey"))
		}
	})
}

func TestUnrecognizedSBKeyWarnsOncePerSubtypeWithoutSecret(t *testing.T) {
	transport.ResetAPIKeyWarningsForTest()
	logger := &captureLogger{}

	transport.CheckAPIKeyFormat("sb_publishable_ok", logger)
	transport.CheckAPIKeyFormat("sb_secret_ok", logger)
	transport.CheckAPIKeyFormat("sb_temp_ok", logger)
	if len(logger.warns) != 0 {
		t.Fatalf("expected 0 warnings for known keys, got %d", len(logger.warns))
	}

	secret := "sb_future_SUPER_SECRET_VALUE"
	transport.CheckAPIKeyFormat(secret, logger)
	transport.CheckAPIKeyFormat("sb_future_ANOTHER_SECRET", logger)
	if len(logger.warns) != 1 {
		t.Fatalf("expected 1 warning for repeated subtype, got %d", len(logger.warns))
	}
	if strings.Contains(logger.warns[0], secret) {
		t.Fatalf("warning leaked secret value: %s", logger.warns[0])
	}

	transport.CheckAPIKeyFormat("sb_other_123", logger)
	if len(logger.warns) != 2 {
		t.Fatalf("expected 2 warnings for second subtype, got %d", len(logger.warns))
	}
}

func TestTracePropagation(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer otherSrv.Close()

	sampledTP := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	unsampledTP := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"

	client := transport.NewHTTPClient(srv.Client(), transport.Config{
		SupabaseURL: srv.URL,
		APIKey:      "key",
		TracePropagation: &transport.TracePropagationOptions{
			Enabled: true,
		},
	})

	t.Run("sampled trace propagates all three headers to Supabase target", func(t *testing.T) {
		ctx := transport.WithTraceContext(context.Background(), transport.TraceContext{
			Traceparent: sampledTP,
			Tracestate:  "rojo=00f067aa0ba902b7",
			Baggage:     "userId=alice",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/rest/v1/items", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if gotHeaders.Get("traceparent") != sampledTP {
			t.Fatalf("traceparent = %q", gotHeaders.Get("traceparent"))
		}
		if gotHeaders.Get("tracestate") != "rojo=00f067aa0ba902b7" {
			t.Fatalf("tracestate = %q", gotHeaders.Get("tracestate"))
		}
		if gotHeaders.Get("baggage") != "userId=alice" {
			t.Fatalf("baggage = %q", gotHeaders.Get("baggage"))
		}
	})

	t.Run("unsampled trace withholds tracestate and baggage when respectSamplingDecision is active", func(t *testing.T) {
		ctx := transport.WithTraceContext(context.Background(), transport.TraceContext{
			Traceparent: unsampledTP,
			Tracestate:  "rojo=00f067aa0ba902b7",
			Baggage:     "userId=alice",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/rest/v1/items", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if gotHeaders.Get("traceparent") != unsampledTP {
			t.Fatalf("traceparent = %q", gotHeaders.Get("traceparent"))
		}
		if gotHeaders.Get("tracestate") != "" {
			t.Fatalf("expected empty tracestate, got %q", gotHeaders.Get("tracestate"))
		}
		if gotHeaders.Get("baggage") != "" {
			t.Fatalf("expected empty baggage, got %q", gotHeaders.Get("baggage"))
		}
	})

	t.Run("non-Supabase target does not receive trace headers", func(t *testing.T) {
		ctx := transport.WithTraceContext(context.Background(), transport.TraceContext{
			Traceparent: sampledTP,
			Tracestate:  "vendor=1",
			Baggage:     "k=v",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, otherSrv.URL+"/external", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if gotHeaders.Get("traceparent") != "" {
			t.Fatalf("expected no traceparent on non-Supabase target, got %q", gotHeaders.Get("traceparent"))
		}
	})

	t.Run("caller-supplied trace headers are preserved", func(t *testing.T) {
		ctx := transport.WithTraceContext(context.Background(), transport.TraceContext{
			Traceparent: sampledTP,
			Tracestate:  "from-ctx=1",
			Baggage:     "from-ctx=2",
		})
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/rest/v1/items", nil)
		req.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
		req.Header.Set("tracestate", "caller=1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()

		if gotHeaders.Get("traceparent") != "00-11111111111111111111111111111111-2222222222222222-01" {
			t.Fatalf("traceparent = %q", gotHeaders.Get("traceparent"))
		}
		if gotHeaders.Get("tracestate") != "caller=1" {
			t.Fatalf("tracestate = %q", gotHeaders.Get("tracestate"))
		}
		if gotHeaders.Get("baggage") != "from-ctx=2" {
			t.Fatalf("baggage = %q", gotHeaders.Get("baggage"))
		}
	})
}
