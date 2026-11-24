package supabase

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/database/db"
)

const DefaultAuthSessionsTable = "supabase_auth_sessions"

// Dialect identifies the SQL placeholder and upsert syntax for a Goravel DB driver.
type Dialect string

const (
	DialectPostgres  Dialect = "postgres"
	DialectMySQL     Dialect = "mysql"
	DialectSQLite    Dialect = "sqlite"
	DialectSQLServer Dialect = "sqlserver"
)

var validTableNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$`)

// NormalizeDialect maps Goravel database driver names to a supported Dialect.
func NormalizeDialect(driver string) Dialect {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql", "pgx":
		return DialectPostgres
	case "mysql", "mariadb":
		return DialectMySQL
	case "sqlserver", "mssql":
		return DialectSQLServer
	default:
		return DialectSQLite
	}
}

type authSessionRow struct {
	Value string `db:"value"`
}

// DBStorage implements auth.Storage using Goravel's contracts/database/db.DB raw SQL methods
// (Select, Insert, Update, Delete, Statement) with dialect-aware placeholders and upserts.
type DBStorage struct {
	db          db.DB
	connection  string
	table       string
	dialect     Dialect
	autoMigrate bool

	ensureMu sync.Mutex
	ensured  bool
}

// DBStorageOptions configures DBStorage.
type DBStorageOptions struct {
	Connection  string
	Table       string
	Dialect     Dialect
	AutoMigrate bool
}

// NewDBStorage creates a new raw-SQL DBStorage backed by Goravel's db.DB.
func NewDBStorage(database db.DB, opts DBStorageOptions) (*DBStorage, error) {
	table := strings.TrimSpace(opts.Table)
	if table == "" {
		table = DefaultAuthSessionsTable
	}
	if !validTableNameRe.MatchString(table) {
		return nil, fmt.Errorf("supabase: invalid auth session table name %q", table)
	}
	dialect := opts.Dialect
	if dialect == "" {
		dialect = DialectSQLite
	}
	return &DBStorage{
		db:          database,
		connection:  opts.Connection,
		table:       table,
		dialect:     dialect,
		autoMigrate: opts.AutoMigrate,
	}, nil
}

func (s *DBStorage) conn(ctx context.Context) db.DB {
	d := s.db
	if s.connection != "" {
		d = d.Connection(s.connection)
	}
	if ctx != nil {
		d = d.WithContext(ctx)
	}
	return d
}

// BuildCreateTableSQL returns the dialect-appropriate CREATE TABLE DDL statement.
func (s *DBStorage) BuildCreateTableSQL() string {
	switch s.dialect {
	case DialectSQLServer:
		return fmt.Sprintf(
			`IF OBJECT_ID(N'%s', N'U') IS NULL CREATE TABLE %s ([key] VARCHAR(255) PRIMARY KEY, [value] NVARCHAR(MAX) NOT NULL, updated_at DATETIME2 NOT NULL)`,
			s.table, s.table,
		)
	case DialectMySQL:
		return fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (`key` VARCHAR(255) PRIMARY KEY, `value` TEXT NOT NULL, updated_at TIMESTAMP NOT NULL)",
			s.table,
		)
	default:
		return fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s (key VARCHAR(255) PRIMARY KEY, value TEXT NOT NULL, updated_at TIMESTAMP NOT NULL)`,
			s.table,
		)
	}
}

// BuildSelectSQL returns the dialect-appropriate SELECT SQL query.
func (s *DBStorage) BuildSelectSQL() string {
	switch s.dialect {
	case DialectPostgres:
		return fmt.Sprintf(`SELECT value FROM %s WHERE key = $1 LIMIT 1`, s.table)
	case DialectMySQL:
		return fmt.Sprintf("SELECT `value` FROM %s WHERE `key` = ? LIMIT 1", s.table)
	case DialectSQLServer:
		return fmt.Sprintf(`SELECT TOP 1 [value] AS value FROM %s WHERE [key] = @p1`, s.table)
	default:
		return fmt.Sprintf(`SELECT value FROM %s WHERE key = ? LIMIT 1`, s.table)
	}
}

// BuildUpsertSQL returns the dialect-appropriate UPSERT SQL statement for Postgres/MySQL/SQLite/SQLServer.
func (s *DBStorage) BuildUpsertSQL() string {
	switch s.dialect {
	case DialectPostgres:
		return fmt.Sprintf(
			`INSERT INTO %s (key, value, updated_at) VALUES ($1, $2, $3) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
			s.table,
		)
	case DialectMySQL:
		return fmt.Sprintf(
			"INSERT INTO %s (`key`, `value`, updated_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE `value` = VALUES(`value`), updated_at = VALUES(updated_at)",
			s.table,
		)
	case DialectSQLServer:
		return fmt.Sprintf(
			`MERGE %s AS target USING (VALUES (@p1, @p2, @p3)) AS source ([key], [value], updated_at) ON target.[key] = source.[key] WHEN MATCHED THEN UPDATE SET [value] = source.[value], updated_at = source.updated_at WHEN NOT MATCHED THEN INSERT ([key], [value], updated_at) VALUES (source.[key], source.[value], source.updated_at);`,
			s.table,
		)
	default:
		return fmt.Sprintf(
			`INSERT INTO %s (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			s.table,
		)
	}
}

// BuildDeleteSQL returns the dialect-appropriate DELETE SQL query.
func (s *DBStorage) BuildDeleteSQL() string {
	switch s.dialect {
	case DialectPostgres:
		return fmt.Sprintf(`DELETE FROM %s WHERE key = $1`, s.table)
	case DialectMySQL:
		return fmt.Sprintf("DELETE FROM %s WHERE `key` = ?", s.table)
	case DialectSQLServer:
		return fmt.Sprintf(`DELETE FROM %s WHERE [key] = @p1`, s.table)
	default:
		return fmt.Sprintf(`DELETE FROM %s WHERE key = ?`, s.table)
	}
}

// EnsureTable creates the auth session table if it does not exist.
func (s *DBStorage) EnsureTable(ctx context.Context) error {
	return s.conn(ctx).Statement(s.BuildCreateTableSQL())
}

func (s *DBStorage) maybeEnsure(ctx context.Context) error {
	if !s.autoMigrate {
		return nil
	}
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if s.ensured {
		return nil
	}
	if err := s.EnsureTable(ctx); err != nil {
		return err
	}
	s.ensured = true
	return nil
}

// GetItem fetches the stored value for key using raw SELECT, returning ("", nil) when absent.
func (s *DBStorage) GetItem(ctx context.Context, key string) (string, error) {
	if err := s.maybeEnsure(ctx); err != nil {
		return "", err
	}
	var rows []authSessionRow
	if err := s.conn(ctx).Select(&rows, s.BuildSelectSQL(), key); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].Value, nil
}

// SetItem upserts (key, value, updated_at) using dialect-appropriate raw SQL.
func (s *DBStorage) SetItem(ctx context.Context, key, value string) error {
	if err := s.maybeEnsure(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := s.conn(ctx).Insert(s.BuildUpsertSQL(), key, value, now)
	return err
}

// RemoveItem deletes the stored entry for key using raw DELETE.
func (s *DBStorage) RemoveItem(ctx context.Context, key string) error {
	if err := s.maybeEnsure(ctx); err != nil {
		return err
	}
	_, err := s.conn(ctx).Delete(s.BuildDeleteSQL(), key)
	return err
}
