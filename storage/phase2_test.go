package storage_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rusmanplatd/goravel-supabase/storage"
)

func TestStoragePhase2NewHostnamePathTraversalSetHeaderAndErrors(t *testing.T) {
	// Multi-TLD new hostname rewrite
	for _, tc := range []struct {
		in, want string
	}{
		{"https://p.supabase.co/storage/v1", "https://p.storage.supabase.co/storage/v1"},
		{"https://p.supabase.in/storage/v1", "https://p.storage.supabase.in/storage/v1"},
		{"https://p.supabase.red/storage/v1", "https://p.storage.supabase.red/storage/v1"},
		{"https://p.storage.supabase.in/storage/v1", "https://p.storage.supabase.in/storage/v1"},
	} {
		got := storage.New(tc.in, storage.WithUseNewHostname(true)).URL()
		if got != tc.want {
			t.Errorf("WithUseNewHostname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	var gotCustomHeader, gotCopyMeta, gotSignedUpMeta, gotDownloadNonce, gotAnalyticsSort string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustomHeader = r.Header.Get("X-Custom")
		switch {
		case r.URL.Path == "/storage/v1/object/upload/sign/b/a.png":
			gotSignedUpMeta = r.Header.Get("x-metadata")
			_, _ = w.Write([]byte(`{"Key":"b/a.png"}`))
		case r.URL.Path == "/storage/v1/object/b/a.png" && r.Method == http.MethodGet:
			gotDownloadNonce = r.URL.Query().Get("cacheNonce")
			_, _ = w.Write([]byte("ok"))
		case r.URL.Path == "/storage/v1/object/copy":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if m, ok := body["metadata"].(map[string]any); ok {
				gotCopyMeta, _ = m["mimetype"].(string)
			}
			_, _ = w.Write([]byte(`{"Key":"b/c.png"}`))
		case r.URL.Path == "/storage/v1/iceberg/bucket":
			gotAnalyticsSort = r.URL.Query().Get("sortColumn") + ":" + r.URL.Query().Get("sortOrder")
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/storage/v1/iceberg/v1/an/namespaces/bad":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"ns missing","type":"NoSuchNamespaceException","code":404}}`))
		case r.URL.Path == "/storage/v1/vector/GetVectorBucket":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"S3VectorNotFoundException","message":"not found"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := storage.New(srv.URL+"/storage/v1", storage.WithHTTPClient(srv.Client()), storage.WithHeaders(map[string]string{"x-custom": "old"}))
	c.SetHeader("X-Custom", "new")
	ctx := context.Background()
	f := c.From("b")

	// Path traversal rejected before network call
	if _, err := f.Download(ctx, "../secret"); err == nil {
		t.Fatal("expected error for .. path traversal on Download")
	}
	if _, err := f.Upload(ctx, "a/../../secret", strings.NewReader("x")); err == nil {
		t.Fatal("expected error for .. path traversal on Upload")
	}

	// UploadToSignedUrl forwards base64 x-metadata + sets FullPath
	upResp, err := f.UploadToSignedUrl(ctx, "a.png", "tok", strings.NewReader("x"), storage.FileOptions{
		Metadata: map[string]any{"k": "v"},
	})
	if err != nil || upResp.FullPath != "b/a.png" {
		t.Fatalf("UploadToSignedUrl = %+v, %v", upResp, err)
	}
	wantMeta := base64.StdEncoding.EncodeToString([]byte(`{"k":"v"}`))
	if gotSignedUpMeta != wantMeta {
		t.Fatalf("UploadToSignedUrl x-metadata = %q, want %q", gotSignedUpMeta, wantMeta)
	}
	if gotCustomHeader != "new" {
		t.Fatalf("SetHeader X-Custom = %q, want new", gotCustomHeader)
	}

	// Download with CacheNonce
	if _, err := f.Download(ctx, "a.png", storage.DownloadOptions{CacheNonce: "n2"}); err != nil || gotDownloadNonce != "n2" {
		t.Fatalf("Download CacheNonce = %q, err = %v", gotDownloadNonce, err)
	}

	// Copy with Metadata override
	copyMeta := false
	if _, err := f.Copy(ctx, "a.png", "c.png", storage.CopyMoveOptions{
		CopyMetadata: &copyMeta,
		Metadata:     &storage.CopyMetadataOverrides{Mimetype: "image/webp"},
	}); err != nil || gotCopyMeta != "image/webp" {
		t.Fatalf("Copy metadata = %q, err = %v", gotCopyMeta, err)
	}

	// Analytics ListBuckets sortColumn/sortOrder + IcebergError decoding
	if _, err := c.Analytics().ListBuckets(ctx, storage.ListBucketsOptions{SortColumn: "name", SortOrder: "asc"}); err != nil || gotAnalyticsSort != "name:asc" {
		t.Fatalf("Analytics sort = %q, err = %v", gotAnalyticsSort, err)
	}
	cat, _ := c.Analytics().From("an")
	_, err = cat.LoadNamespaceMetadata(ctx, []string{"bad"})
	var iceErr *storage.IcebergError
	if !errors.As(err, &iceErr) || iceErr.Type != "NoSuchNamespaceException" {
		t.Fatalf("expected IcebergError, got %v", err)
	}

	// StorageVectorsApiError decoding + >500 bounds
	_, err = c.Vectors().GetBucket(ctx, "missing")
	var vecErr *storage.StorageVectorsApiError
	if !errors.As(err, &vecErr) || vecErr.Code != string(storage.VectorsErrNotFound) {
		t.Fatalf("expected StorageVectorsApiError S3VectorNotFoundException, got %v", err)
	}
	tooMany := make([]string, 501)
	if err := c.Vectors().From("vb").Index("idx").DeleteVectors(ctx, tooMany); err == nil {
		t.Fatal("expected error for >500 keys in DeleteVectors")
	}

	// StorageUnknownError on network failure
	closedSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedSrv.Close()
	badClient := storage.New(closedSrv.URL+"/storage/v1", storage.WithHTTPClient(closedSrv.Client()))
	_, err = badClient.GetBucket(ctx, "any")
	var unkErr *storage.StorageUnknownError
	if !errors.As(err, &unkErr) || unkErr.OriginalError == nil {
		t.Fatalf("expected StorageUnknownError on network error, got %T (%v)", err, err)
	}
}
