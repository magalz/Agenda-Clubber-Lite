package store

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"testing/fstest"
)

var wantTables = []string{
	"audit_log", "collectives", "email_outbox", "events", "memberships",
	"password_history", "schema_migrations", "sessions", "tokens", "users",
}

func TestMigrateCreatesTheSchema(t *testing.T) {
	db := openMigrated(t)
	if got := tableNames(t, db); !slices.Equal(got, wantTables) {
		t.Errorf("tables = %v, want %v", got, wantTables)
	}
}

func TestMigrateTwiceIsANoOp(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)
	first := appliedMigrations(t, db)
	if len(first) != 1 || first[0].version != 1 {
		t.Fatalf("schema_migrations = %+v, want exactly version 1", first)
	}
	if _, err := ParseTime(first[0].appliedAt); err != nil {
		t.Errorf("applied_at %q is not a database timestamp: %v", first[0].appliedAt, err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if second := appliedMigrations(t, db); !slices.Equal(first, second) {
		t.Errorf("schema_migrations changed on the second run: %+v -> %+v", first, second)
	}
}

func TestMigrateAppliesFilesInNumericOrderAndOnlyOnce(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	fsys := fstest.MapFS{
		"0003_c.sql": {Data: []byte("INSERT INTO log VALUES ('c');")},
		"0001_a.sql": {Data: []byte("CREATE TABLE log (v TEXT); INSERT INTO log VALUES ('a');")},
		"0002_b.sql": {Data: []byte("INSERT INTO log VALUES ('b');")},
	}
	for range 2 {
		if err := migrateFS(ctx, db, fsys); err != nil {
			t.Fatalf("migrateFS: %v", err)
		}
	}
	var got []string
	rows, err := db.QueryContext(ctx, "SELECT v FROM log ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v (each file once, in numeric order)", got, want)
	}
	if applied := appliedMigrations(t, db); len(applied) != 3 || applied[0].version != 1 || applied[1].version != 2 || applied[2].version != 3 {
		t.Errorf("schema_migrations = %+v, want versions 1, 2, 3", applied)
	}
}

func TestMigrateAppliesOnlyNewFilesOnLaterRuns(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	one := fstest.MapFS{"0001_a.sql": {Data: []byte("CREATE TABLE a (x INTEGER);")}}
	if err := migrateFS(ctx, db, one); err != nil {
		t.Fatalf("first migrateFS: %v", err)
	}
	two := fstest.MapFS{
		"0001_a.sql": one["0001_a.sql"], // would fail if it ran again
		"0002_b.sql": {Data: []byte("CREATE TABLE b (x INTEGER);")},
	}
	if err := migrateFS(ctx, db, two); err != nil {
		t.Fatalf("second migrateFS: %v", err)
	}
	if got, want := tableNames(t, db), []string{"a", "b", "schema_migrations"}; !slices.Equal(got, want) {
		t.Errorf("tables = %v, want %v", got, want)
	}
}

func TestMigrateRejectsBadMigrationSets(t *testing.T) {
	sql1 := []byte("CREATE TABLE a (x INTEGER);")
	for name, fsys := range map[string]fstest.MapFS{
		"gap":                 {"0001_a.sql": {Data: sql1}, "0003_c.sql": {Data: sql1}},
		"does not start at 1": {"0002_b.sql": {Data: sql1}},
		"version zero":        {"0000_a.sql": {Data: sql1}},
		"duplicate version":   {"0001_a.sql": {Data: sql1}, "0001_b.sql": {Data: sql1}},
		"no zero padding":     {"1_a.sql": {Data: sql1}},
		"dash instead of _":   {"0001-a.sql": {Data: sql1}},
		"missing name":        {"0001_.sql": {Data: sql1}},
		"upper case name":     {"0001_Init.sql": {Data: sql1}},
		"wrong extension":     {"0001_a.txt": {Data: sql1}},
		"stray file":          {"0001_a.sql": {Data: sql1}, "README.md": {Data: []byte("x")}},
		"subdirectory":        {"0001_a.sql": {Data: sql1}, "sub/0002_b.sql": {Data: sql1}},
		"five-digit version":  {"00001_a.sql": {Data: sql1}},
		"space in name":       {"0001_a b.sql": {Data: sql1}},
	} {
		t.Run(name, func(t *testing.T) {
			db := openTemp(t)
			if err := migrateFS(context.Background(), db, fsys); err == nil {
				t.Fatal("migrateFS succeeded, want an error")
			}
			if got := tableNames(t, db); slices.Contains(got, "a") {
				t.Errorf("tables = %v: a rejected set must apply nothing", got)
			}
		})
	}
}

func TestMigrateRollsBackAFailingFile(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	fsys := fstest.MapFS{
		"0001_ok.sql":    {Data: []byte("CREATE TABLE ok (x INTEGER);")},
		"0002_bad.sql":   {Data: []byte("CREATE TABLE half (x INTEGER); THIS IS NOT SQL;")},
		"0003_after.sql": {Data: []byte("CREATE TABLE after (x INTEGER);")},
	}
	if err := migrateFS(ctx, db, fsys); err == nil {
		t.Fatal("migrateFS succeeded, want the failing file's error")
	}
	if got, want := tableNames(t, db), []string{"ok", "schema_migrations"}; !slices.Equal(got, want) {
		t.Errorf("tables = %v, want %v: the failed file leaves no trace and later files do not run", got, want)
	}
	if applied := appliedMigrations(t, db); len(applied) != 1 || applied[0].version != 1 {
		t.Errorf("schema_migrations = %+v, want only version 1", applied)
	}
}

func TestMigrateRefusesADatabaseNewerThanTheBuild(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (2, '2026-11-12T16:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err == nil {
		t.Error("Migrate succeeded on a database with an unknown version, want an error")
	}
}

type appliedRow struct {
	version   int
	appliedAt string
}

func appliedMigrations(t *testing.T, db *sql.DB) []appliedRow {
	t.Helper()
	rows, err := db.Query("SELECT version, applied_at FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	var out []appliedRow
	for rows.Next() {
		var r appliedRow
		if err := rows.Scan(&r.version, &r.appliedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
