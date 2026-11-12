// Package store owns the SQLite database: opening it safely, applying the
// embedded migrations and taking backups.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the CGO-free "sqlite" driver
)

const maxOpenConns = 8

// connQuery is the DSN query that configures every pooled connection: each
// _pragma runs when a connection opens, so no connection runs without foreign
// keys or the busy timeout. _txlock makes BeginTx take the write lock at once,
// so a read-then-write transaction cannot fail midway with SQLITE_BUSY.
var connQuery = url.Values{
	"_pragma": {
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"temp_store(MEMORY)",
	},
	"_txlock": {"immediate"},
}

// Open opens the database file at path, creating the directory (0700) and the
// file (0600) when they are missing, and checks the connection with a ping.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data directory: %w", err)
	}
	// SQLite would create the file with the process umask; do it here so it is
	// private to the owner. An existing file keeps its mode.
	f, err := os.OpenFile(abs, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}

	db, err := sql.Open("sqlite", dsn(abs))
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", abs, err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %q: %w", abs, err)
	}
	return db, nil
}

// dsn builds a file: URI for the absolute path abs. A URI keeps its query for
// the driver and, with percent-escaping, carries any character a path can
// hold (spaces, '#', '?', '%'); on Windows the drive letter follows an empty
// authority and a slash: file:///C:/data/agenda.db.
func dsn(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: connQuery.Encode()}
	return u.String()
}

// Backup writes a consistent copy of the database to dest, which must not
// exist. The copy holds password hashes, so it is created with mode 0600.
func Backup(ctx context.Context, db *sql.DB, dest string) error {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return fmt.Errorf("store: resolve %q: %w", dest, err)
	}
	// O_EXCL refuses an existing file atomically and fixes the mode; VACUUM
	// INTO accepts an empty file. Close before it: SQLite opens the path itself.
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("store: create backup file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(abs)
		return fmt.Errorf("store: create backup file: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
		os.Remove(abs) // no half-written backup left to be mistaken for a good one
		return fmt.Errorf("store: backup to %q: %w", abs, err)
	}
	return nil
}
