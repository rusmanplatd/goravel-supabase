# Storage (`storage`)

The `storage` package provides a pure-Go Supabase Storage client covering **Bucket management & lifecycle**, **File uploads/downloads/streaming/image transforms/signed URLs**, **Analytics (Apache Iceberg REST Catalog)**, and **Vector Buckets & Indexes**.

- **Package:** `github.com/rusmanplatd/goravel-supabase/storage`
- **Access via Client:** `sb.Storage` (pure-Go) or `facades.Supabase().Storage()` (Goravel)

---

## 1. Bucket Management & Lifecycle Rules

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/storage"
)

func main() {
	sb, err := client.New("https://xyzcompany.supabase.co", "sb_secret_...")
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()

	// Create a private bucket with size & MIME restrictions
	_, err = sb.Storage.CreateBucket(ctx, "avatars", storage.BucketOptions{
		Public:           false,
		FileSizeLimit:    "5MB",
		AllowedMimeTypes: []string{"image/png", "image/jpeg", "image/webp"},
		VersioningStatus: storage.VersioningEnabled,
	})
	if err != nil {
		log.Fatal(err)
	}

	// List & inspect buckets
	buckets, err := sb.Storage.ListBuckets(ctx, storage.ListBucketsOptions{Limit: 50})
	bucket, err := sb.Storage.GetBucket(ctx, "avatars")
	fmt.Printf("Total buckets: %d, selected: %s\n", len(buckets), bucket.Name)

	// Update bucket settings
	_, err = sb.Storage.UpdateBucket(ctx, "avatars", storage.BucketOptions{
		Public:        true,
		FileSizeLimit: "10MB",
	})

	// Configure lifecycle rules
	_, err = sb.Storage.UpdateBucketLifecycle(ctx, "avatars", storage.BucketLifecycleConfiguration{
		Rules: []storage.LifecycleRule{
			{
				ID:     "expire-old-versions",
				Status: "Enabled",
				Prefix: "tmp/",
			},
		},
	})
	_, err = sb.Storage.GetBucketLifecycle(ctx, "avatars")
	_ = sb.Storage.DeleteBucketLifecycle(ctx, "avatars")

	// Purge CDN cache, empty, and delete bucket
	_, _ = sb.Storage.PurgeBucketCache(ctx, "avatars")
	_, _ = sb.Storage.EmptyBucket(ctx, "avatars")
	_, _ = sb.Storage.DeleteBucket(ctx, "avatars")
}
```

---

## 2. File Operations (Upload, Download, Stream, Transforms, Signed URLs)

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/rusmanplatd/goravel-supabase/client"
	"github.com/rusmanplatd/goravel-supabase/storage"
)

func main() {
	sb, err := client.New("https://xyzcompany.supabase.co", "sb_publishable_...")
	if err != nil {
		log.Fatal(err)
	}
	defer sb.Close()

	ctx := context.Background()
	bucket := sb.Storage.From("avatars")

	// 1. Upload & Update
	data := bytes.NewReader([]byte("fake-png-bytes"))
	upRes, err := bucket.Upload(ctx, "users/ada/avatar.png", data, storage.FileOptions{
		ContentType:  "image/png",
		CacheControl: "3600",
		Upsert:       true,
		Metadata:     map[string]any{"uploaded_by": "ada"},
	})
	if err != nil {
		var apiErr *storage.StorageApiError
		if errors.As(err, &apiErr) {
			log.Fatalf("Storage %d: %s", apiErr.StatusCode, apiErr.Message)
		}
		log.Fatal(err)
	}
	fmt.Println("Uploaded key:", upRes.Key)

	// 2. List (v1 & v2), Exists, and Info
	listV1, err := bucket.List(ctx, "users/ada", storage.SearchOptions{Limit: 20, Offset: 0})
	listV2, err := bucket.ListV2(ctx, storage.SearchV2Options{Prefix: "users/ada/", Limit: 20})
	exists, err := bucket.Exists(ctx, "users/ada/avatar.png")
	info, err := bucket.Info(ctx, "users/ada/avatar.png")
	fmt.Printf("v1=%d v2=%d exists=%v size=%d\n", len(listV1), len(listV2.Objects), exists, info.Size)

	// 3. Download with Image Transformation
	transformed, err := bucket.Download(ctx, "users/ada/avatar.png", storage.DownloadOptions{
		Transform: &storage.TransformOptions{
			Width:   256,
			Height:  256,
			Resize:  "cover",
			Quality: 85,
		},
	})
	fmt.Println("Downloaded transformed bytes:", len(transformed))

	// 4. Streaming download for large files
	rc, _, err := bucket.DownloadStream(ctx, "users/ada/avatar.png")
	if err == nil {
		defer rc.Close()
		out, _ := os.Create("downloaded.png")
		defer out.Close()
		_, _ = io.Copy(out, rc)
	}

	// 5. Public & Signed URLs (Download + Direct Upload)
	pubURL := bucket.GetPublicUrl("users/ada/avatar.png", storage.PublicUrlOptions{
		Transform: &storage.TransformOptions{Width: 400, Resize: "contain"},
	})
	signedURL, err := bucket.CreateSignedUrl(ctx, "users/ada/avatar.png", 60)
	signedBatch, err := bucket.CreateSignedUrls(ctx, []string{"users/ada/avatar.png"}, 60)
	signedUpload, err := bucket.CreateSignedUploadUrl(ctx, "users/ada/new.png", true)
	_, err = bucket.UploadToSignedUrl(ctx, "users/ada/new.png", signedUpload.Token, bytes.NewReader([]byte("new")))
	fmt.Println("Public:", pubURL, "Signed:", signedURL, "Batch:", len(signedBatch))

	// 6. Copy, Move, PurgeCache, and Remove
	_, err = bucket.Copy(ctx, "users/ada/avatar.png", "users/ada/avatar-copy.png")
	_, err = bucket.Move(ctx, "users/ada/avatar-copy.png", "archive/avatar.png")
	_, err = bucket.PurgeCache(ctx, "users/ada/avatar.png")
	_, err = bucket.Remove(ctx, []string{"archive/avatar.png", "users/ada/new.png"})
}
```

### Goravel Upload Controller Example

```go
func UploadAvatar(ctx http.Context) http.Response {
	file, err := ctx.Request().File("avatar")
	if err != nil {
		return ctx.Response().Json(400, http.Json{"error": "missing avatar file"})
	}
	f, err := file.ReadCloser()
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	defer f.Close()

	sb := facades.Supabase().WithToken(ctx.Request().Header("Authorization"))
	res, err := sb.Storage().From("avatars").Upload(ctx.Context(), file.GetClientOriginalName(), f, storage.FileOptions{
		ContentType: file.MimeType(),
		Upsert:      true,
	})
	if err != nil {
		return ctx.Response().Json(500, http.Json{"error": err.Error()})
	}
	return ctx.Response().Success().Json(res)
}
```

---

## 3. Analytics (Apache Iceberg REST Catalog)

```go
ctx := context.Background()
analytics := sb.Storage.Analytics()

// Create & list analytics buckets
_, err := analytics.CreateBucket(ctx, "warehouse")
buckets, err := analytics.ListBuckets(ctx)
fmt.Println("Analytics buckets:", len(buckets))

// Open Iceberg REST Catalog client
catalog, err := analytics.From("warehouse")
if err != nil {
	log.Fatal(err)
}

// Namespace operations
ns := []string{"events"}
_, err = catalog.CreateNamespace(ctx, ns, map[string]string{"owner": "data-eng"})
exists, err := catalog.NamespaceExists(ctx, ns)
meta, err := catalog.LoadNamespaceMetadata(ctx, ns)
allNS, err := catalog.ListNamespaces(ctx)
fmt.Printf("Namespace exists=%v, meta=%v, total=%d\n", exists, meta, len(allNS))

// Table operations
_, err = catalog.CreateTable(ctx, ns, map[string]any{
	"name": "page_views",
	"schema": map[string]any{
		"type":      "struct",
		"schema-id": 0,
		"fields": []map[string]any{
			{"id": 1, "name": "user_id", "required": true, "type": "string"},
			{"id": 2, "name": "path", "required": true, "type": "string"},
		},
	},
})
tables, err := catalog.ListTables(ctx, ns)
tableExists, err := catalog.TableExists(ctx, ns, "page_views")
tableMeta, err := catalog.LoadTable(ctx, ns, "page_views")
_, _, _ = tables, tableExists, tableMeta

// Rename, Drop Table, Drop Namespace, Delete Bucket
_ = catalog.RenameTable(ctx,
	storage.IcebergTableIdentifier{Namespace: ns, Name: "page_views"},
	storage.IcebergTableIdentifier{Namespace: ns, Name: "page_views_v2"},
)
_ = catalog.DropTable(ctx, ns, "page_views_v2", true)
_ = catalog.DropNamespace(ctx, ns)
_, _ = analytics.DeleteBucket(ctx, "warehouse")
```

---

## 4. Vector Buckets & Indexes

```go
ctx := context.Background()
vectors := sb.Storage.Vectors()

// 1. Create vector bucket & index
err := vectors.CreateBucket(ctx, "embeddings")
bucketScope := vectors.From("embeddings")

err = bucketScope.CreateIndex(ctx, storage.CreateIndexOptions{
	IndexName:      "docs-idx",
	DataType:       "float32",
	Dimension:      3,
	DistanceMetric: "cosine",
})

idx := bucketScope.Index("docs-idx")

// 2. Put, Get, List, and Query vectors
err = idx.PutVectors(ctx, []storage.VectorItem{
	{
		Key:      "doc-1",
		Data:     map[string]any{"float32": []float32{0.1, 0.2, 0.7}},
		Metadata: map[string]any{"category": "guide"},
	},
	{
		Key:      "doc-2",
		Data:     map[string]any{"float32": []float32{0.8, 0.1, 0.1}},
		Metadata: map[string]any{"category": "api"},
	},
})

items, err := idx.GetVectors(ctx, []string{"doc-1"}, true, true)
listed, err := idx.ListVectors(ctx, storage.ListVectorsOptions{MaxResults: 10, ReturnMetadata: true})
matches, err := idx.QueryVectors(ctx, storage.QueryVectorsOptions{
	QueryVector:    map[string]any{"float32": []float32{0.1, 0.2, 0.7}},
	TopK:           5,
	Filter:         map[string]any{"category": "guide"},
	ReturnDistance: true,
	ReturnMetadata: true,
})
fmt.Printf("Got %d items, listed %d, matches %d\n", len(items), len(listed.Vectors), len(matches.Vectors))

// 3. Cleanup
_ = idx.DeleteVectors(ctx, []string{"doc-1", "doc-2"})
_ = bucketScope.DeleteIndex(ctx, "docs-idx")
_ = vectors.DeleteBucket(ctx, "embeddings")
```
