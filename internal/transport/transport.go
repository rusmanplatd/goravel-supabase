package transport

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// Logger is a minimal structured logging interface used across pure-Go SDK packages.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NopLogger discards all log messages.
type NopLogger struct{}

func (NopLogger) Debug(string, ...any) {}
func (NopLogger) Info(string, ...any)  {}
func (NopLogger) Warn(string, ...any)  {}
func (NopLogger) Error(string, ...any) {}

// TokenProvider dynamically resolves a bearer access token for outgoing requests.
type TokenProvider func(ctx context.Context) (string, error)

const tempKeyPrefix = "sb_temp_"

var (
	sbSubtypeRe     = regexp.MustCompile(`^sb_[a-zA-Z0-9]+_`)
	warnedKeyTypes  sync.Map
)

// IsNewAPIKey reports whether key is a new-format Supabase API key (`sb_publishable_*`
// or `sb_secret_*`), which is not a JWT and must never be sent as a Bearer token on
// Edge Functions calls.
func IsNewAPIKey(key string) bool {
	return strings.HasPrefix(key, "sb_publishable_") || strings.HasPrefix(key, "sb_secret_")
}

// CheckAPIKeyFormat logs a one-time diagnostic warning per unrecognized `sb_*` key subtype
// without ever logging the key value itself.
func CheckAPIKeyFormat(key string, logger Logger) {
	if !strings.HasPrefix(key, "sb_") || IsNewAPIKey(key) || strings.HasPrefix(key, tempKeyPrefix) {
		return
	}
	subtype := sbSubtypeRe.FindString(key)
	if subtype == "" {
		subtype = "unknown"
	}
	if _, loaded := warnedKeyTypes.LoadOrStore(subtype, struct{}{}); loaded {
		return
	}
	if logger != nil {
		logger.Warn("supabase: unrecognized Supabase API key format; proceeding and sending key as-is", "subtype", subtype)
	}
}

// ResetAPIKeyWarningsForTest clears the one-time API key warning cache for unit tests.
func ResetAPIKeyWarningsForTest() {
	warnedKeyTypes.Range(func(k, _ any) bool {
		warnedKeyTypes.Delete(k)
		return true
	})
}

// Config configures the shared Supabase HTTP transport middleware.
type Config struct {
	Base               http.RoundTripper
	SupabaseURL        string
	APIKey             string
	Headers            map[string]string
	GetAccessToken     TokenProvider
	OmitAPIKeyAsBearer bool
	TracePropagation   *TracePropagationOptions
	Logger             Logger
}

type roundTripper struct {
	base             http.RoundTripper
	apiKey           string
	headers          map[string]string
	getAccessToken   TokenProvider
	allowKeyAsBearer bool
	traceEnabled     bool
	respectSampling  bool
	traceTargets     []string
	traceExtractor   TraceContextExtractor
	logger           Logger
}

// NewRoundTripper wraps cfg.Base (defaulting to http.DefaultTransport) with Supabase
// apikey injection, Bearer token resolution, global headers, and optional W3C trace propagation.
func NewRoundTripper(cfg Config) http.RoundTripper {
	base := cfg.Base
	if base == nil {
		base = http.DefaultTransport
	}
	logger := cfg.Logger
	if logger == nil {
		logger = NopLogger{}
	}

	CheckAPIKeyFormat(cfg.APIKey, logger)

	headersCopy := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headersCopy[k] = v
	}

	rt := &roundTripper{
		base:             base,
		apiKey:           cfg.APIKey,
		headers:          headersCopy,
		getAccessToken:   cfg.GetAccessToken,
		allowKeyAsBearer: !(cfg.OmitAPIKeyAsBearer && IsNewAPIKey(cfg.APIKey)),
		logger:           logger,
	}

	if cfg.TracePropagation != nil && cfg.TracePropagation.Enabled {
		rt.traceEnabled = true
		rt.respectSampling = cfg.TracePropagation.RespectSamplingDecision == nil || *cfg.TracePropagation.RespectSamplingDecision
		if len(cfg.TracePropagation.Targets) > 0 {
			rt.traceTargets = append([]string(nil), cfg.TracePropagation.Targets...)
		} else {
			rt.traceTargets = DefaultPropagationTargets(cfg.SupabaseURL)
		}
		rt.traceExtractor = cfg.TracePropagation.Extractor
	}

	return rt
}

// NewHTTPClient returns a shallow clone of base (or a fresh *http.Client) whose Transport
// is wrapped with NewRoundTripper, sharing the underlying connection pool.
func NewHTTPClient(base *http.Client, cfg Config) *http.Client {
	c := &http.Client{}
	if base != nil {
		*c = *base
		if cfg.Base == nil {
			cfg.Base = base.Transport
		}
	}
	c.Transport = NewRoundTripper(cfg)
	return c
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}

	// Global headers apply only when not already set on the request.
	for k, v := range rt.headers {
		if !hasHeader(cloned.Header, k) {
			cloned.Header.Set(k, v)
		}
	}

	if rt.apiKey != "" && !hasHeader(cloned.Header, "apikey") {
		cloned.Header.Set("apikey", rt.apiKey)
	}

	if !hasHeader(cloned.Header, "Authorization") {
		var token string
		if rt.getAccessToken != nil {
			t, err := rt.getAccessToken(req.Context())
			if err != nil {
				return nil, err
			}
			token = t
		}
		if token == "" && rt.allowKeyAsBearer {
			token = rt.apiKey
		}
		if token != "" {
			cloned.Header.Set("Authorization", "Bearer "+token)
		}
	}

	if rt.traceEnabled {
		rt.injectTraceHeaders(cloned)
	}

	return rt.base.RoundTrip(cloned)
}

func hasHeader(h http.Header, name string) bool {
	if h.Get(name) != "" {
		return true
	}
	lower := strings.ToLower(name)
	for k, vals := range h {
		if strings.ToLower(k) == lower && len(vals) > 0 {
			return true
		}
	}
	return false
}
