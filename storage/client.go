package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// VersioningStatus controls object versioning on a bucket.
type VersioningStatus string

const (
	VersioningEnabled   VersioningStatus = "Enabled"
	VersioningSuspended VersioningStatus = "Suspended"
)

// Bucket represents a Supabase Storage bucket.
type Bucket struct {
	ID               string           `json:"id"`
	Name             string           `json:"name"`
	Owner            string           `json:"owner,omitempty"`
	Public           bool             `json:"public"`
	FileSizeLimit    any              `json:"file_size_limit,omitempty"`
	AllowedMimeTypes []string         `json:"allowed_mime_types,omitempty"`
	VersioningStatus VersioningStatus `json:"versioning_status,omitempty"`
	Type             string           `json:"type,omitempty"`
	CreatedAt        string           `json:"created_at,omitempty"`
	UpdatedAt        string           `json:"updated_at,omitempty"`
}

// BucketOptions configures CreateBucket and UpdateBucket.
type BucketOptions struct {
	Public           bool             `json:"public"`
	FileSizeLimit    any              `json:"file_size_limit,omitempty"`
	AllowedMimeTypes []string         `json:"allowed_mime_types,omitempty"`
	VersioningStatus VersioningStatus `json:"versioning_status,omitempty"`
	Type             string           `json:"type,omitempty"`
}

// ListBucketsOptions configures ListBuckets.
type ListBucketsOptions struct {
	Limit      int
	Offset     int
	SortColumn string
	SortOrder  string
	Search     string
}

// LifecycleRule represents a bucket lifecycle management rule.
type LifecycleRule struct {
	ID                         string         `json:"id,omitempty"`
	Status                     string         `json:"status"` // "Enabled" | "Disabled"
	Filter                     map[string]any `json:"filter,omitempty"`
	Expiration                 map[string]any `json:"expiration,omitempty"`
	NoncurrentVersionExpiration map[string]any `json:"noncurrentVersionExpiration,omitempty"`
	AbortIncompleteMultipartUpload map[string]any `json:"abortIncompleteMultipartUpload,omitempty"`
}

// BucketLifecycleConfiguration holds a bucket's lifecycle rules.
type BucketLifecycleConfiguration struct {
	Rules []LifecycleRule `json:"rules"`
}

// PurgeCacheOptions configures CDN cache purge operations.
type PurgeCacheOptions struct {
	Transformations bool `json:"transformations,omitempty"`
}

// Client is a pure-Go Supabase Storage client.
type Client struct {
	url            string
	headers        map[string]string
	httpClient     *http.Client
	useNewHostname bool
}

// Option configures a storage.Client.
type Option func(*Client)

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

// WithUseNewHostname rewrites `*.supabase.co` hosts to `*.storage.supabase.co` when applicable.
func WithUseNewHostname(enabled bool) Option {
	return func(c *Client) {
		c.useNewHostname = enabled
	}
}

// New creates a new Supabase Storage Client bound to baseURL (e.g. "https://xyz.supabase.co/storage/v1").
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		url:        strings.TrimRight(baseURL, "/"),
		headers:    make(map[string]string),
		httpClient: &http.Client{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.useNewHostname {
		c.url = rewriteNewHostname(c.url)
	}
	return c
}

func rewriteNewHostname(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := u.Hostname()
	if strings.HasSuffix(host, ".supabase.co") && !strings.HasSuffix(host, ".storage.supabase.co") {
		newHost := strings.TrimSuffix(host, ".supabase.co") + ".storage.supabase.co"
		if port := u.Port(); port != "" {
			u.Host = newHost + ":" + port
		} else {
			u.Host = newHost
		}
		return strings.TrimRight(u.String(), "/")
	}
	return rawURL
}

// URL returns the resolved storage base URL.
func (c *Client) URL() string { return c.url }

// WithHTTPClientClone returns a shallow-derived Storage Client using hc.
func (c *Client) WithHTTPClientClone(hc *http.Client) *Client {
	headersCopy := make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		headersCopy[k] = v
	}
	if hc == nil {
		hc = c.httpClient
	}
	return &Client{
		url:            c.url,
		headers:        headersCopy,
		httpClient:     hc,
		useNewHostname: c.useNewHostname,
	}
}

// From returns a FileApi scoped to the given bucket ID.
func (c *Client) From(bucketID string) *FileApi {
	return &FileApi{
		client:   c,
		bucketID: bucketID,
	}
}

// Analytics returns a StorageAnalyticsClient targeting `/storage/v1/iceberg`.
func (c *Client) Analytics() *AnalyticsClient {
	return &AnalyticsClient{
		url:        c.url + "/iceberg",
		headers:    c.headers,
		httpClient: c.httpClient,
	}
}

// Vectors returns a StorageVectorsClient targeting `/storage/v1/vector`.
func (c *Client) Vectors() *VectorsClient {
	return &VectorsClient{
		url:        c.url + "/vector",
		headers:    c.headers,
		httpClient: c.httpClient,
	}
}

func (c *Client) applyHeaders(req *http.Request) {
	for k, v := range c.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, reader)
	if err != nil {
		return &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
	}
	c.applyHeaders(req)
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeStorageApiError(raw, resp.StatusCode, resp.Status)
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
		}
	}
	return nil
}

// --- Bucket CRUD & Lifecycle ---

func (c *Client) CreateBucket(ctx context.Context, id string, opts ...BucketOptions) (string, error) {
	body := map[string]any{
		"id":   id,
		"name": id,
	}
	if len(opts) > 0 {
		opt := opts[0]
		body["public"] = opt.Public
		if opt.FileSizeLimit != nil {
			body["file_size_limit"] = opt.FileSizeLimit
		}
		if opt.AllowedMimeTypes != nil {
			body["allowed_mime_types"] = opt.AllowedMimeTypes
		}
		if opt.VersioningStatus != "" {
			body["versioning_status"] = opt.VersioningStatus
		}
		if opt.Type != "" {
			body["type"] = opt.Type
		}
	}
	var resp struct {
		Name string `json:"name"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/bucket", body, &resp); err != nil {
		return "", err
	}
	return resp.Name, nil
}

func (c *Client) GetBucket(ctx context.Context, id string) (*Bucket, error) {
	var b Bucket
	if err := c.doJSON(ctx, http.MethodGet, "/bucket/"+url.PathEscape(id), nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) ListBuckets(ctx context.Context, opts ...ListBucketsOptions) ([]Bucket, error) {
	path := "/bucket"
	if len(opts) > 0 {
		q := url.Values{}
		opt := opts[0]
		if opt.Limit > 0 {
			q.Set("limit", itoa(opt.Limit))
		}
		if opt.Offset > 0 {
			q.Set("offset", itoa(opt.Offset))
		}
		if opt.SortColumn != "" {
			q.Set("sortColumn", opt.SortColumn)
		}
		if opt.SortOrder != "" {
			q.Set("sortOrder", opt.SortOrder)
		}
		if opt.Search != "" {
			q.Set("search", opt.Search)
		}
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
	}
	var buckets []Bucket
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &buckets); err != nil {
		return nil, err
	}
	return buckets, nil
}

func (c *Client) UpdateBucket(ctx context.Context, id string, opts BucketOptions) (string, error) {
	body := map[string]any{
		"id":     id,
		"name":   id,
		"public": opts.Public,
	}
	if opts.FileSizeLimit != nil {
		body["file_size_limit"] = opts.FileSizeLimit
	}
	if opts.AllowedMimeTypes != nil {
		body["allowed_mime_types"] = opts.AllowedMimeTypes
	}
	if opts.VersioningStatus != "" {
		body["versioning_status"] = opts.VersioningStatus
	}
	var resp struct {
		Message string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodPut, "/bucket/"+url.PathEscape(id), body, &resp); err != nil {
		return "", err
	}
	return resp.Message, nil
}

func (c *Client) EmptyBucket(ctx context.Context, id string) (string, error) {
	var resp struct {
		Message string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/bucket/"+url.PathEscape(id)+"/empty", map[string]any{}, &resp); err != nil {
		return "", err
	}
	return resp.Message, nil
}

func (c *Client) DeleteBucket(ctx context.Context, id string) (string, error) {
	var resp struct {
		Message string `json:"message"`
	}
	if err := c.doJSON(ctx, http.MethodDelete, "/bucket/"+url.PathEscape(id), map[string]any{}, &resp); err != nil {
		return "", err
	}
	return resp.Message, nil
}

func (c *Client) GetBucketLifecycle(ctx context.Context, id string) (*BucketLifecycleConfiguration, error) {
	var cfg BucketLifecycleConfiguration
	if err := c.doJSON(ctx, http.MethodGet, "/bucket/"+url.PathEscape(id)+"/lifecycle", nil, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Client) UpdateBucketLifecycle(ctx context.Context, id string, cfg BucketLifecycleConfiguration) (*BucketLifecycleConfiguration, error) {
	var out BucketLifecycleConfiguration
	if err := c.doJSON(ctx, http.MethodPut, "/bucket/"+url.PathEscape(id)+"/lifecycle", cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteBucketLifecycle(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/bucket/"+url.PathEscape(id)+"/lifecycle", nil, nil)
}

func (c *Client) PurgeBucketCache(ctx context.Context, id string, opts ...PurgeCacheOptions) error {
	body := map[string]any{}
	if len(opts) > 0 && opts[0].Transformations {
		body["transformations"] = true
	}
	return c.doJSON(ctx, http.MethodPost, "/bucket/"+url.PathEscape(id)+"/purge", body, nil)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
