package realtime

import (
	"fmt"
	"strings"
)

// RealtimePostgresFilterBuilder builds PostgREST-compatible filter strings for postgres_changes subscriptions.
type RealtimePostgresFilterBuilder struct {
	filter string
}

// NewPostgresFilterBuilder creates a new RealtimePostgresFilterBuilder.
func NewPostgresFilterBuilder() *RealtimePostgresFilterBuilder {
	return &RealtimePostgresFilterBuilder{}
}

func (b *RealtimePostgresFilterBuilder) set(expr string) *RealtimePostgresFilterBuilder {
	b.filter = expr
	return b
}

func (b *RealtimePostgresFilterBuilder) Eq(column string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=eq.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) Neq(column string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=neq.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) Gt(column string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=gt.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) Gte(column string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=gte.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) Lt(column string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=lt.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) Lte(column string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=lte.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) In(column string, values []any) *RealtimePostgresFilterBuilder {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	return b.set(fmt.Sprintf("%s=in.(%s)", column, strings.Join(parts, ",")))
}

func (b *RealtimePostgresFilterBuilder) Like(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=like.%s", column, pattern))
}

func (b *RealtimePostgresFilterBuilder) ILike(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=ilike.%s", column, pattern))
}

func (b *RealtimePostgresFilterBuilder) Match(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=match.%s", column, pattern))
}

func (b *RealtimePostgresFilterBuilder) IMatch(column, pattern string) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=imatch.%s", column, pattern))
}

func (b *RealtimePostgresFilterBuilder) Is(column string, value any) *RealtimePostgresFilterBuilder {
	if value == nil {
		return b.set(fmt.Sprintf("%s=is.null", column))
	}
	return b.set(fmt.Sprintf("%s=is.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) IsDistinct(column string, value any) *RealtimePostgresFilterBuilder {
	if value == nil {
		return b.set(fmt.Sprintf("%s=isdistinct.null", column))
	}
	return b.set(fmt.Sprintf("%s=isdistinct.%v", column, value))
}

func (b *RealtimePostgresFilterBuilder) Not(column, operator string, value any) *RealtimePostgresFilterBuilder {
	return b.set(fmt.Sprintf("%s=not.%s.%v", column, operator, value))
}

// Build returns the serialized filter string.
func (b *RealtimePostgresFilterBuilder) Build() string {
	return b.filter
}

// String implements fmt.Stringer.
func (b *RealtimePostgresFilterBuilder) String() string {
	return b.filter
}
