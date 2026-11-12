package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationName is NNNN_name.sql: a four-digit version, then a lower-case name.
var migrationName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

type migration struct {
	version int
	name    string
}

// Migrate applies the embedded migrations that the database has not seen yet.
func Migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("store: embedded migrations: %w", err)
	}
	return migrateFS(ctx, db, sub)
}

// migrateFS applies the migrations in fsys, a flat directory of NNNN_name.sql
// files. The whole set is checked before anything runs: versions must be 1..N
// with no gap and no duplicate, so a bad set changes nothing. Each pending
// file then runs in its own transaction together with its schema_migrations
// row, so a failure leaves that file without a trace.
func migrateFS(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	migrations, err := listMigrations(fsys)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}
	for v := range applied {
		if v < 1 || v > len(migrations) {
			return fmt.Errorf("store: database has migration %d, newer than this build knows (latest %d)", v, len(migrations))
		}
	}
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applyMigration(ctx, db, fsys, m); err != nil {
			return err
		}
	}
	return nil
}

// listMigrations reads and validates the file names in fsys.
func listMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var out []migration // ReadDir sorts by name, which is numeric order for four digits
	for _, e := range entries {
		match := migrationName.FindStringSubmatch(e.Name())
		if match == nil || e.IsDir() {
			return nil, fmt.Errorf("store: migration %q: want a file named NNNN_name.sql", e.Name())
		}
		version, _ := strconv.Atoi(match[1]) // four digits: cannot fail
		if want := len(out) + 1; version != want {
			return nil, fmt.Errorf("store: migration %q has version %d, want %d: versions run from 1 with no gap or duplicate", e.Name(), version, want)
		}
		out = append(out, migration{version: version, name: e.Name()})
	}
	return out, nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("store: read schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, db *sql.DB, fsys fs.FS, m migration) error {
	script, err := fs.ReadFile(fsys, m.name)
	if err != nil {
		return fmt.Errorf("store: read migration %s: %w", m.name, err)
	}
	tx, err := db.BeginTx(ctx, nil) // immediate: holds the write lock from here on
	if err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	defer tx.Rollback()

	// Another process may have applied it between our read and the lock.
	var done int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.version).Scan(&done); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if done > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, string(script)); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.version, FormatTime(time.Now())); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	return nil
}
