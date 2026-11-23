package postgrest

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// CountOption specifies the PostgREST count algorithm.
type CountOption string

const (
	CountNone      CountOption = ""
	CountExact     CountOption = "exact"
	CountPlanned   CountOption = "planned"
	CountEstimated CountOption = "estimated"
)

// SelectOptions configures QueryBuilder.Select.
type SelectOptions struct {
	Count CountOption
	Head  bool
}

// InsertOptions configures QueryBuilder.Insert.
type InsertOptions struct {
	Count         CountOption
	DefaultToNull *bool
}

// UpsertOptions configures QueryBuilder.Upsert.
type UpsertOptions struct {
	OnConflict       string
	IgnoreDuplicates bool
	Count            CountOption
	DefaultToNull    *bool
}

// UpdateOptions configures QueryBuilder.Update.
type UpdateOptions struct {
	Count CountOption
}

// DeleteOptions configures QueryBuilder.Delete.
type DeleteOptions struct {
	Count CountOption
}

// RPCOptions configures Client.RPC.
type RPCOptions struct {
	Head  bool
	Get   bool
	Count CountOption
}

// OrderOptions configures FilterBuilder.Order.
type OrderOptions struct {
	Ascending       *bool
	NullsFirst      *bool
	ReferencedTable string
	ForeignTable    string // deprecated alias for ReferencedTable
}

// LimitOptions configures FilterBuilder.Limit.
type LimitOptions struct {
	ReferencedTable string
	ForeignTable    string
}

// RangeOptions configures FilterBuilder.Range.
type RangeOptions struct {
	ReferencedTable string
	ForeignTable    string
}

// ExplainOptions configures FilterBuilder.Explain.
type ExplainOptions struct {
	Analyze  bool
	Verbose  bool
	Settings bool
	Buffers  bool
	WAL      bool
	Format   string // "text" (default) or "json"
}

// TextSearchOptions configures FilterBuilder.TextSearch.
type TextSearchOptions struct {
	Type   string // "plain", "phrase", "websearch", or ""
	Config string
}

// OrOptions configures FilterBuilder.Or.
type OrOptions struct {
	ReferencedTable string
	ForeignTable    string
}

// QueryBuilderOptions configures per-request overrides on Client.From.
type QueryBuilderOptions struct {
	Headers    map[string]string
	HTTPClient *http.Client
	Timeout    time.Duration
}

// Response holds the raw PostgREST HTTP response metadata and body.
type Response struct {
	Data       []byte
	Count      *int64
	Status     int
	StatusText string
	Headers    http.Header
}

// Decode unmarshals the JSON response body into dst.
func (r *Response) Decode(dst any) error {
	if len(r.Data) == 0 || dst == nil {
		return nil
	}
	return json.Unmarshal(r.Data, dst)
}

// OpenApiSpec represents the JSON OpenAPI/Swagger specification returned by PostgREST.
type OpenApiSpec map[string]any

// Execute runs b with ctx and decodes the JSON response into a newly allocated T.
func Execute[T any](ctx context.Context, b *FilterBuilder) (T, *Response, error) {
	var zero T
	resp, err := b.ExecuteRaw(ctx)
	if err != nil {
		return zero, resp, err
	}
	if len(resp.Data) == 0 {
		return zero, resp, nil
	}
	var out T
	if err := json.Unmarshal(resp.Data, &out); err != nil {
		return zero, resp, err
	}
	return out, resp, nil
}
