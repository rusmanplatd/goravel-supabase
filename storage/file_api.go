package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// FileApi operates on objects inside a single Storage bucket.
type FileApi struct {
	client   *Client
	bucketID string
}

type FileOptions struct {
	CacheControl    string
	ContentType     string
	ContentEncoding string
	Upsert          bool
	Duplex          string
	Metadata        map[string]any
	Headers         map[string]string
}

type UploadResponse struct {
	ID       string `json:"id,omitempty"`
	Key      string `json:"Key,omitempty"`
	Path     string `json:"path,omitempty"`
	FullPath string `json:"fullPath,omitempty"`
}

type TransformOptions struct {
	Width   int
	Height  int
	Resize  string // "cover" | "contain" | "fill"
	Quality int
	Format  string // "origin"
}

type DownloadOptions struct {
	VersionID string
	Transform *TransformOptions
}

type SignedUrlOptions struct {
	Download  string // "" (none), "true", or filename
	Transform *TransformOptions
}

type SignedUrlResponse struct {
	SignedURL string `json:"signedURL"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

type SignedUploadUrlResponse struct {
	SignedURL string `json:"signedUrl"`
	Token     string `json:"token"`
	Path      string `json:"path"`
}

type PublicUrlOptions struct {
	Download  string
	Transform *TransformOptions
	Nonce     string
}

type CopyMoveOptions struct {
	DestinationBucket string
	SourceVersionID   string
}

type DeleteObjectEntry struct {
	Name      string `json:"name"`
	VersionID string `json:"versionId,omitempty"`
}

type FileObject struct {
	Name           string         `json:"name"`
	BucketID       string         `json:"bucket_id,omitempty"`
	Owner          string         `json:"owner,omitempty"`
	ID             string         `json:"id,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	LastAccessedAt string         `json:"last_accessed_at,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type FileObjectV2 struct {
	ID             string         `json:"id,omitempty"`
	Version        string         `json:"version,omitempty"`
	Name           string         `json:"name"`
	BucketID       string         `json:"bucket_id,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	LastAccessedAt string         `json:"last_accessed_at,omitempty"`
	Size           int64          `json:"size,omitempty"`
	CacheControl   string         `json:"cache_control,omitempty"`
	ContentType    string         `json:"content_type,omitempty"`
	ETag           string         `json:"etag,omitempty"`
	IsLatest       *bool          `json:"is_latest,omitempty"`
	IsDeleteMarker *bool          `json:"is_delete_marker,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type SearchOptions struct {
	Limit  int     `json:"limit,omitempty"`
	Offset int     `json:"offset,omitempty"`
	SortBy *SortBy `json:"sortBy,omitempty"`
	Search string  `json:"search,omitempty"`
}

type SortBy struct {
	Column string `json:"column,omitempty"`
	Order  string `json:"order,omitempty"`
}

type SearchV2Options struct {
	Prefix             string `json:"prefix,omitempty"`
	Cursor             string `json:"cursor,omitempty"`
	Limit              int    `json:"limit,omitempty"`
	WithDelimiter      bool   `json:"with_delimiter,omitempty"`
	NoncurrentVersions bool   `json:"noncurrent_versions,omitempty"`
	DeleteMarkers      bool   `json:"delete_markers,omitempty"`
	ExactMatch         bool   `json:"exact_match,omitempty"`
}

type SearchV2Result struct {
	HasNext    bool           `json:"hasNext"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Folders    []map[string]any `json:"folders,omitempty"`
	Objects    []FileObjectV2 `json:"objects,omitempty"`
}

type InfoOptions struct {
	VersionID string
}

func cleanPath(p string) string {
	return strings.TrimLeft(p, "/")
}

func (f *FileApi) uploadOrUpdate(ctx context.Context, method, path string, body io.Reader, opts ...FileOptions) (*UploadResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opt FileOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	fullURL := f.client.url + "/object/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
	}
	f.client.applyHeaders(req)
	if opt.ContentType != "" {
		req.Header.Set("Content-Type", opt.ContentType)
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if opt.CacheControl != "" {
		req.Header.Set("cache-control", "max-age="+opt.CacheControl)
	}
	if opt.ContentEncoding != "" {
		req.Header.Set("Content-Encoding", opt.ContentEncoding)
	}
	if opt.Upsert && method == http.MethodPost {
		req.Header.Set("x-upsert", "true")
	}
	if opt.Metadata != nil {
		if mb, err := json.Marshal(opt.Metadata); err == nil {
			req.Header.Set("x-metadata", string(mb))
		}
	}
	for k, v := range opt.Headers {
		req.Header.Set(k, v)
	}

	resp, err := f.client.httpClient.Do(req)
	if err != nil {
		return nil, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeStorageApiError(raw, resp.StatusCode, resp.Status)
	}

	var out UploadResponse
	_ = json.Unmarshal(raw, &out)
	out.Path = cleanPath(path)
	if out.FullPath == "" && out.Key != "" {
		out.FullPath = out.Key
	}
	return &out, nil
}

func (f *FileApi) Upload(ctx context.Context, path string, body io.Reader, opts ...FileOptions) (*UploadResponse, error) {
	return f.uploadOrUpdate(ctx, http.MethodPost, path, body, opts...)
}

func (f *FileApi) Update(ctx context.Context, path string, body io.Reader, opts ...FileOptions) (*UploadResponse, error) {
	return f.uploadOrUpdate(ctx, http.MethodPut, path, body, opts...)
}

func (f *FileApi) UploadToSignedUrl(ctx context.Context, path, token string, body io.Reader, opts ...FileOptions) (*UploadResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opt FileOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	fullURL := f.client.url + "/object/upload/sign/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path) + "?token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, fullURL, body)
	if err != nil {
		return nil, &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
	}
	f.client.applyHeaders(req)
	if opt.ContentType != "" {
		req.Header.Set("Content-Type", opt.ContentType)
	}
	if opt.CacheControl != "" {
		req.Header.Set("cache-control", "max-age="+opt.CacheControl)
	}
	if opt.Upsert {
		req.Header.Set("x-upsert", "true")
	}

	resp, err := f.client.httpClient.Do(req)
	if err != nil {
		return nil, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeStorageApiError(raw, resp.StatusCode, resp.Status)
	}
	var out UploadResponse
	_ = json.Unmarshal(raw, &out)
	out.Path = cleanPath(path)
	return &out, nil
}

func applyTransformQuery(q url.Values, t *TransformOptions) {
	if t == nil {
		return
	}
	if t.Width > 0 {
		q.Set("width", itoa(t.Width))
	}
	if t.Height > 0 {
		q.Set("height", itoa(t.Height))
	}
	if t.Resize != "" {
		q.Set("resize", t.Resize)
	}
	if t.Quality > 0 {
		q.Set("quality", itoa(t.Quality))
	}
	if t.Format != "" {
		q.Set("format", t.Format)
	}
}

func (f *FileApi) downloadURL(path string, opts ...DownloadOptions) string {
	var opt DownloadOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	base := f.client.url + "/object/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
	q := url.Values{}
	if opt.Transform != nil {
		base = f.client.url + "/render/image/authenticated/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
		applyTransformQuery(q, opt.Transform)
	}
	if opt.VersionID != "" {
		q.Set("versionId", opt.VersionID)
	}
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

// DownloadStream returns the unbuffered response body stream for the object. Caller must close it.
func (f *FileApi) DownloadStream(ctx context.Context, path string, opts ...DownloadOptions) (io.ReadCloser, http.Header, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.downloadURL(path, opts...), nil)
	if err != nil {
		return nil, nil, &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
	}
	f.client.applyHeaders(req)

	resp, err := f.client.httpClient.Do(req)
	if err != nil {
		return nil, nil, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, resp.Header, decodeStorageApiError(raw, resp.StatusCode, resp.Status)
	}
	return resp.Body, resp.Header, nil
}

// Download fetches the full object bytes.
func (f *FileApi) Download(ctx context.Context, path string, opts ...DownloadOptions) ([]byte, error) {
	rc, _, err := f.DownloadStream(ctx, path, opts...)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func (f *FileApi) CreateSignedUrl(ctx context.Context, path string, expiresIn int, opts ...SignedUrlOptions) (string, error) {
	body := map[string]any{"expiresIn": expiresIn}
	var opt SignedUrlOptions
	if len(opts) > 0 {
		opt = opts[0]
		if opt.Transform != nil {
			body["transform"] = map[string]any{
				"width":   opt.Transform.Width,
				"height":  opt.Transform.Height,
				"resize":  opt.Transform.Resize,
				"quality": opt.Transform.Quality,
				"format":  opt.Transform.Format,
			}
		}
	}
	var resp SignedUrlResponse
	if err := f.client.doJSON(ctx, http.MethodPost, "/object/sign/"+url.PathEscape(f.bucketID)+"/"+cleanPath(path), body, &resp); err != nil {
		return "", err
	}
	full := f.client.url + resp.SignedURL
	if opt.Download != "" {
		sep := "?"
		if strings.Contains(full, "?") {
			sep = "&"
		}
		if opt.Download == "true" {
			full += sep + "download="
		} else {
			full += sep + "download=" + url.QueryEscape(opt.Download)
		}
	}
	return full, nil
}

func (f *FileApi) CreateSignedUrls(ctx context.Context, paths []string, expiresIn int, opts ...SignedUrlOptions) ([]SignedUrlResponse, error) {
	body := map[string]any{
		"expiresIn": expiresIn,
		"paths":     paths,
	}
	var resp []SignedUrlResponse
	if err := f.client.doJSON(ctx, http.MethodPost, "/object/sign/"+url.PathEscape(f.bucketID), body, &resp); err != nil {
		return nil, err
	}
	for i := range resp {
		if resp[i].SignedURL != "" && !strings.HasPrefix(resp[i].SignedURL, "http") {
			resp[i].SignedURL = f.client.url + resp[i].SignedURL
		}
	}
	return resp, nil
}

func (f *FileApi) CreateSignedUploadUrl(ctx context.Context, path string, upsert ...bool) (*SignedUploadUrlResponse, error) {
	reqPath := "/object/upload/sign/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.client.url+reqPath, nil)
	if err != nil {
		return nil, &StorageError{Name: "StorageError", Message: err.Error(), Err: err}
	}
	f.client.applyHeaders(req)
	if len(upsert) > 0 && upsert[0] {
		req.Header.Set("x-upsert", "true")
	}
	resp, err := f.client.httpClient.Do(req)
	if err != nil {
		return nil, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeStorageApiError(raw, resp.StatusCode, resp.Status)
	}
	var payload struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &payload)
	fullURL := f.client.url + payload.URL
	parsed, _ := url.Parse(fullURL)
	token := ""
	if parsed != nil {
		token = parsed.Query().Get("token")
	}
	return &SignedUploadUrlResponse{
		SignedURL: fullURL,
		Token:     token,
		Path:      cleanPath(path),
	}, nil
}

func (f *FileApi) GetPublicUrl(path string, opts ...PublicUrlOptions) string {
	var opt PublicUrlOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	base := f.client.url + "/object/public/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
	q := url.Values{}
	if opt.Transform != nil {
		base = f.client.url + "/render/image/public/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
		applyTransformQuery(q, opt.Transform)
	}
	if opt.Download != "" {
		if opt.Download == "true" {
			q.Set("download", "")
		} else {
			q.Set("download", opt.Download)
		}
	}
	if opt.Nonce != "" {
		q.Set("nonce", opt.Nonce)
	}
	if enc := q.Encode(); enc != "" {
		return base + "?" + enc
	}
	return base
}

func (f *FileApi) Copy(ctx context.Context, fromPath, toPath string, opts ...CopyMoveOptions) (string, error) {
	body := map[string]any{
		"bucketId":       f.bucketID,
		"sourceKey":      cleanPath(fromPath),
		"destinationKey": cleanPath(toPath),
	}
	if len(opts) > 0 {
		if opts[0].DestinationBucket != "" {
			body["destinationBucket"] = opts[0].DestinationBucket
		}
		if opts[0].SourceVersionID != "" {
			body["sourceVersionId"] = opts[0].SourceVersionID
		}
	}
	var resp struct {
		Key string `json:"Key"`
	}
	if err := f.client.doJSON(ctx, http.MethodPost, "/object/copy", body, &resp); err != nil {
		return "", err
	}
	return resp.Key, nil
}

func (f *FileApi) Move(ctx context.Context, fromPath, toPath string, opts ...CopyMoveOptions) (string, error) {
	body := map[string]any{
		"bucketId":       f.bucketID,
		"sourceKey":      cleanPath(fromPath),
		"destinationKey": cleanPath(toPath),
	}
	if len(opts) > 0 {
		if opts[0].DestinationBucket != "" {
			body["destinationBucket"] = opts[0].DestinationBucket
		}
		if opts[0].SourceVersionID != "" {
			body["sourceVersionId"] = opts[0].SourceVersionID
		}
	}
	var resp struct {
		Message string `json:"message"`
	}
	if err := f.client.doJSON(ctx, http.MethodPost, "/object/move", body, &resp); err != nil {
		return "", err
	}
	return resp.Message, nil
}

func (f *FileApi) Remove(ctx context.Context, paths []string) ([]FileObject, error) {
	var out []FileObject
	if err := f.client.doJSON(ctx, http.MethodDelete, "/object/"+url.PathEscape(f.bucketID), map[string]any{
		"prefixes": paths,
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveVersions deletes specific object versions (or delete markers) using DeleteObjectEntry items.
func (f *FileApi) RemoveVersions(ctx context.Context, entries []DeleteObjectEntry) ([]FileObject, error) {
	var out []FileObject
	if err := f.client.doJSON(ctx, http.MethodDelete, "/object/"+url.PathEscape(f.bucketID), map[string]any{
		"prefixes": entries,
	}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *FileApi) List(ctx context.Context, path string, opts ...SearchOptions) ([]FileObject, error) {
	body := map[string]any{
		"prefix": path,
		"limit":  100,
		"offset": 0,
		"sortBy": map[string]string{"column": "name", "order": "asc"},
	}
	if len(opts) > 0 {
		opt := opts[0]
		if opt.Limit > 0 {
			body["limit"] = opt.Limit
		}
		if opt.Offset > 0 {
			body["offset"] = opt.Offset
		}
		if opt.SortBy != nil {
			body["sortBy"] = opt.SortBy
		}
		if opt.Search != "" {
			body["search"] = opt.Search
		}
	}
	var out []FileObject
	if err := f.client.doJSON(ctx, http.MethodPost, "/object/list/"+url.PathEscape(f.bucketID), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *FileApi) ListV2(ctx context.Context, opts ...SearchV2Options) (*SearchV2Result, error) {
	var opt SearchV2Options
	if len(opts) > 0 {
		opt = opts[0]
	}
	var out SearchV2Result
	if err := f.client.doJSON(ctx, http.MethodPost, "/object/list-v2/"+url.PathEscape(f.bucketID), opt, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (f *FileApi) Exists(ctx context.Context, path string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, f.client.url+"/object/"+url.PathEscape(f.bucketID)+"/"+cleanPath(path), nil)
	if err != nil {
		return false, err
	}
	f.client.applyHeaders(req)
	resp, err := f.client.httpClient.Do(req)
	if err != nil {
		return false, &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		return false, nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	return false, decodeStorageApiError(nil, resp.StatusCode, resp.Status)
}

func (f *FileApi) Info(ctx context.Context, path string, opts ...InfoOptions) (*FileObjectV2, error) {
	reqPath := "/object/info/" + url.PathEscape(f.bucketID) + "/" + cleanPath(path)
	if len(opts) > 0 && opts[0].VersionID != "" {
		reqPath += "?versionId=" + url.QueryEscape(opts[0].VersionID)
	}
	var out FileObjectV2
	if err := f.client.doJSON(ctx, http.MethodGet, reqPath, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (f *FileApi) PurgeCache(ctx context.Context, path string, opts ...PurgeCacheOptions) error {
	body := map[string]any{}
	if len(opts) > 0 && opts[0].Transformations {
		body["transformations"] = true
	}
	return f.client.doJSON(ctx, http.MethodPost, "/object/purge/"+url.PathEscape(f.bucketID)+"/"+cleanPath(path), body, nil)
}

func (f *FileApi) PurgeBucketCache(ctx context.Context, opts ...PurgeCacheOptions) error {
	return f.client.PurgeBucketCache(ctx, f.bucketID, opts...)
}
