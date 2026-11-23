package transport

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// TraceContext holds W3C Trace Context headers extracted for an outgoing request.
type TraceContext struct {
	Traceparent string
	Tracestate  string
	Baggage     string
	CarrierKeys []string
}

// TraceContextExtractor extracts W3C trace context from the request's context.
type TraceContextExtractor func(ctx context.Context) *TraceContext

// TracePropagationOptions controls opt-in W3C trace propagation.
type TracePropagationOptions struct {
	Enabled                 bool
	RespectSamplingDecision *bool
	Targets                 []string
	Extractor               TraceContextExtractor
}

type traceCtxKey struct{}

// WithTraceContext attaches a TraceContext to ctx for propagation on outgoing requests.
func WithTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceCtxKey{}, &tc)
}

// TraceContextFromContext extracts a TraceContext previously attached with WithTraceContext.
func TraceContextFromContext(ctx context.Context) *TraceContext {
	if ctx == nil {
		return nil
	}
	tc, _ := ctx.Value(traceCtxKey{}).(*TraceContext)
	return tc
}

var (
	globalExtractorMu sync.RWMutex
	globalExtractor   TraceContextExtractor
)

// RegisterTraceContextExtractor registers a process-wide fallback TraceContextExtractor.
func RegisterTraceContextExtractor(fn TraceContextExtractor) {
	globalExtractorMu.Lock()
	globalExtractor = fn
	globalExtractorMu.Unlock()
}

// GetTraceContextExtractor returns the registered process-wide TraceContextExtractor, if any.
func GetTraceContextExtractor() TraceContextExtractor {
	globalExtractorMu.RLock()
	fn := globalExtractor
	globalExtractorMu.RUnlock()
	return fn
}

// ParsedTraceParent represents a parsed W3C traceparent header.
type ParsedTraceParent struct {
	Version   string
	TraceID   string
	SpanID    string
	Flags     byte
	IsSampled bool
}

// ParseTraceParent parses a W3C traceparent header (`vv-32hex-16hex-2hex`).
func ParseTraceParent(header string) (ParsedTraceParent, bool) {
	parts := strings.Split(strings.TrimSpace(header), "-")
	if len(parts) < 4 {
		return ParsedTraceParent{}, false
	}
	version, traceID, spanID, flagsHex := parts[0], parts[1], parts[2], parts[3]
	if len(version) != 2 || len(traceID) != 32 || len(spanID) != 16 || len(flagsHex) != 2 {
		return ParsedTraceParent{}, false
	}
	if strings.EqualFold(version, "ff") {
		return ParsedTraceParent{}, false
	}
	if _, err := hex.DecodeString(version); err != nil {
		return ParsedTraceParent{}, false
	}
	if !isNonZeroHex(traceID) || !isNonZeroHex(spanID) {
		return ParsedTraceParent{}, false
	}
	flagBytes, err := hex.DecodeString(flagsHex)
	if err != nil || len(flagBytes) != 1 {
		return ParsedTraceParent{}, false
	}
	return ParsedTraceParent{
		Version:   strings.ToLower(version),
		TraceID:   strings.ToLower(traceID),
		SpanID:    strings.ToLower(spanID),
		Flags:     flagBytes[0],
		IsSampled: (flagBytes[0] & 0x01) == 0x01,
	}, true
}

func isNonZeroHex(s string) bool {
	b, err := hex.DecodeString(s)
	if err != nil {
		return false
	}
	for _, v := range b {
		if v != 0 {
			return true
		}
	}
	return false
}

// DefaultPropagationTargets derives default W3C trace propagation target origins from supabaseURL.
func DefaultPropagationTargets(supabaseURL string) []string {
	u, err := url.Parse(strings.TrimSpace(supabaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	origin := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
	targets := []string{origin}

	// Include storage.supabase.co new-hostname variant when project URL is *.supabase.co.
	host := strings.ToLower(u.Hostname())
	if strings.HasSuffix(host, ".supabase.co") && !strings.HasSuffix(host, ".storage.supabase.co") {
		storageHost := strings.TrimSuffix(host, ".supabase.co") + ".storage.supabase.co"
		if port := u.Port(); port != "" {
			storageHost += ":" + port
		}
		targets = append(targets, strings.ToLower(u.Scheme)+"://"+storageHost)
	}
	return targets
}

// ShouldPropagateToTarget reports whether targetURL matches any allowed propagation target origin/prefix.
func ShouldPropagateToTarget(targetURL string, targets []string) bool {
	if len(targets) == 0 {
		return false
	}
	tu, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || tu.Scheme == "" || tu.Host == "" {
		return false
	}
	targetOrigin := strings.ToLower(tu.Scheme) + "://" + strings.ToLower(tu.Host)
	targetFull := targetOrigin + tu.EscapedPath()

	for _, t := range targets {
		norm := strings.ToLower(strings.TrimRight(strings.TrimSpace(t), "/"))
		if norm == "" {
			continue
		}
		if targetOrigin == norm || strings.HasPrefix(targetFull, norm+"/") || targetFull == norm {
			return true
		}
	}
	return false
}

func (rt *roundTripper) injectTraceHeaders(req *http.Request) {
	extractor := rt.traceExtractor
	if extractor == nil {
		extractor = GetTraceContextExtractor()
	}
	if extractor == nil {
		extractor = TraceContextFromContext
	}

	if req.URL == nil || !ShouldPropagateToTarget(req.URL.String(), rt.traceTargets) {
		return
	}

	tc := extractor(req.Context())
	if tc == nil || tc.Traceparent == "" {
		return
	}

	traceparent := tc.Traceparent
	tracestate := tc.Tracestate
	baggage := tc.Baggage

	if rt.respectSampling {
		if parsed, ok := ParseTraceParent(traceparent); ok && !parsed.IsSampled {
			// Unsampled spans keep traceparent (flag 00) for log correlation,
			// but withhold tracestate and baggage.
			tracestate = ""
			baggage = ""
		}
	}

	if traceparent != "" && !hasHeader(req.Header, "traceparent") {
		req.Header.Set("traceparent", traceparent)
	}
	if tracestate != "" && !hasHeader(req.Header, "tracestate") {
		req.Header.Set("tracestate", tracestate)
	}
	if baggage != "" && !hasHeader(req.Header, "baggage") {
		req.Header.Set("baggage", baggage)
	}
}
