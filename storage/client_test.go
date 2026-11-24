package storage_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rusmanplatd/goravel-supabase/storage"
)

func TestStorageBucketsLifecycleAndNewHostname(t *testing.T) {
	rewritten := storage.New("https://myproj.supabase.co/storage/v1", storage.WithUseNewHostname(true))
	if rewritten.URL() != "https://myproj.storage.supabase.co/storage/v1" {
		t.Fatalf("rewritten URL = %q", rewritten.URL())
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/storage/v1/bucket" && r.Method == http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode CreateBucket body: %v", err)
			}
			if body["versioning_status"] != "ENABLED" {
				t.Errorf("CreateBucket versioning_status = %v, want ENABLED", body["versioning_status"])
			}
			_, _ = w.Write([]byte(`{"name":"avatars"}`))
		case r.URL.Path == "/storage/v1/bucket" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"avatars","name":"avatars","public":true,"versioning_status":"ENABLED"}]`))
		case r.URL.Path == "/storage/v1/bucket/avatars" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":"avatars","name":"avatars","public":true,"versioning_status":"ENABLED"}`))
		case r.URL.Path == "/storage/v1/bucket/avatars" && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		case r.URL.Path == "/storage/v1/bucket/avatars/empty":
			_, _ = w.Write([]byte(`{"message":"emptied"}`))
		case r.URL.Path == "/storage/v1/bucket/avatars/lifecycle" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
			_, _ = w.Write([]byte(`{"rules":[{"id":"r1","status":"Enabled"}]}`))
		case r.URL.Path == "/storage/v1/bucket/avatars/lifecycle" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/storage/v1/bucket/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"statusCode":"404","error":"Bucket not found","message":"Bucket not found"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := storage.New(srv.URL+"/storage/v1", storage.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	if name, err := c.CreateBucket(ctx, "avatars", storage.BucketOptions{Public: true, VersioningStatus: storage.VersioningEnabled}); err != nil || name != "avatars" {
		t.Fatalf("CreateBucket = %q, %v", name, err)
	}
	if b, err := c.GetBucket(ctx, "avatars"); err != nil || b.VersioningStatus != storage.VersioningEnabled {
		t.Fatalf("GetBucket = %+v, %v", b, err)
	}
	if list, err := c.ListBuckets(ctx, storage.ListBucketsOptions{Limit: 10}); err != nil || len(list) != 1 {
		t.Fatalf("ListBuckets = %+v, %v", list, err)
	}
	if _, err := c.UpdateBucket(ctx, "avatars", storage.BucketOptions{Public: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EmptyBucket(ctx, "avatars"); err != nil {
		t.Fatal(err)
	}
	if lc, err := c.UpdateBucketLifecycle(ctx, "avatars", storage.BucketLifecycleConfiguration{
		Rules: []storage.LifecycleRule{{ID: "r1", Status: "Enabled"}},
	}); err != nil || len(lc.Rules) != 1 {
		t.Fatalf("UpdateBucketLifecycle = %+v, %v", lc, err)
	}
	if _, err := c.GetBucketLifecycle(ctx, "avatars"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteBucketLifecycle(ctx, "avatars"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteBucket(ctx, "avatars"); err != nil {
		t.Fatal(err)
	}

	_, err := c.GetBucket(ctx, "missing")
	var apiErr *storage.StorageApiError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || apiErr.Code != "Bucket not found" {
		t.Fatalf("expected StorageApiError 404, got %v", err)
	}
}

func TestStorageFileOperations(t *testing.T) {
	var gotDownloadPath, gotDownloadQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/storage/v1/object/avatars/a.png" && r.Method == http.MethodPost:
			wantMeta := base64.StdEncoding.EncodeToString([]byte(`{"k":"v"}`))
			if got := r.Header.Get("x-metadata"); got != wantMeta {
				t.Errorf("Upload x-metadata = %q, want base64 %q", got, wantMeta)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"id1","Key":"avatars/a.png"}`))
		case r.URL.Path == "/storage/v1/object/avatars/a.png" && r.Method == http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"id1","Key":"avatars/a.png"}`))
		case r.URL.Path == "/storage/v1/render/image/authenticated/avatars/a.png":
			gotDownloadPath = r.URL.Path
			gotDownloadQuery = r.URL.RawQuery
			_, _ = w.Write([]byte("img-bytes"))
		case r.URL.Path == "/storage/v1/object/sign/avatars/a.png":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["versionId"] != "v1" {
				t.Errorf("CreateSignedUrl versionId = %v, want v1", body["versionId"])
			}
			tr, ok := body["transform"].(map[string]any)
			if !ok || tr["width"] != float64(200) {
				t.Errorf("CreateSignedUrl transform = %v", body["transform"])
			}
			if _, hasZero := tr["height"]; hasZero {
				t.Errorf("CreateSignedUrl transform should omit zero height: %v", tr)
			}
			_, _ = w.Write([]byte(`{"signedURL":"/object/sign/avatars/a.png?token=tok1"}`))
		case r.URL.Path == "/storage/v1/object/sign/avatars":
			_, _ = w.Write([]byte(`[{"signedURL":"/object/sign/avatars/a.png?token=tok1","path":"a.png"}]`))
		case r.URL.Path == "/storage/v1/object/upload/sign/avatars/a.png" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"url":"/object/upload/sign/avatars/a.png?token=up1"}`))
		case r.URL.Path == "/storage/v1/object/upload/sign/avatars/a.png" && r.Method == http.MethodPut:
			_, _ = w.Write([]byte(`{"Key":"avatars/a.png"}`))
		case r.URL.Path == "/storage/v1/object/copy":
			_, _ = w.Write([]byte(`{"Key":"dest/b.png"}`))
		case r.URL.Path == "/storage/v1/object/move":
			_, _ = w.Write([]byte(`{"message":"moved"}`))
		case r.URL.Path == "/storage/v1/object/avatars" && r.Method == http.MethodDelete:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if prefixes, ok := body["prefixes"].([]any); ok && len(prefixes) > 0 {
				if entry, isObj := prefixes[0].(map[string]any); isObj {
					if entry["path"] != "a.png" || entry["versionId"] != "v1" {
						t.Errorf("RemoveVersions entry = %v, want path+versionId", entry)
					}
					if _, hasName := entry["name"]; hasName {
						t.Errorf("RemoveVersions must not send 'name': %v", entry)
					}
				}
			}
			_, _ = w.Write([]byte(`[{"name":"a.png"}]`))
		case r.URL.Path == "/storage/v1/object/list/avatars":
			_, _ = w.Write([]byte(`[{"name":"a.png"}]`))
		case r.URL.Path == "/storage/v1/object/list-v2/avatars":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["noncurrentVersions"] != "include" || body["deleteMarkers"] != "exclude" || body["exactMatch"] != true {
				t.Errorf("ListV2 body = %v", body)
			}
			if sb, ok := body["sortBy"].(map[string]any); !ok || sb["column"] != "name" || sb["order"] != "desc" {
				t.Errorf("ListV2 sortBy = %v", body["sortBy"])
			}
			_, _ = w.Write([]byte(`{"hasNext":false,"objects":[{"name":"a.png","version":"v1"}]}`))
		case r.URL.Path == "/storage/v1/object/info/avatars/a.png":
			_, _ = w.Write([]byte(`{"name":"a.png","version":"v1","size":9}`))
		case r.URL.Path == "/storage/v1/object/info/avatars/missing.png":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"statusCode":"404","code":"NoSuchKey","message":"Object not found"}`))
		case r.URL.Path == "/storage/v1/object/info/avatars/invalid-jwt.png":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"statusCode":"400","code":"InvalidJWT","message":"Invalid JWT"}`))
		case r.URL.Path == "/storage/v1/cdn/avatars/a.png" && r.Method == http.MethodDelete:
			if r.URL.RawQuery != "" {
				t.Errorf("PurgeCache unexpected query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"message":"Successfully purged"}`))
		case r.URL.Path == "/storage/v1/cdn/avatars" && r.Method == http.MethodDelete:
			if r.URL.Query().Get("transformations") != "true" {
				t.Errorf("PurgeBucketCache query = %q, want transformations=true", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"message":"Successfully purged bucket"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	f := storage.New(srv.URL+"/storage/v1", storage.WithHTTPClient(srv.Client())).From("avatars")
	ctx := context.Background()

	if _, err := f.Upload(ctx, "a.png", strings.NewReader("data"), storage.FileOptions{Upsert: true, ContentType: "image/png", Metadata: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Update(ctx, "a.png", strings.NewReader("data2")); err != nil {
		t.Fatal(err)
	}
	data, err := f.Download(ctx, "a.png", storage.DownloadOptions{
		VersionID: "v1",
		Transform: &storage.TransformOptions{Width: 200, Height: 200, Format: "origin"},
	})
	if err != nil || string(data) != "img-bytes" {
		t.Fatalf("Download = %q, %v", string(data), err)
	}
	if gotDownloadPath != "/storage/v1/render/image/authenticated/avatars/a.png" || !strings.Contains(gotDownloadQuery, "versionId=v1") || !strings.Contains(gotDownloadQuery, "width=200") {
		t.Fatalf("download url = %s?%s", gotDownloadPath, gotDownloadQuery)
	}
	rc, _, err := f.DownloadStream(ctx, "a.png", storage.DownloadOptions{Transform: &storage.TransformOptions{Width: 100}})
	if err != nil {
		t.Fatal(err)
	}
	streamBytes, _ := io.ReadAll(rc)
	rc.Close()
	if string(streamBytes) != "img-bytes" {
		t.Fatalf("stream = %q", string(streamBytes))
	}

	if su, err := f.CreateSignedUrl(ctx, "a.png", 60, storage.SignedUrlOptions{
		Download:   "avatar.png",
		CacheNonce: "c1",
		VersionID:  "v1",
		Transform:  &storage.TransformOptions{Width: 200},
	}); err != nil || !strings.Contains(su, "download=avatar.png") || !strings.Contains(su, "cacheNonce=c1") {
		t.Fatalf("CreateSignedUrl = %q, %v", su, err)
	}
	if sus, err := f.CreateSignedUrls(ctx, []string{"a.png"}, 60, storage.SignedUrlOptions{Download: "true", CacheNonce: "c2"}); err != nil || len(sus) != 1 || !strings.Contains(sus[0].SignedURL, "download=") || !strings.Contains(sus[0].SignedURL, "cacheNonce=c2") {
		t.Fatalf("CreateSignedUrls = %+v, %v", sus, err)
	}
	up, err := f.CreateSignedUploadUrl(ctx, "a.png", true)
	if err != nil || up.Token != "up1" {
		t.Fatalf("CreateSignedUploadUrl = %+v, %v", up, err)
	}
	if _, err := f.UploadToSignedUrl(ctx, "a.png", up.Token, strings.NewReader("xyz")); err != nil {
		t.Fatal(err)
	}
	pub := f.GetPublicUrl("a.png", storage.PublicUrlOptions{CacheNonce: "n1", VersionID: "v1", Transform: &storage.TransformOptions{Width: 50}})
	if !strings.Contains(pub, "/render/image/public/avatars/a.png") || !strings.Contains(pub, "cacheNonce=n1") || !strings.Contains(pub, "versionId=v1") {
		t.Fatalf("GetPublicUrl = %q", pub)
	}
	if _, err := f.Copy(ctx, "a.png", "b.png", storage.CopyMoveOptions{DestinationBucket: "dest", SourceVersionID: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Move(ctx, "a.png", "b.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Remove(ctx, []string{"a.png"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.RemoveVersions(ctx, []storage.DeleteObjectEntry{{Path: "a.png", VersionID: "v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.List(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if lv2, err := f.ListV2(ctx, storage.SearchV2Options{
		NoncurrentVersions: storage.VersionInclude,
		DeleteMarkers:      storage.VersionExclude,
		ExactMatch:         true,
		SortBy:             &storage.SortBy{Column: "name", Order: "desc"},
	}); err != nil || len(lv2.Objects) != 1 {
		t.Fatalf("ListV2 = %+v, %v", lv2, err)
	}
	if exists, err := f.Exists(ctx, "a.png"); err != nil || !exists {
		t.Fatalf("Exists(a.png) = %v, %v", exists, err)
	}
	if exists, err := f.Exists(ctx, "missing.png"); err != nil || exists {
		t.Fatalf("Exists(missing.png) = %v, %v", exists, err)
	}
	if exists, err := f.Exists(ctx, "invalid-jwt.png"); err == nil || exists {
		t.Fatalf("Exists(invalid-jwt.png) should propagate InvalidJWT error, got %v, %v", exists, err)
	}
	if info, err := f.Info(ctx, "a.png", storage.InfoOptions{VersionID: "v1"}); err != nil || info.Version != "v1" {
		t.Fatalf("Info = %+v, %v", info, err)
	}
	if pr, err := f.PurgeCache(ctx, "a.png"); err != nil || pr.Message != "Successfully purged" {
		t.Fatalf("PurgeCache = %+v, %v", pr, err)
	}
	if pr, err := f.PurgeBucketCache(ctx, storage.PurgeCacheOptions{Transformations: true}); err != nil || pr.Message != "Successfully purged bucket" {
		t.Fatalf("PurgeBucketCache = %+v, %v", pr, err)
	}
}

func TestStorageAnalyticsIcebergAndVectors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		// Analytics Buckets
		case r.URL.Path == "/storage/v1/iceberg/bucket" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":"an1","name":"analytics-data"}`))
		case r.URL.Path == "/storage/v1/iceberg/bucket" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"an1","name":"analytics-data"}]`))
		case r.URL.Path == "/storage/v1/iceberg/bucket/analytics-data" && r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"message":"deleted"}`))
		// Iceberg REST Catalog
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"namespace":["db"]}`))
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"namespaces":[["db"]]}`))
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db" && r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"namespace":["db"],"properties":{"owner":"team"}}`))
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db/tables" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"metadata-location":"s3://bucket/meta.json"}`))
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db/tables" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"identifiers":[{"namespace":["db"],"name":"events"}]}`))
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db/tables/events" && r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db/tables/events" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
			_, _ = w.Write([]byte(`{"metadata":{"table-uuid":"123"}}`))
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/namespaces/db/tables/events" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/storage/v1/iceberg/v1/analytics-data/tables/rename":
			w.WriteHeader(http.StatusNoContent)
		// Vectors
		case strings.HasPrefix(r.URL.Path, "/storage/v1/vector/"):
			action := strings.TrimPrefix(r.URL.Path, "/storage/v1/vector/")
			switch action {
			case "GetVectorBucket":
				_, _ = w.Write([]byte(`{"vectorBucket":{"vectorBucketName":"vb1"}}`))
			case "ListVectorBuckets":
				_, _ = w.Write([]byte(`{"vectorBuckets":[{"vectorBucketName":"vb1"}]}`))
			case "GetIndex":
				_, _ = w.Write([]byte(`{"index":{"vectorBucketName":"vb1","indexName":"idx1","dimension":3}}`))
			case "ListIndexes":
				_, _ = w.Write([]byte(`{"indexes":[{"vectorBucketName":"vb1","indexName":"idx1"}]}`))
			case "GetVectors":
				_, _ = w.Write([]byte(`{"vectors":[{"key":"k1","data":{"float32":[1,2,3]}}]}`))
			case "ListVectors":
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["segmentCount"] != float64(4) || body["segmentIndex"] != float64(2) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"vectors":[{"key":"k1"}],"nextToken":"nt1"}`))
			case "QueryVectors":
				_, _ = w.Write([]byte(`{"vectors":[{"key":"k1","distance":0.1}],"nextToken":"nt2"}`))
			default:
				_, _ = w.Write([]byte(`{}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := storage.New(srv.URL+"/storage/v1", storage.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// Analytics + Iceberg
	an := c.Analytics()
	if _, err := an.CreateBucket(ctx, "analytics-data"); err != nil {
		t.Fatal(err)
	}
	if _, err := an.ListBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := an.From(""); err == nil {
		t.Fatal("expected error for empty analytics bucket name")
	}
	cat, err := an.From("analytics-data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.CreateNamespace(ctx, []string{"db"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ListNamespaces(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := cat.NamespaceExists(ctx, []string{"db"}); err != nil || !ok {
		t.Fatalf("NamespaceExists = %v, %v", ok, err)
	}
	if _, err := cat.LoadNamespaceMetadata(ctx, []string{"db"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.CreateTable(ctx, []string{"db"}, map[string]any{"name": "events"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ListTables(ctx, []string{"db"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := cat.TableExists(ctx, []string{"db"}, "events"); err != nil || !ok {
		t.Fatalf("TableExists = %v, %v", ok, err)
	}
	if _, err := cat.LoadTable(ctx, []string{"db"}, "events"); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpdateTable(ctx, []string{"db"}, "events", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := cat.RenameTable(ctx, storage.IcebergTableIdentifier{Namespace: []string{"db"}, Name: "events"}, storage.IcebergTableIdentifier{Namespace: []string{"db"}, Name: "events2"}); err != nil {
		t.Fatal(err)
	}
	if err := cat.DropTable(ctx, []string{"db"}, "events", true); err != nil {
		t.Fatal(err)
	}
	if err := cat.DropNamespace(ctx, []string{"db"}); err != nil {
		t.Fatal(err)
	}
	if _, err := an.DeleteBucket(ctx, "analytics-data"); err != nil {
		t.Fatal(err)
	}

	// Vectors
	vec := c.Vectors()
	if err := vec.CreateBucket(ctx, "vb1"); err != nil {
		t.Fatal(err)
	}
	if _, err := vec.GetBucket(ctx, "vb1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vec.ListBuckets(ctx, "", 10, ""); err != nil {
		t.Fatal(err)
	}
	vb := vec.From("vb1")
	if err := vb.CreateIndex(ctx, storage.CreateIndexOptions{IndexName: "idx1", DataType: "float32", Dimension: 3, DistanceMetric: "cosine"}); err != nil {
		t.Fatal(err)
	}
	if _, err := vb.GetIndex(ctx, "idx1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vb.ListIndexes(ctx, "", 10, ""); err != nil {
		t.Fatal(err)
	}
	idx := vb.Index("idx1")
	if err := idx.PutVectors(ctx, nil); err == nil {
		t.Fatal("expected error for empty PutVectors")
	}
	if err := idx.PutVectors(ctx, []storage.VectorItem{{Key: "k1", Data: map[string]any{"float32": []float32{1, 2, 3}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.GetVectors(ctx, []string{"k1"}, true, true); err != nil {
		t.Fatal(err)
	}
	// Segment bounds validation
	if _, err := idx.ListVectors(ctx, storage.ListVectorsOptions{SegmentCount: 17, SegmentIndex: 0}); err == nil {
		t.Fatal("expected error for segmentCount > 16")
	}
	if _, err := idx.ListVectors(ctx, storage.ListVectorsOptions{SegmentCount: 4, SegmentIndex: 4}); err == nil {
		t.Fatal("expected error for segmentIndex >= segmentCount")
	}
	if lv, err := idx.ListVectors(ctx, storage.ListVectorsOptions{SegmentCount: 4, SegmentIndex: 2}); err != nil || lv.NextToken != "nt1" {
		t.Fatalf("ListVectors = %+v, %v", lv, err)
	}
	if qv, err := idx.QueryVectors(ctx, storage.QueryVectorsOptions{QueryVector: map[string]any{"float32": []float32{1, 2, 3}}, TopK: 5}); err != nil || qv.NextToken != "nt2" {
		t.Fatalf("QueryVectors = %+v, %v", qv, err)
	}
	if err := idx.DeleteVectors(ctx, nil); err == nil {
		t.Fatal("expected error for empty DeleteVectors")
	}
	if err := idx.DeleteVectors(ctx, []string{"k1"}); err != nil {
		t.Fatal(err)
	}
	if err := vb.DeleteIndex(ctx, "idx1"); err != nil {
		t.Fatal(err)
	}
	if err := vec.DeleteBucket(ctx, "vb1"); err != nil {
		t.Fatal(err)
	}
}
