package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
)

// Migrate applies the pending embedded migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	return errors.New("not implemented")
}

// migrateFS applies the migrations found in fsys, a flat directory of
// NNNN_name.sql files.
func migrateFS(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	return errors.New("not implemented")
}
