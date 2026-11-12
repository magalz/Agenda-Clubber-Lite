package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// openTemp opens an empty database in a fresh temporary directory.
func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "agenda.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openMigrated opens a database with every embedded migration applied.
func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	db := openTemp(t)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func TestOpenAppliesPragmasOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if got := db.Stats().MaxOpenConnections; got != 8 {
		t.Errorf("MaxOpenConnections = %d, want 8", got)
	}

	// Holding the connections makes the pool open that many distinct ones.
	const held = 6
	want := []struct{ pragma, value string }{
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"}, // NORMAL
		{"temp_store", "2"},  // MEMORY
	}
	for i := range held {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		defer conn.Close()
		for _, w := range want {
			var got string
			if err := conn.QueryRowContext(ctx, "PRAGMA "+w.pragma).Scan(&got); err != nil {
				t.Fatalf("conn %d PRAGMA %s: %v", i, w.pragma, err)
			}
			if got != w.value {
				t.Errorf("conn %d PRAGMA %s = %q, want %q", i, w.pragma, got, w.value)
			}
		}
	}
}

func TestOpenBeginsTransactionsImmediately(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)

	first, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("first BeginTx: %v", err)
	}
	defer first.Rollback()

	// A deferred transaction would begin without a lock and succeed here.
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer other.Close()
	if _, err := other.ExecContext(ctx, "PRAGMA busy_timeout = 50"); err != nil {
		t.Fatalf("shorten busy_timeout: %v", err)
	}
	if tx, err := other.BeginTx(ctx, nil); err == nil {
		tx.Rollback()
		t.Error("second BeginTx succeeded while the first holds the write lock, want a busy error")
	}
}

func TestOpenCreatesDataDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "data")
	path := filepath.Join(dir, "agenda.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil || !dirInfo.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil || !fileInfo.Mode().IsRegular() {
		t.Fatalf("database file not created: %v", err)
	}
	if runtime.GOOS != "windows" { // Windows has no Unix permission bits
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Errorf("data dir mode = %o, want 700", got)
		}
		if got := fileInfo.Mode().Perm(); got != 0o600 {
			t.Errorf("database file mode = %o, want 600", got)
		}
	}
}

func TestOpenKeepsExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agenda.db")
	if err := os.WriteFile(path, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("existing file mode = %v (%v), want it untouched at 640", info.Mode().Perm(), err)
		}
	}
}

// TestOpenPaths covers what a path can contain: Windows drive letters and
// backslashes come from t.TempDir on Windows; the rest are characters a URI
// would otherwise swallow.
func TestOpenPaths(t *testing.T) {
	for _, dirName := range []string{"plain", "with space", "dados 100% #1 (cópia)"} {
		t.Run(dirName, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), dirName, "agenda.db")
			db, err := Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()
			if _, err := db.Exec("CREATE TABLE t (x INTEGER)"); err != nil {
				t.Fatalf("write: %v", err)
			}
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				t.Errorf("database file at %q missing or empty: %v", path, err)
			}
		})
	}
}

func TestOpenRelativePath(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := Open(context.Background(), filepath.Join("data", "agenda.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(filepath.Join("data", "agenda.db")); err != nil {
		t.Errorf("database not created relative to the working directory: %v", err)
	}
}

func TestOpenErrors(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"empty path":               "",
		"parent is a regular file": filepath.Join(blocker, "agenda.db"),
		"path is a directory":      t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			if db, err := Open(context.Background(), path); err == nil {
				db.Close()
				t.Error("Open succeeded, want an error")
			}
		})
	}
}

func TestBackupCopiesTheDatabase(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, name, email, phone, password_hash, password_changed_at, status, created_at, updated_at)
		VALUES (7, 'Ana', 'ana@example.com', '85999998888', 'hash', '2026-11-12T16:00:00Z', 'active', '2026-11-12T16:00:00Z', '2026-11-12T16:00:00Z')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := Backup(ctx, db, dest); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	copied, err := Open(ctx, dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer copied.Close()
	if got, want := tableNames(t, copied), tableNames(t, db); !slices.Equal(got, want) {
		t.Errorf("backup tables = %v, want %v", got, want)
	}
	var name string
	if err := copied.QueryRowContext(ctx, "SELECT name FROM users WHERE id = 7").Scan(&name); err != nil || name != "Ana" {
		t.Errorf("copied row = %q, %v; want Ana", name, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("backup file mode = %v (%v), want 600: it holds password hashes", info.Mode().Perm(), err)
		}
	}
}

func TestBackupRefusesAnExistingDestination(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)
	for name, content := range map[string][]byte{"with content": []byte("keep me"), "empty": nil} {
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "backup.db")
			if err := os.WriteFile(dest, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Backup(ctx, db, dest); err == nil {
				t.Fatal("Backup succeeded over an existing file, want an error")
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != string(content) {
				t.Errorf("destination = %q, %v; want it untouched (%q)", got, err, content)
			}
		})
	}
}

func TestBackupToMissingDirectoryFailsWithoutLeavingAFile(t *testing.T) {
	db := openMigrated(t)
	dest := filepath.Join(t.TempDir(), "missing", "backup.db")
	if err := Backup(context.Background(), db, dest); err == nil {
		t.Fatal("Backup succeeded, want an error")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a file was left behind")
	}
}

// tableNames lists the user tables of db, sorted.
func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}
