package postgrest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// QueryBuilder builds SELECT, INSERT, UPSERT, UPDATE, and DELETE queries for a relation.
type QueryBuilder struct {
	url            *url.URL
	headers        http.Header
	schema         string
	httpClient     *http.Client
	timeout        time.Duration
	urlLengthLimit int
	retry          bool
	retryBaseDelay time.Duration
}

func (qb *QueryBuilder) cloneURL() *url.URL {
	u := *qb.url
	return &u
}

func cleanColumns(columns string) string {
	if columns == "" {
		return "*"
	}
	var b strings.Builder
	quoted := false
	for _, r := range columns {
		if !quoted && (r == ' ' || r == '\t' || r == '\n' || r == '\r') {
			continue
		}
		if r == '"' {
			quoted = !quoted
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "*"
	}
	return b.String()
}

// Select starts a SELECT query on the relation.
func (qb *QueryBuilder) Select(columns string, opts ...SelectOptions) *FilterBuilder {
	var opt SelectOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	u := qb.cloneURL()
	q := u.Query()
	q.Set("select", cleanColumns(columns))
	u.RawQuery = q.Encode()

	headers := qb.headers.Clone()
	method := http.MethodGet
	if opt.Head {
		method = http.MethodHead
	}
	if opt.Count != CountNone {
		headers.Add("Prefer", "count="+string(opt.Count))
	}

	return &FilterBuilder{
		method:         method,
		url:            u,
		headers:        headers,
		schema:         qb.schema,
		httpClient:     qb.httpClient,
		timeout:        qb.timeout,
		urlLengthLimit: qb.urlLengthLimit,
		retry:          qb.retry,
		retryBaseDelay: qb.retryBaseDelay,
	}
}

// Insert starts an INSERT mutation on the relation.
func (qb *QueryBuilder) Insert(values any, opts ...InsertOptions) *FilterBuilder {
	var opt InsertOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	u := qb.cloneURL()
	headers := qb.headers.Clone()

	if opt.Count != CountNone {
		headers.Add("Prefer", "count="+string(opt.Count))
	}
	if opt.DefaultToNull != nil && !*opt.DefaultToNull {
		headers.Add("Prefer", "missing=default")
	}

	if cols := computeBulkColumns(values); cols != "" {
		q := u.Query()
		q.Set("columns", cols)
		u.RawQuery = q.Encode()
	}

	return &FilterBuilder{
		method:         http.MethodPost,
		url:            u,
		headers:        headers,
		schema:         qb.schema,
		body:           values,
		httpClient:     qb.httpClient,
		timeout:        qb.timeout,
		urlLengthLimit: qb.urlLengthLimit,
		retry:          qb.retry,
		retryBaseDelay: qb.retryBaseDelay,
	}
}

// Upsert starts an UPSERT mutation on the relation.
func (qb *QueryBuilder) Upsert(values any, opts ...UpsertOptions) *FilterBuilder {
	var opt UpsertOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	u := qb.cloneURL()
	q := u.Query()
	headers := qb.headers.Clone()

	if opt.IgnoreDuplicates {
		headers.Add("Prefer", "resolution=ignore-duplicates")
	} else {
		headers.Add("Prefer", "resolution=merge-duplicates")
	}
	if opt.OnConflict != "" {
		q.Set("on_conflict", opt.OnConflict)
	}
	if opt.Count != CountNone {
		headers.Add("Prefer", "count="+string(opt.Count))
	}
	if opt.DefaultToNull != nil && !*opt.DefaultToNull {
		headers.Add("Prefer", "missing=default")
	}
	if cols := computeBulkColumns(values); cols != "" {
		q.Set("columns", cols)
	}
	u.RawQuery = q.Encode()

	return &FilterBuilder{
		method:         http.MethodPost,
		url:            u,
		headers:        headers,
		schema:         qb.schema,
		body:           values,
		httpClient:     qb.httpClient,
		timeout:        qb.timeout,
		urlLengthLimit: qb.urlLengthLimit,
		retry:          qb.retry,
		retryBaseDelay: qb.retryBaseDelay,
	}
}

// Update starts an UPDATE mutation on the relation.
func (qb *QueryBuilder) Update(values any, opts ...UpdateOptions) *FilterBuilder {
	var opt UpdateOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	u := qb.cloneURL()
	headers := qb.headers.Clone()
	if opt.Count != CountNone {
		headers.Add("Prefer", "count="+string(opt.Count))
	}

	return &FilterBuilder{
		method:         http.MethodPatch,
		url:            u,
		headers:        headers,
		schema:         qb.schema,
		body:           values,
		httpClient:     qb.httpClient,
		timeout:        qb.timeout,
		urlLengthLimit: qb.urlLengthLimit,
		retry:          qb.retry,
		retryBaseDelay: qb.retryBaseDelay,
	}
}

// Delete starts a DELETE mutation on the relation.
func (qb *QueryBuilder) Delete(opts ...DeleteOptions) *FilterBuilder {
	var opt DeleteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	u := qb.cloneURL()
	headers := qb.headers.Clone()
	if opt.Count != CountNone {
		headers.Add("Prefer", "count="+string(opt.Count))
	}

	return &FilterBuilder{
		method:         http.MethodDelete,
		url:            u,
		headers:        headers,
		schema:         qb.schema,
		httpClient:     qb.httpClient,
		timeout:        qb.timeout,
		urlLengthLimit: qb.urlLengthLimit,
		retry:          qb.retry,
		retryBaseDelay: qb.retryBaseDelay,
	}
}

// computeBulkColumns detects slice/array payloads of objects and computes the
// deduplicated, sorted union of keys formatted as `"col1","col2"` for PostgREST's
// `columns` query parameter.
func computeBulkColumns(values any) string {
	if values == nil {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) == 0 || raw[0] != '[' {
		return ""
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
		return ""
	}
	keySet := make(map[string]struct{})
	for _, row := range rows {
		for k := range row {
			keySet[k] = struct{}{}
		}
	}
	if len(keySet) == 0 {
		return ""
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = `"` + strings.ReplaceAll(k, `"`, `\"`) + `"`
	}
	return strings.Join(quoted, ",")
}
