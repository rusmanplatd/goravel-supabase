package postgrest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

const DefaultURLLengthLimit = 8000

// Client is a pure-Go PostgREST client.
type Client struct {
	url               string
	headers           http.Header
	schema            string
	httpClient        *http.Client
	httpClientWrapper func(*http.Client) *http.Client
	timeout           time.Duration
	urlLengthLimit    int
	retry             bool
	retryBaseDelay    time.Duration
}

// Option configures a PostgREST Client.
type Option func(*Client)

// WithHeaders sets default HTTP headers sent with every request.
func WithHeaders(headers map[string]string) Option {
	return func(c *Client) {
		for k, v := range headers {
			c.headers.Set(k, v)
		}
	}
}

// WithSchema sets the Postgres schema for Accept-Profile / Content-Profile headers.
func WithSchema(schema string) Option {
	return func(c *Client) {
		c.schema = schema
	}
}

// WithHTTPClient sets the underlying *http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithHTTPClientWrapper sets a wrapper applied to per-request QueryBuilderOptions.HTTPClient instances.
func WithHTTPClientWrapper(fn func(*http.Client) *http.Client) Option {
	return func(c *Client) {
		c.httpClientWrapper = fn
	}
}

// WithTimeout sets a default per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.timeout = d
	}
}

// WithURLLengthLimit sets the maximum allowed request URL length (default 8000).
func WithURLLengthLimit(limit int) Option {
	return func(c *Client) {
		if limit > 0 {
			c.urlLengthLimit = limit
		}
	}
}

// WithRetry enables or disables idempotent auto-retry (default true).
func WithRetry(enabled bool) Option {
	return func(c *Client) {
		c.retry = enabled
	}
}

// WithRetryBaseDelay overrides the exponential backoff base delay (primarily for tests).
func WithRetryBaseDelay(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.retryBaseDelay = d
		}
	}
}

// New creates a new PostgREST Client bound to baseURL (e.g. "https://xyz.supabase.co/rest/v1").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		url:            strings.TrimRight(baseURL, "/"),
		headers:        make(http.Header),
		httpClient:     &http.Client{},
		urlLengthLimit: DefaultURLLengthLimit,
		retry:          true,
		retryBaseDelay: defaultRetryBaseDelay,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// Schema returns a shallow-derived Client targeting the specified Postgres schema.
func (c *Client) Schema(schema string) *Client {
	cp := *c
	cp.headers = c.headers.Clone()
	cp.schema = schema
	return &cp
}

// WithHTTPClientClone returns a shallow-derived Client using hc.
func (c *Client) WithHTTPClientClone(hc *http.Client) *Client {
	cp := *c
	cp.headers = c.headers.Clone()
	if hc != nil {
		cp.httpClient = hc
	}
	return &cp
}

// From creates a QueryBuilder for the given table or view.
func (c *Client) From(relation string, opts ...QueryBuilderOptions) *QueryBuilder {
	rawURL := c.url + "/" + strings.TrimLeft(relation, "/")
	u, _ := url.Parse(rawURL)

	headers := c.headers.Clone()
	hc := c.httpClient
	timeout := c.timeout

	if len(opts) > 0 {
		opt := opts[0]
		for k, v := range opt.Headers {
			headers.Set(k, v)
		}
		if opt.HTTPClient != nil {
			hc = opt.HTTPClient
			if c.httpClientWrapper != nil {
				hc = c.httpClientWrapper(hc)
			}
		}
		if opt.Timeout > 0 {
			timeout = opt.Timeout
		}
	}

	return &QueryBuilder{
		url:            u,
		headers:        headers,
		schema:         c.schema,
		httpClient:     hc,
		timeout:        timeout,
		urlLengthLimit: c.urlLengthLimit,
		retry:          c.retry,
		retryBaseDelay: c.retryBaseDelay,
	}
}

func hasNestedObjectArg(args map[string]any) bool {
	for _, v := range args {
		if v == nil {
			continue
		}
		rv := reflect.ValueOf(v)
		for rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				break
			}
			rv = rv.Elem()
		}
		switch rv.Kind() {
		case reflect.Map, reflect.Struct:
			return true
		case reflect.Slice, reflect.Array:
			for i := 0; i < rv.Len(); i++ {
				ev := rv.Index(i)
				if ev.Kind() == reflect.Interface && !ev.IsNil() {
					ev = ev.Elem()
				}
				if ev.Kind() == reflect.Pointer && !ev.IsNil() {
					ev = ev.Elem()
				}
				if ev.Kind() == reflect.Map || ev.Kind() == reflect.Struct {
					return true
				}
			}
		}
	}
	return false
}

func formatRPCSliceQuery(v any) (string, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return "", false
	}
	parts := make([]string, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		parts[i] = fmt.Sprint(rv.Index(i).Interface())
	}
	return "{" + strings.Join(parts, ",") + "}", true
}

// RPC invokes a Postgres stored procedure / function via `/rpc/<fn>`.
func (c *Client) RPC(fn string, args map[string]any, opts ...RPCOptions) *FilterBuilder {
	var opt RPCOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	u, _ := url.Parse(c.url + "/rpc/" + strings.TrimLeft(fn, "/"))
	headers := c.headers.Clone()

	method := http.MethodPost
	var body any
	var prefers []string
	if opt.Count != CountNone {
		prefers = append(prefers, "count="+string(opt.Count))
	}

	if opt.Head && hasNestedObjectArg(args) {
		method = http.MethodPost
		body = args
		prefers = append(prefers, "return=minimal")
	} else if opt.Head || opt.Get {
		if opt.Head {
			method = http.MethodHead
		} else {
			method = http.MethodGet
		}
		q := u.Query()
		for k, v := range args {
			if v == nil {
				continue
			}
			if formatted, ok := formatRPCSliceQuery(v); ok {
				q.Set(k, formatted)
			} else {
				q.Set(k, fmt.Sprint(v))
			}
		}
		u.RawQuery = q.Encode()
	} else {
		if args == nil {
			body = map[string]any{}
		} else {
			body = args
		}
	}

	if len(prefers) > 0 {
		headers.Add("Prefer", strings.Join(prefers, ","))
	}

	return &FilterBuilder{
		method:         method,
		url:            u,
		headers:        headers,
		schema:         c.schema,
		body:           body,
		httpClient:     c.httpClient,
		timeout:        c.timeout,
		urlLengthLimit: c.urlLengthLimit,
		retry:          c.retry,
		retryBaseDelay: c.retryBaseDelay,
	}
}

// GetOpenApiSpec fetches the PostgREST OpenAPI specification from the root REST endpoint.
func (c *Client) GetOpenApiSpec(ctx context.Context) (OpenApiSpec, *Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	if c.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	headers := c.headers.Clone()
	headers.Set("Accept", "application/openapi+json")
	if c.schema != "" {
		headers.Set("Accept-Profile", c.schema)
	}

	resp, err := doWithRetry(ctx, c.httpClient, http.MethodGet, c.url+"/", headers, nil, c.retry, c.retryBaseDelay)
	if err != nil {
		return nil, nil, &PostgrestError{Message: err.Error()}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, &PostgrestError{Message: err.Error(), Status: resp.StatusCode, StatusText: resp.Status}
	}

	pgResp := &Response{
		Data:       raw,
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    resp.Header.Clone(),
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, pgResp, decodePostgrestError(raw, resp.StatusCode, resp.Status)
	}

	var spec OpenApiSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, pgResp, &PostgrestError{Message: err.Error(), Status: resp.StatusCode, StatusText: resp.Status}
	}
	return spec, pgResp, nil
}
