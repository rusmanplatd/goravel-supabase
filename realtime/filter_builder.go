package realtime

import (
	"fmt"
	"reflect"
	"strings"
)

// RealtimePostgresFilterBuilder builds PostgREST-compatible filter strings for postgres_changes subscriptions.
type RealtimePostgresFilterBuilder struct {
	filters []string
}

// NewPostgresFilterBuilder creates a new RealtimePostgresFilterBuilder.
func NewPostgresFilterBuilder() *RealtimePostgresFilterBuilder {
	return &RealtimePostgresFilterBuilder{}
}

func (b *RealtimePostgresFilterBuilder) add(expr string) *RealtimePostgresFilterBuilder {
	b.filters = append(b.filters, expr)
	return b
}

func formatFilterValue(v any) string {
	if v == nil {
		return "null"
	}
	s := fmt.Sprint(v)
	needsQuote := strings.ContainsAny(s, `,()"\`) || (len(s) > 0 && (s[0] == ' ' || s[len(s)-1] == ' ' || s[0] == '\t' || s[len(s)-1] == '\t'))
	if needsQuote {
		escaped := strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return s
}

func formatInValues(values []any) string {
	seen := make(map[string]struct{}, len(values))
	parts := make([]string, 0, len(values))
	for _, v := range values {
		formatted := formatFilterValue(v)
		if _, dup := seen[formatted]; dup {
			continue
		}
		seen[formatted] = struct{}{}
		parts = append(parts, formatted)
	}
	return "(" + strings.Join(parts, ",") + ")"
}

func (b *RealtimePostgresFilterBuilder) Eq(column string, value any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=eq.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) Neq(column string, value any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=neq.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) Gt(column string, value any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=gt.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) Gte(column string, value any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=gte.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) Lt(column string, value any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=lt.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) Lte(column string, value any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=lte.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) In(column string, values []any) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=in.%s", column, formatInValues(values)))
}

func (b *RealtimePostgresFilterBuilder) Like(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=like.%s", column, formatFilterValue(pattern)))
}

func (b *RealtimePostgresFilterBuilder) ILike(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=ilike.%s", column, formatFilterValue(pattern)))
}

func (b *RealtimePostgresFilterBuilder) Match(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=match.%s", column, formatFilterValue(pattern)))
}

func (b *RealtimePostgresFilterBuilder) IMatch(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.add(fmt.Sprintf("%s=imatch.%s", column, formatFilterValue(pattern)))
}

func (b *RealtimePostgresFilterBuilder) Is(column string, value any) *RealtimePostgresFilterBuilder {
	if value == nil {
		return b.add(fmt.Sprintf("%s=is.null", column))
	}
	return b.add(fmt.Sprintf("%s=is.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) IsDistinct(column string, value any) *RealtimePostgresFilterBuilder {
	if value == nil {
		return b.add(fmt.Sprintf("%s=isdistinct.null", column))
	}
	return b.add(fmt.Sprintf("%s=isdistinct.%s", column, formatFilterValue(value)))
}

func (b *RealtimePostgresFilterBuilder) Not(column, operator string, value any) *RealtimePostgresFilterBuilder {
	if value == nil {
		return b.add(fmt.Sprintf("%s=not.%s.null", column, operator))
	}
	if strings.EqualFold(operator, "in") {
		rv := reflect.ValueOf(value)
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			vals := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				vals[i] = rv.Index(i).Interface()
			}
			return b.add(fmt.Sprintf("%s=not.in.%s", column, formatInValues(vals)))
		}
	}
	return b.add(fmt.Sprintf("%s=not.%s.%s", column, operator, formatFilterValue(value)))
}

// Build returns the serialized filter string.
func (b *RealtimePostgresFilterBuilder) Build() string {
	return strings.Join(b.filters, ",")
}

// String implements fmt.Stringer.
func (b *RealtimePostgresFilterBuilder) String() string {
	return b.Build()
}
