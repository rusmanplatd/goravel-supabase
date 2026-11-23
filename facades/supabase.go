package facades

import (
	"fmt"

	supabase "github.com/rusmanplatd/goravel-supabase"
	"github.com/rusmanplatd/goravel-supabase/contracts"
)

// Supabase resolves the default (or named) Supabase client from the Goravel container.
func Supabase(connection ...string) contracts.Supabase {
	if supabase.App == nil {
		panic("supabase: ServiceProvider has not been registered")
	}
	instance, err := supabase.App.Make(supabase.Binding)
	if err != nil {
		panic(fmt.Sprintf("supabase: failed to resolve binding %q: %v", supabase.Binding, err))
	}
	sb := instance.(contracts.Supabase)
	if len(connection) > 0 && connection[0] != "" {
		conn, err := sb.Connection(connection[0])
		if err != nil {
			panic(fmt.Sprintf("supabase: failed to resolve connection %q: %v", connection[0], err))
		}
		return conn
	}
	return sb
}
