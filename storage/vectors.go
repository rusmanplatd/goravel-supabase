package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// VectorsClient manages Supabase Vector Buckets, Indexes, and Vector Data under `/storage/v1/vector`.
type VectorsClient struct {
	url        string
	headers    map[string]string
	httpClient *http.Client
}

type VectorBucket struct {
	VectorBucketName string `json:"vectorBucketName"`
	CreationTime     int64  `json:"creationTime,omitempty"`
}

type VectorIndex struct {
	VectorBucketName      string `json:"vectorBucketName"`
	IndexName             string `json:"indexName"`
	DataType              string `json:"dataType"`
	Dimension             int    `json:"dimension"`
	DistanceMetric        string `json:"distanceMetric"`
	MetadataConfiguration any    `json:"metadataConfiguration,omitempty"`
}

type VectorItem struct {
	Key      string         `json:"key"`
	Data     map[string]any `json:"data"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ListVectorsOptions struct {
	MaxResults     int    `json:"maxResults,omitempty"`
	NextToken      string `json:"nextToken,omitempty"`
	ReturnData     bool   `json:"returnData,omitempty"`
	ReturnMetadata bool   `json:"returnMetadata,omitempty"`
	SegmentCount   int    `json:"segmentCount,omitempty"`
	SegmentIndex   int    `json:"segmentIndex,omitempty"`
}

type ListVectorsResponse struct {
	Vectors   []VectorItem `json:"vectors"`
	NextToken string       `json:"nextToken,omitempty"`
}

type QueryVectorsOptions struct {
	QueryVector    map[string]any `json:"queryVector"`
	TopK           int            `json:"topK"`
	Filter         map[string]any `json:"filter,omitempty"`
	ReturnDistance bool           `json:"returnDistance,omitempty"`
	ReturnMetadata bool           `json:"returnMetadata,omitempty"`
	NextToken      string         `json:"nextToken,omitempty"`
}

type QueryVectorsResponse struct {
	Vectors   []map[string]any `json:"vectors"`
	NextToken string           `json:"nextToken,omitempty"`
}

func (v *VectorsClient) post(ctx context.Context, action string, body any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url+"/"+action, bytes.NewReader(b))
	if err != nil {
		return err
	}
	for k, val := range v.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, val)
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.httpClient.Do(req)
	if err != nil {
		return &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &StorageError{Name: "StorageUnknownError", Message: err.Error(), Err: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeVectorsApiError(raw, resp.StatusCode, resp.Status)
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// --- Vector Bucket Operations ---

func (v *VectorsClient) CreateBucket(ctx context.Context, bucketName string) error {
	return v.post(ctx, "CreateVectorBucket", map[string]any{"vectorBucketName": bucketName}, nil)
}

func (v *VectorsClient) GetBucket(ctx context.Context, bucketName string) (*VectorBucket, error) {
	var resp struct {
		VectorBucket VectorBucket `json:"vectorBucket"`
	}
	if err := v.post(ctx, "GetVectorBucket", map[string]any{"vectorBucketName": bucketName}, &resp); err != nil {
		return nil, err
	}
	return &resp.VectorBucket, nil
}

func (v *VectorsClient) ListBuckets(ctx context.Context, prefix string, maxResults int, nextToken string) ([]VectorBucket, string, error) {
	body := map[string]any{}
	if prefix != "" {
		body["prefix"] = prefix
	}
	if maxResults > 0 {
		body["maxResults"] = maxResults
	}
	if nextToken != "" {
		body["nextToken"] = nextToken
	}
	var resp struct {
		VectorBuckets []VectorBucket `json:"vectorBuckets"`
		NextToken     string         `json:"nextToken"`
	}
	if err := v.post(ctx, "ListVectorBuckets", body, &resp); err != nil {
		return nil, "", err
	}
	return resp.VectorBuckets, resp.NextToken, nil
}

func (v *VectorsClient) DeleteBucket(ctx context.Context, bucketName string) error {
	return v.post(ctx, "DeleteVectorBucket", map[string]any{"vectorBucketName": bucketName}, nil)
}

// From returns a VectorBucketScope bound to bucketName.
func (v *VectorsClient) From(bucketName string) *VectorBucketScope {
	return &VectorBucketScope{client: v, bucketName: bucketName}
}

// --- Vector Bucket Scope (Index Operations) ---

type VectorBucketScope struct {
	client     *VectorsClient
	bucketName string
}

type CreateIndexOptions struct {
	IndexName             string `json:"indexName"`
	DataType              string `json:"dataType"`
	Dimension             int    `json:"dimension"`
	DistanceMetric        string `json:"distanceMetric"`
	MetadataConfiguration any    `json:"metadataConfiguration,omitempty"`
}

func (s *VectorBucketScope) CreateIndex(ctx context.Context, opts CreateIndexOptions) error {
	body := map[string]any{
		"vectorBucketName": s.bucketName,
		"indexName":        opts.IndexName,
		"dataType":         opts.DataType,
		"dimension":        opts.Dimension,
		"distanceMetric":   opts.DistanceMetric,
	}
	if opts.MetadataConfiguration != nil {
		body["metadataConfiguration"] = opts.MetadataConfiguration
	}
	return s.client.post(ctx, "CreateIndex", body, nil)
}

func (s *VectorBucketScope) GetIndex(ctx context.Context, indexName string) (*VectorIndex, error) {
	var resp struct {
		Index VectorIndex `json:"index"`
	}
	if err := s.client.post(ctx, "GetIndex", map[string]any{
		"vectorBucketName": s.bucketName,
		"indexName":        indexName,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Index, nil
}

func (s *VectorBucketScope) ListIndexes(ctx context.Context, prefix string, maxResults int, nextToken string) ([]VectorIndex, string, error) {
	body := map[string]any{"vectorBucketName": s.bucketName}
	if prefix != "" {
		body["prefix"] = prefix
	}
	if maxResults > 0 {
		body["maxResults"] = maxResults
	}
	if nextToken != "" {
		body["nextToken"] = nextToken
	}
	var resp struct {
		Indexes   []VectorIndex `json:"indexes"`
		NextToken string        `json:"nextToken"`
	}
	if err := s.client.post(ctx, "ListIndexes", body, &resp); err != nil {
		return nil, "", err
	}
	return resp.Indexes, resp.NextToken, nil
}

func (s *VectorBucketScope) DeleteIndex(ctx context.Context, indexName string) error {
	return s.client.post(ctx, "DeleteIndex", map[string]any{
		"vectorBucketName": s.bucketName,
		"indexName":        indexName,
	}, nil)
}

// Index returns a VectorIndexScope bound to indexName.
func (s *VectorBucketScope) Index(indexName string) *VectorIndexScope {
	return &VectorIndexScope{
		client:     s.client,
		bucketName: s.bucketName,
		indexName:  indexName,
	}
}

// --- Vector Index Scope (Vector Data Operations) ---

type VectorIndexScope struct {
	client     *VectorsClient
	bucketName string
	indexName  string
}

func (idx *VectorIndexScope) PutVectors(ctx context.Context, vectors []VectorItem) error {
	if len(vectors) < 1 || len(vectors) > 500 {
		return &StorageError{Name: "StorageError", Message: fmt.Sprintf("PutVectors requires 1-500 vectors, got %d", len(vectors))}
	}
	return idx.client.post(ctx, "PutVectors", map[string]any{
		"vectorBucketName": idx.bucketName,
		"indexName":        idx.indexName,
		"vectors":          vectors,
	}, nil)
}

func (idx *VectorIndexScope) GetVectors(ctx context.Context, keys []string, returnData, returnMetadata bool) ([]VectorItem, error) {
	var resp struct {
		Vectors []VectorItem `json:"vectors"`
	}
	if err := idx.client.post(ctx, "GetVectors", map[string]any{
		"vectorBucketName": idx.bucketName,
		"indexName":        idx.indexName,
		"keys":             keys,
		"returnData":       returnData,
		"returnMetadata":   returnMetadata,
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Vectors, nil
}

func (idx *VectorIndexScope) ListVectors(ctx context.Context, opts ...ListVectorsOptions) (*ListVectorsResponse, error) {
	var opt ListVectorsOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.SegmentCount != 0 {
		if opt.SegmentCount < 1 || opt.SegmentCount > 16 {
			return nil, &StorageError{Name: "StorageError", Message: "segmentCount must be between 1 and 16"}
		}
		if opt.SegmentIndex < 0 || opt.SegmentIndex >= opt.SegmentCount {
			return nil, &StorageError{Name: "StorageError", Message: "segmentIndex must be in [0, segmentCount)"}
		}
	}
	body := map[string]any{
		"vectorBucketName": idx.bucketName,
		"indexName":        idx.indexName,
	}
	if opt.MaxResults > 0 {
		body["maxResults"] = opt.MaxResults
	}
	if opt.NextToken != "" {
		body["nextToken"] = opt.NextToken
	}
	if opt.ReturnData {
		body["returnData"] = true
	}
	if opt.ReturnMetadata {
		body["returnMetadata"] = true
	}
	if opt.SegmentCount > 0 {
		body["segmentCount"] = opt.SegmentCount
		body["segmentIndex"] = opt.SegmentIndex
	}
	var resp ListVectorsResponse
	if err := idx.client.post(ctx, "ListVectors", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (idx *VectorIndexScope) QueryVectors(ctx context.Context, opts QueryVectorsOptions) (*QueryVectorsResponse, error) {
	body := map[string]any{
		"vectorBucketName": idx.bucketName,
		"indexName":        idx.indexName,
		"queryVector":      opts.QueryVector,
		"topK":             opts.TopK,
	}
	if opts.Filter != nil {
		body["filter"] = opts.Filter
	}
	if opts.ReturnDistance {
		body["returnDistance"] = true
	}
	if opts.ReturnMetadata {
		body["returnMetadata"] = true
	}
	if opts.NextToken != "" {
		body["nextToken"] = opts.NextToken
	}
	var resp QueryVectorsResponse
	if err := idx.client.post(ctx, "QueryVectors", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (idx *VectorIndexScope) DeleteVectors(ctx context.Context, keys []string) error {
	if len(keys) < 1 || len(keys) > 500 {
		return &StorageError{Name: "StorageError", Message: fmt.Sprintf("DeleteVectors requires 1-500 keys, got %d", len(keys))}
	}
	return idx.client.post(ctx, "DeleteVectors", map[string]any{
		"vectorBucketName": idx.bucketName,
		"indexName":        idx.indexName,
		"keys":             keys,
	}, nil)
}
