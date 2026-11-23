package functions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/rusmanplatd/goravel-supabase/internal/transport"
)

// Client invokes Supabase Edge Functions.
type Client struct {
	mu         sync.RWMutex
	url        string
	headers    map[string]string
	region     FunctionRegion
	httpClient *http.Client
	apiKey     string
}

// Option configures a Functions Client.
type Option func(*Client)

// WithHeaders sets client-level default headers.
func WithHeaders(headers map[string]string) Option {
	return func(c *Client) {
		for k, v := range headers {
			c.headers[k] = v
		}
	}
}

// WithHTTPClient overrides the underlying *http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithRegion sets the default FunctionRegion for invocations.
func WithRegion(region FunctionRegion) Option {
	return func(c *Client) {
		if region != "" {
			c.region = region
		}
	}
}

// WithAPIKey configures the Supabase API key on a standalone Functions client,
// applying the omitApiKeyAsBearer rule for sb_publishable_* and sb_secret_* keys.
func WithAPIKey(apiKey string) Option {
	return func(c *Client) {
		c.apiKey = apiKey
	}
}

// New creates a new Edge Functions Client bound to the given functions base URL
// (e.g. "https://xyzcompany.supabase.co/functions/v1").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		url:        strings.TrimRight(baseURL, "/"),
		headers:    make(map[string]string),
		region:     RegionAny,
		httpClient: &http.Client{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.apiKey != "" {
		c.httpClient = transport.NewHTTPClient(c.httpClient, transport.Config{
			SupabaseURL:        c.url,
			APIKey:             c.apiKey,
			OmitAPIKeyAsBearer: true,
		})
	}
	return c
}

// SetAuth updates the Authorization Bearer token on the client.
func (c *Client) SetAuth(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if token == "" {
		delete(c.headers, "Authorization")
		return
	}
	c.headers["Authorization"] = "Bearer " + token
}

// WithHTTPClientClone returns a shallow-derived Functions client using a different *http.Client.
func (c *Client) WithHTTPClientClone(hc *http.Client) *Client {
	c.mu.RLock()
	headersCopy := make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		headersCopy[k] = v
	}
	region := c.region
	baseURL := c.url
	c.mu.RUnlock()

	return &Client{
		url:        baseURL,
		headers:    headersCopy,
		region:     region,
		httpClient: hc,
	}
}

// Invoke calls the named Supabase Edge Function.
func (c *Client) Invoke(ctx context.Context, functionName string, opts ...InvokeOptions) (*InvokeResponse, error) {
	var opt InvokeOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	if ctx == nil {
		ctx = context.Background()
	}

	var cancel context.CancelFunc
	if opt.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opt.Timeout)
		defer cancel()
	}

	c.mu.RLock()
	clientRegion := c.region
	clientHeaders := make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		clientHeaders[k] = v
	}
	hc := c.httpClient
	c.mu.RUnlock()

	region := opt.Region
	if region == "" {
		region = clientRegion
	}

	rawURL := c.url + "/" + strings.TrimLeft(functionName, "/")
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, NewFunctionsFetchError(err)
	}

	autoHeaders := make(map[string]string)
	if region != "" && region != RegionAny {
		autoHeaders["x-region"] = string(region)
		q := parsedURL.Query()
		q.Set("forceFunctionRegion", string(region))
		parsedURL.RawQuery = q.Encode()
	}

	hasCallerContentType := hasHeaderKeyCI(opt.Headers, "content-type") || hasHeaderKeyCI(clientHeaders, "content-type")

	var bodyReader io.Reader
	if opt.Body != nil {
		switch b := opt.Body.(type) {
		case []byte:
			if !hasCallerContentType {
				autoHeaders["Content-Type"] = "application/octet-stream"
			}
			bodyReader = bytes.NewReader(b)
		case io.Reader:
			if !hasCallerContentType {
				autoHeaders["Content-Type"] = "application/octet-stream"
			}
			bodyReader = b
		case string:
			if !hasCallerContentType {
				autoHeaders["Content-Type"] = "text/plain"
			}
			bodyReader = strings.NewReader(b)
		case *MultipartBody:
			if b != nil {
				buf, ct, err := encodeMultipart(b)
				if err != nil {
					return nil, NewFunctionsFetchError(err)
				}
				if !hasCallerContentType {
					autoHeaders["Content-Type"] = ct
				}
				bodyReader = buf
			}
		case MultipartBody:
			buf, ct, err := encodeMultipart(&b)
			if err != nil {
				return nil, NewFunctionsFetchError(err)
			}
			if !hasCallerContentType {
				autoHeaders["Content-Type"] = ct
			}
			bodyReader = buf
		default:
			encoded, err := json.Marshal(b)
			if err != nil {
				return nil, NewFunctionsFetchError(err)
			}
			if !hasCallerContentType {
				autoHeaders["Content-Type"] = "application/json"
			}
			bodyReader = bytes.NewReader(encoded)
		}
	}

	method := opt.Method
	if method == "" {
		method = http.MethodPost
	}

	req, err := http.NewRequestWithContext(ctx, method, parsedURL.String(), bodyReader)
	if err != nil {
		return nil, NewFunctionsFetchError(err)
	}

	// Header precedence (low -> high): autoHeaders < clientHeaders < opt.Headers.
	// Preserve caller's exact header casing/value without creating duplicate keys.
	setHeadersCI(req.Header, autoHeaders)
	setHeadersCI(req.Header, clientHeaders)
	setHeadersCI(req.Header, opt.Headers)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, NewFunctionsFetchError(err)
	}

	if strings.EqualFold(resp.Header.Get("x-relay-error"), "true") {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, NewFunctionsRelayError(resp, raw)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, NewFunctionsHttpError(resp, raw)
	}

	ctHeader := resp.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ctHeader)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(ctHeader, ";")[0]))
		if mediaType == "" {
			mediaType = "text/plain"
		}
	} else {
		mediaType = strings.ToLower(mediaType)
	}

	// text/event-stream must NOT be buffered; return live stream directly.
	if mediaType == "text/event-stream" {
		return &InvokeResponse{
			Data:     resp.Body,
			Stream:   resp.Body,
			Response: resp,
		}, nil
	}

	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, NewFunctionsFetchError(err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	result := &InvokeResponse{
		RawBody:  raw,
		Response: resp,
	}

	switch mediaType {
	case "application/json":
		var decoded any
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, NewFunctionsFetchError(err)
			}
		}
		result.Data = decoded
	case "application/octet-stream", "application/pdf":
		result.Data = raw
	case "multipart/form-data":
		boundary := params["boundary"]
		mr := multipart.NewReader(bytes.NewReader(raw), boundary)
		form, err := mr.ReadForm(32 << 20)
		if err != nil {
			return nil, NewFunctionsFetchError(err)
		}
		result.Form = form
		result.Data = form
	default:
		result.Data = string(raw)
	}

	return result, nil
}

func encodeMultipart(mb *MultipartBody) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range mb.Fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	for _, f := range mb.Files {
		fw, err := w.CreateFormFile(f.FieldName, f.FileName)
		if err != nil {
			return nil, "", err
		}
		if f.Reader != nil {
			if _, err := io.Copy(fw, f.Reader); err != nil {
				return nil, "", err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func hasHeaderKeyCI(m map[string]string, target string) bool {
	for k := range m {
		if strings.EqualFold(k, target) {
			return true
		}
	}
	return false
}

func setHeadersCI(h http.Header, m map[string]string) {
	for k, v := range m {
		// Delete any existing key matching case-insensitively before setting.
		for existing := range h {
			if strings.EqualFold(existing, k) {
				delete(h, existing)
			}
		}
		h.Set(k, v)
	}
}
