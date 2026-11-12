// Package store owns the SQLite database: opening it safely, applying the
// embedded migrations and taking backups.
package store

import (
	"context"
	"database/sql"
	"errors"
)

// Open opens the database file at path.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	return nil, errors.New("not implemented")
}

// Backup writes a consistent copy of the database to dest.
func Backup(ctx context.Context, db *sql.DB, dest string) error {
	return errors.New("not implemented")
}
