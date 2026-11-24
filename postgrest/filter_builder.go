package postgrest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var postgrestReservedCharsRe = regexp.MustCompile(`[,()]`)

// FilterBuilder chains PostgREST filters, modifiers, and terminal execution.
type FilterBuilder struct {
	method           string
	url              *url.URL
	headers          http.Header
	schema           string
	body             any
	httpClient       *http.Client
	timeout          time.Duration
	urlLengthLimit   int
	retry            bool
	retryBaseDelay   time.Duration
	isSingle         bool
	isMaybeSingle    bool
	shouldStripNulls bool
	buildErr         error
}

func (fb *FilterBuilder) appendParam(key, val string) {
	q := fb.url.Query()
	q.Add(key, val)
	fb.url.RawQuery = q.Encode()
}

func (fb *FilterBuilder) setParam(key, val string) {
	q := fb.url.Query()
	q.Set(key, val)
	fb.url.RawQuery = q.Encode()
}

// --- All 31 Filter Operators ---

func (fb *FilterBuilder) Eq(column string, value any) *FilterBuilder {
	fb.appendParam(column, fmt.Sprintf("eq.%v", value))
	return fb
}

func (fb *FilterBuilder) Neq(column string, value any) *FilterBuilder {
	fb.appendParam(column, fmt.Sprintf("neq.%v", value))
	return fb
}

func (fb *FilterBuilder) Gt(column string, value any) *FilterBuilder {
	fb.appendParam(column, fmt.Sprintf("gt.%v", value))
	return fb
}

func (fb *FilterBuilder) Gte(column string, value any) *FilterBuilder {
	fb.appendParam(column, fmt.Sprintf("gte.%v", value))
	return fb
}

func (fb *FilterBuilder) Lt(column string, value any) *FilterBuilder {
	fb.appendParam(column, fmt.Sprintf("lt.%v", value))
	return fb
}

func (fb *FilterBuilder) Lte(column string, value any) *FilterBuilder {
	fb.appendParam(column, fmt.Sprintf("lte.%v", value))
	return fb
}

func (fb *FilterBuilder) Like(column, pattern string) *FilterBuilder {
	fb.appendParam(column, "like."+pattern)
	return fb
}

func (fb *FilterBuilder) ILike(column, pattern string) *FilterBuilder {
	fb.appendParam(column, "ilike."+pattern)
	return fb
}

func (fb *FilterBuilder) LikeAllOf(column string, patterns []string) *FilterBuilder {
	fb.appendParam(column, "like(all).{"+strings.Join(patterns, ",")+"}")
	return fb
}

func (fb *FilterBuilder) LikeAnyOf(column string, patterns []string) *FilterBuilder {
	fb.appendParam(column, "like(any).{"+strings.Join(patterns, ",")+"}")
	return fb
}

func (fb *FilterBuilder) ILikeAllOf(column string, patterns []string) *FilterBuilder {
	fb.appendParam(column, "ilike(all).{"+strings.Join(patterns, ",")+"}")
	return fb
}

func (fb *FilterBuilder) ILikeAnyOf(column string, patterns []string) *FilterBuilder {
	fb.appendParam(column, "ilike(any).{"+strings.Join(patterns, ",")+"}")
	return fb
}

func (fb *FilterBuilder) Is(column string, value any) *FilterBuilder {
	if value == nil {
		fb.appendParam(column, "is.null")
	} else {
		fb.appendParam(column, fmt.Sprintf("is.%v", value))
	}
	return fb
}

func (fb *FilterBuilder) IsDistinct(column string, value any) *FilterBuilder {
	if value == nil {
		fb.appendParam(column, "isdistinct.null")
	} else {
		fb.appendParam(column, fmt.Sprintf("isdistinct.%v", value))
	}
	return fb
}

func formatInValues(values []any) string {
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, v := range values {
		s := fmt.Sprint(v)
		if postgrestReservedCharsRe.MatchString(s) {
			s = `"` + s + `"`
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		cleaned = append(cleaned, s)
	}
	return "(" + strings.Join(cleaned, ",") + ")"
}

func (fb *FilterBuilder) In(column string, values []any) *FilterBuilder {
	fb.appendParam(column, "in."+formatInValues(values))
	return fb
}

func (fb *FilterBuilder) NotIn(column string, values []any) *FilterBuilder {
	fb.appendParam(column, "not.in."+formatInValues(values))
	return fb
}

func formatContainsValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []string:
		return "{" + strings.Join(v, ",") + "}"
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = fmt.Sprint(item)
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		if value != nil {
			rv := reflect.ValueOf(value)
			if (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Type().Elem().Kind() != reflect.Uint8 {
				parts := make([]string, rv.Len())
				for i := 0; i < rv.Len(); i++ {
					parts[i] = fmt.Sprint(rv.Index(i).Interface())
				}
				return "{" + strings.Join(parts, ",") + "}"
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

func (fb *FilterBuilder) Contains(column string, value any) *FilterBuilder {
	fb.appendParam(column, "cs."+formatContainsValue(value))
	return fb
}

func (fb *FilterBuilder) ContainedBy(column string, value any) *FilterBuilder {
	fb.appendParam(column, "cd."+formatContainsValue(value))
	return fb
}

func (fb *FilterBuilder) RangeGt(column, r string) *FilterBuilder {
	fb.appendParam(column, "sr."+r)
	return fb
}

func (fb *FilterBuilder) RangeGte(column, r string) *FilterBuilder {
	fb.appendParam(column, "nxl."+r)
	return fb
}

func (fb *FilterBuilder) RangeLt(column, r string) *FilterBuilder {
	fb.appendParam(column, "sl."+r)
	return fb
}

func (fb *FilterBuilder) RangeLte(column, r string) *FilterBuilder {
	fb.appendParam(column, "nxr."+r)
	return fb
}

func (fb *FilterBuilder) RangeAdjacent(column, r string) *FilterBuilder {
	fb.appendParam(column, "adj."+r)
	return fb
}

func (fb *FilterBuilder) Overlaps(column string, value any) *FilterBuilder {
	fb.appendParam(column, "ov."+formatContainsValue(value))
	return fb
}

func (fb *FilterBuilder) TextSearch(column, query string, opts ...TextSearchOptions) *FilterBuilder {
	var opt TextSearchOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	typePart := ""
	switch opt.Type {
	case "plain":
		typePart = "pl"
	case "phrase":
		typePart = "ph"
	case "websearch":
		typePart = "w"
	}
	configPart := ""
	if opt.Config != "" {
		configPart = "(" + opt.Config + ")"
	}
	fb.appendParam(column, fmt.Sprintf("%sfts%s.%s", typePart, configPart, query))
	return fb
}

func (fb *FilterBuilder) Match(query map[string]any) *FilterBuilder {
	for k, v := range query {
		if v == nil {
			continue
		}
		fb.appendParam(k, fmt.Sprintf("eq.%v", v))
	}
	return fb
}

func (fb *FilterBuilder) Not(column, operator string, value any) *FilterBuilder {
	if value == nil {
		fb.appendParam(column, fmt.Sprintf("not.%s.null", operator))
		return fb
	}
	fb.appendParam(column, fmt.Sprintf("not.%s.%v", operator, value))
	return fb
}

func (fb *FilterBuilder) Or(filters string, opts ...OrOptions) *FilterBuilder {
	key := "or"
	if len(opts) > 0 {
		ref := opts[0].ReferencedTable
		if ref == "" {
			ref = opts[0].ForeignTable
		}
		if ref != "" {
			key = ref + ".or"
		}
	}
	fb.appendParam(key, "("+filters+")")
	return fb
}

func (fb *FilterBuilder) Filter(column, operator string, value any) *FilterBuilder {
	if value == nil {
		fb.appendParam(column, fmt.Sprintf("%s.null", operator))
		return fb
	}
	fb.appendParam(column, fmt.Sprintf("%s.%v", operator, value))
	return fb
}

func (fb *FilterBuilder) RegexMatch(column, pattern string) *FilterBuilder {
	fb.appendParam(column, "match."+pattern)
	return fb
}

func (fb *FilterBuilder) RegexIMatch(column, pattern string) *FilterBuilder {
	fb.appendParam(column, "imatch."+pattern)
	return fb
}

// --- All 13 Modifiers ---

// Select chains a SELECT returning clause onto a mutation (Insert/Upsert/Update/Delete/RPC).
func (fb *FilterBuilder) Select(columns ...string) *FilterBuilder {
	cols := "*"
	if len(columns) > 0 && columns[0] != "" {
		cols = columns[0]
	}
	fb.setParam("select", cleanColumns(cols))
	fb.headers.Add("Prefer", "return=representation")
	return fb
}

func (fb *FilterBuilder) Order(column string, opts ...OrderOptions) *FilterBuilder {
	asc := true
	var nullsFirst *bool
	ref := ""
	if len(opts) > 0 {
		if opts[0].Ascending != nil {
			asc = *opts[0].Ascending
		}
		nullsFirst = opts[0].NullsFirst
		ref = opts[0].ReferencedTable
		if ref == "" {
			ref = opts[0].ForeignTable
		}
	}

	key := "order"
	if ref != "" {
		key = ref + ".order"
	}

	dir := "asc"
	if !asc {
		dir = "desc"
	}
	clause := column + "." + dir
	if nullsFirst != nil {
		if *nullsFirst {
			clause += ".nullsfirst"
		} else {
			clause += ".nullslast"
		}
	}

	q := fb.url.Query()
	if existing := q.Get(key); existing != "" {
		q.Set(key, existing+","+clause)
	} else {
		q.Set(key, clause)
	}
	fb.url.RawQuery = q.Encode()
	return fb
}

func (fb *FilterBuilder) Limit(count int, opts ...LimitOptions) *FilterBuilder {
	key := "limit"
	if len(opts) > 0 {
		ref := opts[0].ReferencedTable
		if ref == "" {
			ref = opts[0].ForeignTable
		}
		if ref != "" {
			key = ref + ".limit"
		}
	}
	fb.setParam(key, strconv.Itoa(count))
	return fb
}

func (fb *FilterBuilder) Range(from, to int, opts ...RangeOptions) *FilterBuilder {
	ref := ""
	if len(opts) > 0 {
		ref = opts[0].ReferencedTable
		if ref == "" {
			ref = opts[0].ForeignTable
		}
	}
	offsetKey := "offset"
	limitKey := "limit"
	if ref != "" {
		offsetKey = ref + ".offset"
		limitKey = ref + ".limit"
	}
	fb.setParam(offsetKey, strconv.Itoa(from))
	fb.setParam(limitKey, strconv.Itoa(to-from+1))
	return fb
}

func (fb *FilterBuilder) Single() *FilterBuilder {
	fb.headers.Set("Accept", "application/vnd.pgrst.object+json")
	fb.isSingle = true
	return fb
}

func (fb *FilterBuilder) MaybeSingle() *FilterBuilder {
	fb.headers.Set("Accept", "application/json")
	fb.isMaybeSingle = true
	return fb
}

func (fb *FilterBuilder) CSV() *FilterBuilder {
	fb.headers.Set("Accept", "text/csv")
	return fb
}

func (fb *FilterBuilder) GeoJSON() *FilterBuilder {
	fb.headers.Set("Accept", "application/geo+json")
	return fb
}

func (fb *FilterBuilder) Explain(opts ...ExplainOptions) *FilterBuilder {
	var opt ExplainOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	format := opt.Format
	if format == "" {
		format = "text"
	}
	var parts []string
	if opt.Analyze {
		parts = append(parts, "analyze")
	}
	if opt.Verbose {
		parts = append(parts, "verbose")
	}
	if opt.Settings {
		parts = append(parts, "settings")
	}
	if opt.Buffers {
		parts = append(parts, "buffers")
	}
	if opt.WAL {
		parts = append(parts, "wal")
	}
	forMediaType := fb.headers.Get("Accept")
	if forMediaType == "" {
		forMediaType = "application/json"
	}
	fb.headers.Set("Accept", fmt.Sprintf(`application/vnd.pgrst.plan+%s; for="%s"; options=%s;`, format, forMediaType, strings.Join(parts, "|")))
	return fb
}

func (fb *FilterBuilder) Rollback() *FilterBuilder {
	fb.headers.Add("Prefer", "tx=rollback")
	return fb
}

func (fb *FilterBuilder) MaxAffected(rows int) *FilterBuilder {
	fb.headers.Add("Prefer", "handling=strict")
	fb.headers.Add("Prefer", fmt.Sprintf("max-affected=%d", rows))
	return fb
}

func (fb *FilterBuilder) StripNulls() *FilterBuilder {
	if fb.headers.Get("Accept") == "text/csv" {
		fb.buildErr = &PostgrestError{Message: "stripNulls() cannot be used with csv()"}
		return fb
	}
	fb.shouldStripNulls = true
	return fb
}

// SetHeader sets a per-request HTTP header override.
func (fb *FilterBuilder) SetHeader(name, value string) *FilterBuilder {
	fb.headers.Set(name, value)
	return fb
}

// Retry enables or disables idempotent auto-retry for this request.
func (fb *FilterBuilder) Retry(enabled bool) *FilterBuilder {
	fb.retry = enabled
	return fb
}

// Timeout overrides the timeout for this single request.
func (fb *FilterBuilder) Timeout(d time.Duration) *FilterBuilder {
	fb.timeout = d
	return fb
}

// URL returns the built request URL string (useful for debugging and testing).
func (fb *FilterBuilder) URL() string {
	return fb.url.String()
}

// Execute runs the request and decodes the JSON response body into dst (if non-nil).
func (fb *FilterBuilder) Execute(ctx context.Context, dst any) (*Response, error) {
	resp, err := fb.ExecuteRaw(ctx)
	if err != nil {
		return resp, err
	}
	if dst != nil && len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, dst); err != nil {
			return resp, &PostgrestError{Message: err.Error(), Status: resp.Status, StatusText: resp.StatusText}
		}
	}
	return resp, nil
}

// ExecuteRaw runs the HTTP request and returns the raw Response metadata and bytes.
func (fb *FilterBuilder) ExecuteRaw(ctx context.Context) (*Response, error) {
	if fb.buildErr != nil {
		return nil, fb.buildErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if fb.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fb.timeout)
		defer cancel()
	}

	rawURL := fb.url.String()
	urlTooLong := fb.urlLengthLimit > 0 && len(rawURL) > fb.urlLengthLimit

	headers := fb.headers.Clone()
	if fb.schema != "" {
		if fb.method == http.MethodGet || fb.method == http.MethodHead {
			headers.Set("Accept-Profile", fb.schema)
		} else {
			headers.Set("Content-Profile", fb.schema)
		}
	}
	if fb.method != http.MethodGet && fb.method != http.MethodHead {
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "application/json")
		}
	}

	if fb.shouldStripNulls {
		curAccept := headers.Get("Accept")
		if curAccept == "application/vnd.pgrst.object+json" {
			headers.Set("Accept", "application/vnd.pgrst.object+json;nulls=stripped")
		} else if curAccept == "" || curAccept == "application/json" {
			headers.Set("Accept", "application/vnd.pgrst.array+json;nulls=stripped")
		}
	}

	var bodyBytes []byte
	if fb.body != nil {
		var err error
		bodyBytes, err = json.Marshal(fb.body)
		if err != nil {
			return nil, &PostgrestError{Message: err.Error()}
		}
	}

	resp, err := doWithRetry(ctx, fb.httpClient, fb.method, rawURL, headers, bodyBytes, fb.retry, fb.retryBaseDelay)
	if err != nil {
		pgErr := &PostgrestError{Message: err.Error()}
		if urlTooLong {
			pgErr.Hint = fmt.Sprintf("Request URL length (%d) exceeds configured limit (%d); consider using views or an RPC function for large filter lists.", len(rawURL), fb.urlLengthLimit)
		}
		return nil, pgErr
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &PostgrestError{Message: err.Error(), Status: resp.StatusCode, StatusText: resp.Status}
	}

	count := parseContentRangeCount(resp.Header.Get("Content-Range"))
	pgResp := &Response{
		Data:       raw,
		Count:      count,
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    resp.Header.Clone(),
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// PostgREST #295 workaround: normalize 404 with empty array `[]` or empty body.
		if resp.StatusCode == http.StatusNotFound {
			trimmed := strings.TrimSpace(string(raw))
			if trimmed == "[]" {
				pgResp.Status = http.StatusOK
				pgResp.StatusText = http.StatusText(http.StatusOK)
				return pgResp, nil
			}
			if trimmed == "" {
				pgResp.Status = http.StatusNoContent
				pgResp.StatusText = http.StatusText(http.StatusNoContent)
				pgResp.Data = nil
				return pgResp, nil
			}
		}
		pgErr := decodePostgrestError(raw, resp.StatusCode, resp.Status)
		if urlTooLong && pgErr.Hint == "" {
			pgErr.Hint = fmt.Sprintf("Request URL length (%d) exceeds configured limit (%d); consider using views or an RPC function for large filter lists.", len(rawURL), fb.urlLengthLimit)
		}
		// MaybeSingle treats PostgREST's 0-row PGRST116 (406) as empty success.
		if fb.isMaybeSingle && (strings.Contains(pgErr.Details, "Results contain 0 rows") || strings.Contains(pgErr.Details, "The result contains 0 rows")) {
			pgResp.Data = nil
			return pgResp, nil
		}
		return pgResp, pgErr
	}

	if fb.isMaybeSingle {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err == nil {
			if len(arr) == 0 {
				pgResp.Data = nil
				return pgResp, nil
			}
			if len(arr) > 1 {
				return pgResp, &PostgrestError{
					Code:       "PGRST116",
					Message:    "JSON object requested, multiple (or no) rows returned",
					Details:    fmt.Sprintf("The result contains %d rows", len(arr)),
					Status:     http.StatusNotAcceptable,
					StatusText: http.StatusText(http.StatusNotAcceptable),
				}
			}
			pgResp.Data = arr[0]
		}
	}

	return pgResp, nil
}

func parseContentRangeCount(header string) *int64 {
	if header == "" {
		return nil
	}
	parts := strings.Split(header, "/")
	if len(parts) != 2 || parts[1] == "*" {
		return nil
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
