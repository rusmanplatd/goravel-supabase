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

// AnalyticsClient manages Supabase Analytics (Iceberg) buckets and returns Iceberg REST Catalog clients.
type AnalyticsClient struct {
	url        string // e.g. https://xyz.supabase.co/storage/v1/iceberg
	headers    map[string]string
	httpClient *http.Client
}

type AnalyticBucket struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Format    string `json:"format,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (a *AnalyticsClient) doJSON(ctx context.Context, method, fullURL string, body any, out any, icebergErr bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return err
	}
	for k, v := range a.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if icebergErr {
			return decodeIcebergError(raw, resp.StatusCode, resp.Status)
		}
		return decodeStorageApiError(raw, resp.StatusCode, resp.Status)
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (a *AnalyticsClient) CreateBucket(ctx context.Context, name string) (*AnalyticBucket, error) {
	var out AnalyticBucket
	if err := a.doJSON(ctx, http.MethodPost, a.url+"/bucket", map[string]any{"name": name}, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *AnalyticsClient) ListBuckets(ctx context.Context, opts ...ListBucketsOptions) ([]AnalyticBucket, error) {
	fullURL := a.url + "/bucket"
	if len(opts) > 0 {
		q := url.Values{}
		if opts[0].Limit > 0 {
			q.Set("limit", itoa(opts[0].Limit))
		}
		if opts[0].Offset > 0 {
			q.Set("offset", itoa(opts[0].Offset))
		}
		if opts[0].Search != "" {
			q.Set("search", opts[0].Search)
		}
		if enc := q.Encode(); enc != "" {
			fullURL += "?" + enc
		}
	}
	var out []AnalyticBucket
	if err := a.doJSON(ctx, http.MethodGet, fullURL, nil, &out, false); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *AnalyticsClient) DeleteBucket(ctx context.Context, name string) (string, error) {
	var out struct {
		Message string `json:"message"`
	}
	if err := a.doJSON(ctx, http.MethodDelete, a.url+"/bucket/"+url.PathEscape(name), nil, &out, false); err != nil {
		return "", err
	}
	return out.Message, nil
}

// From returns a native Go Apache Iceberg REST Catalog client scoped to bucketName.
func (a *AnalyticsClient) From(bucketName string) (*IcebergCatalog, error) {
	trimmed := strings.TrimSpace(bucketName)
	if trimmed == "" {
		return nil, &StorageError{Name: "StorageError", Message: "analytics bucket name is required"}
	}
	return &IcebergCatalog{
		analytics: a,
		prefix:    trimmed,
	}, nil
}

// IcebergCatalog is a native Go Apache Iceberg REST Catalog client targeting `/storage/v1/iceberg/v1/{prefix}/...`.
type IcebergCatalog struct {
	analytics *AnalyticsClient
	prefix    string
}

func (ic *IcebergCatalog) nsPath(namespace []string) string {
	joined := strings.Join(namespace, "\x1f")
	return ic.analytics.url + "/v1/" + url.PathEscape(ic.prefix) + "/namespaces/" + url.PathEscape(joined)
}

func (ic *IcebergCatalog) CreateNamespace(ctx context.Context, namespace []string, properties map[string]string) (map[string]any, error) {
	body := map[string]any{"namespace": namespace}
	if properties != nil {
		body["properties"] = properties
	}
	var out map[string]any
	if err := ic.analytics.doJSON(ctx, http.MethodPost, ic.analytics.url+"/v1/"+url.PathEscape(ic.prefix)+"/namespaces", body, &out, true); err != nil {
		return nil, err
	}
	return out, nil
}

func (ic *IcebergCatalog) ListNamespaces(ctx context.Context, parent ...[]string) ([][]string, error) {
	fullURL := ic.analytics.url + "/v1/" + url.PathEscape(ic.prefix) + "/namespaces"
	if len(parent) > 0 && len(parent[0]) > 0 {
		fullURL += "?parent=" + url.QueryEscape(strings.Join(parent[0], "\x1f"))
	}
	var out struct {
		Namespaces [][]string `json:"namespaces"`
	}
	if err := ic.analytics.doJSON(ctx, http.MethodGet, fullURL, nil, &out, true); err != nil {
		return nil, err
	}
	return out.Namespaces, nil
}

func (ic *IcebergCatalog) DropNamespace(ctx context.Context, namespace []string) error {
	return ic.analytics.doJSON(ctx, http.MethodDelete, ic.nsPath(namespace), nil, nil, true)
}

func (ic *IcebergCatalog) LoadNamespaceMetadata(ctx context.Context, namespace []string) (map[string]any, error) {
	var out map[string]any
	if err := ic.analytics.doJSON(ctx, http.MethodGet, ic.nsPath(namespace), nil, &out, true); err != nil {
		return nil, err
	}
	return out, nil
}

func (ic *IcebergCatalog) NamespaceExists(ctx context.Context, namespace []string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ic.nsPath(namespace), nil)
	if err != nil {
		return false, err
	}
	for k, v := range ic.analytics.headers {
		req.Header.Set(k, v)
	}
	resp, err := ic.analytics.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	return false, decodeIcebergError(nil, resp.StatusCode, resp.Status)
}

type IcebergTableIdentifier struct {
	Namespace []string `json:"namespace"`
	Name      string   `json:"name"`
}

func (ic *IcebergCatalog) CreateTable(ctx context.Context, namespace []string, spec map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := ic.analytics.doJSON(ctx, http.MethodPost, ic.nsPath(namespace)+"/tables", spec, &out, true); err != nil {
		return nil, err
	}
	return out, nil
}

func (ic *IcebergCatalog) ListTables(ctx context.Context, namespace []string) ([]IcebergTableIdentifier, error) {
	var out struct {
		Identifiers []IcebergTableIdentifier `json:"identifiers"`
	}
	if err := ic.analytics.doJSON(ctx, http.MethodGet, ic.nsPath(namespace)+"/tables", nil, &out, true); err != nil {
		return nil, err
	}
	return out.Identifiers, nil
}

func (ic *IcebergCatalog) LoadTable(ctx context.Context, namespace []string, table string) (map[string]any, error) {
	var out map[string]any
	if err := ic.analytics.doJSON(ctx, http.MethodGet, ic.nsPath(namespace)+"/tables/"+url.PathEscape(table), nil, &out, true); err != nil {
		return nil, err
	}
	return out, nil
}

func (ic *IcebergCatalog) UpdateTable(ctx context.Context, namespace []string, table string, commit map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := ic.analytics.doJSON(ctx, http.MethodPost, ic.nsPath(namespace)+"/tables/"+url.PathEscape(table), commit, &out, true); err != nil {
		return nil, err
	}
	return out, nil
}

func (ic *IcebergCatalog) RenameTable(ctx context.Context, source, destination IcebergTableIdentifier) error {
	return ic.analytics.doJSON(ctx, http.MethodPost, ic.analytics.url+"/v1/"+url.PathEscape(ic.prefix)+"/tables/rename", map[string]any{
		"source":      source,
		"destination": destination,
	}, nil, true)
}

func (ic *IcebergCatalog) DropTable(ctx context.Context, namespace []string, table string, purge ...bool) error {
	fullURL := ic.nsPath(namespace) + "/tables/" + url.PathEscape(table)
	if len(purge) > 0 && purge[0] {
		fullURL += "?purgeRequested=true"
	}
	return ic.analytics.doJSON(ctx, http.MethodDelete, fullURL, nil, nil, true)
}

func (ic *IcebergCatalog) TableExists(ctx context.Context, namespace []string, table string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, ic.nsPath(namespace)+"/tables/"+url.PathEscape(table), nil)
	if err != nil {
		return false, err
	}
	for k, v := range ic.analytics.headers {
		req.Header.Set(k, v)
	}
	resp, err := ic.analytics.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	return false, decodeIcebergError(nil, resp.StatusCode, resp.Status)
}
