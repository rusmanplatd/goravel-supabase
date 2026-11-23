package supabase_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/goravel/framework/contracts/database/db"

	supabase "github.com/rusmanplatd/goravel-supabase"
)

type fakeDB struct {
	db.DB
	mu         sync.Mutex
	store      map[string]string
	statements []string
	lastSelect string
	lastInsert string
	lastUpdate string
	lastDelete string
	connName   string
}

func newFakeDB() *fakeDB {
	return &fakeDB{store: make(map[string]string)}
}

func (f *fakeDB) Connection(name string) db.DB {
	f.mu.Lock()
	f.connName = name
	f.mu.Unlock()
	return f
}

func (f *fakeDB) WithContext(ctx context.Context) db.DB { return f }

func (f *fakeDB) Statement(sql string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statements = append(f.statements, sql)
	return nil
}

func (f *fakeDB) Select(dest any, sql string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSelect = sql
	key := args[0].(string)
	val, ok := f.store[key]
	if !ok {
		return nil
	}
	// Populate slice via reflection-free type assertion on the unexported struct? Use JSON roundtrip or direct check.
	// Since authSessionRow is unexported in package supabase, we test via package-level helper in supabase_test
	// or reflect. Let's use reflect to set the Value field cleanly.
	setRowValue(dest, val)
	return nil
}

func (f *fakeDB) Insert(sql string, args ...any) (*db.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastInsert = sql
	key := args[0].(string)
	val := args[1].(string)
	f.store[key] = val
	return &db.Result{RowsAffected: 1}, nil
}

func (f *fakeDB) Update(sql string, args ...any) (*db.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUpdate = sql
	val := args[0].(string)
	key := args[2].(string)
	if _, ok := f.store[key]; !ok {
		return &db.Result{RowsAffected: 0}, nil
	}
	f.store[key] = val
	return &db.Result{RowsAffected: 1}, nil
}

func (f *fakeDB) Delete(sql string, args ...any) (*db.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastDelete = sql
	key := args[0].(string)
	delete(f.store, key)
	return &db.Result{RowsAffected: 1}, nil
}

func TestDBStorageDialectsAndCRUD(t *testing.T) {
	dialects := []struct {
		dialect    supabase.Dialect
		wantSelect string
		wantUpsert string
		wantDelete string
	}{
		{supabase.DialectPostgres, "$1", "ON CONFLICT (key) DO UPDATE", "$1"},
		{supabase.DialectMySQL, "?", "ON DUPLICATE KEY UPDATE", "?"},
		{supabase.DialectSQLite, "?", "ON CONFLICT (key) DO UPDATE", "?"},
		{supabase.DialectSQLServer, "@p1", "", "@p1"},
	}

	for _, tc := range dialects {
		t.Run(string(tc.dialect), func(t *testing.T) {
			fdb := newFakeDB()
			st, err := supabase.NewDBStorage(fdb, supabase.DBStorageOptions{
				Table:       "supabase_auth_sessions",
				Dialect:     tc.dialect,
				AutoMigrate: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(st.BuildSelectSQL(), tc.wantSelect) {
				t.Fatalf("select sql %q missing %q", st.BuildSelectSQL(), tc.wantSelect)
			}
			if tc.wantUpsert != "" && !strings.Contains(st.BuildUpsertSQL(), tc.wantUpsert) {
				t.Fatalf("upsert sql %q missing %q", st.BuildUpsertSQL(), tc.wantUpsert)
			}
			if !strings.Contains(st.BuildDeleteSQL(), tc.wantDelete) {
				t.Fatalf("delete sql %q missing %q", st.BuildDeleteSQL(), tc.wantDelete)
			}

			ctx := context.Background()
			if err := st.SetItem(ctx, "k1", "v1"); err != nil {
				t.Fatal(err)
			}
			if len(fdb.statements) != 1 {
				t.Fatalf("expected 1 auto-migrate CREATE TABLE statement, got %d", len(fdb.statements))
			}
			got, err := st.GetItem(ctx, "k1")
			if err != nil || got != "v1" {
				t.Fatalf("GetItem = %q, %v", got, err)
			}
			if err := st.SetItem(ctx, "k1", "v2"); err != nil {
				t.Fatal(err)
			}
			got, err = st.GetItem(ctx, "k1")
			if err != nil || got != "v2" {
				t.Fatalf("GetItem after update = %q, %v", got, err)
			}
			if err := st.RemoveItem(ctx, "k1"); err != nil {
				t.Fatal(err)
			}
			got, err = st.GetItem(ctx, "k1")
			if err != nil || got != "" {
				t.Fatalf("GetItem after delete = %q, %v", got, err)
			}
		})
	}
}
