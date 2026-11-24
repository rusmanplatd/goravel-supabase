package postgrest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultMaxRetries = 3
	maxRetryDelay     = 30 * time.Second
)

var defaultRetryBaseDelay = time.Second

func computeBackoffDelay(baseDelay time.Duration, attempt int) time.Duration {
	if baseDelay >= maxRetryDelay {
		return maxRetryDelay
	}
	delay := baseDelay
	for i := 0; i < attempt; i++ {
		if delay >= maxRetryDelay/2 {
			return maxRetryDelay
		}
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func isIdempotentMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func isRetryableStatus(status int) bool {
	return status == http.StatusServiceUnavailable || status == 520
}

func parseRetryAfter(h string) (time.Duration, bool) {
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

func doWithRetry(
	ctx context.Context,
	hc *http.Client,
	method string,
	rawURL string,
	headers http.Header,
	bodyBytes []byte,
	retryEnabled bool,
	baseDelay time.Duration,
) (*http.Response, error) {
	if baseDelay <= 0 {
		baseDelay = defaultRetryBaseDelay
	}
	maxAttempts := 1
	if retryEnabled && isIdempotentMethod(method) {
		maxAttempts = 1 + defaultMaxRetries
	}

	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var bodyReader io.Reader
		if len(bodyBytes) > 0 {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header = headers.Clone()
		if attempt > 0 {
			req.Header.Set("X-Retry-Count", strconv.Itoa(attempt))
		}

		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			// Never retry if the context was canceled or timed out.
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			if attempt+1 >= maxAttempts {
				return nil, err
			}
			delay := computeBackoffDelay(baseDelay, attempt)
			if !sleepWithContext(ctx, delay) {
				return nil, ctx.Err()
			}
			continue
		}

		if !isRetryableStatus(resp.StatusCode) || attempt+1 >= maxAttempts {
			return resp, nil
		}

		// Drain and close body so the TCP connection can be reused before retrying.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		lastResp = resp

		delay := computeBackoffDelay(baseDelay, attempt)
		if ra, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			delay = ra
		}
		if !sleepWithContext(ctx, delay) {
			return nil, ctx.Err()
		}
	}

	return lastResp, lastErr
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
