package store

import (
	"bytes"
	"context"
	"database/sql"
	"maps"
	"slices"
	"strings"
	"testing"
)

const ts = "2026-11-12T16:00:00Z"

// row is the column values of one INSERT.
type row map[string]any

// with returns a copy of base with the changes applied; a nil value is NULL.
func (base row) with(changes row) row {
	out := maps.Clone(base)
	maps.Copy(out, changes)
	return out
}

func insertRow(ctx context.Context, tx *sql.Tx, table string, r row) error {
	cols := slices.Sorted(maps.Keys(r))
	args := make([]any, len(cols))
	for i, c := range cols {
		args[i] = r[c]
	}
	query := "INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES (?" + strings.Repeat(", ?", len(cols)-1) + ")"
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func hash32() []byte { return bytes.Repeat([]byte{0xAB}, 32) }

// Seed rows exist in every constraint test; the candidate rows below never
// collide with them, so a failure comes from the one value a case changes.
var (
	seedUser = row{"id": 1, "name": "Ana", "email": "ana@example.com", "phone": "85999998888",
		"password_hash": "hash", "password_changed_at": ts, "status": "active", "created_at": ts, "updated_at": ts}
	seedCollective = row{"id": 1, "name": "Coletivo Um", "name_key": "coletivo um", "kind": "coletivo",
		"status": "approved", "created_by": 1, "created_at": ts, "updated_at": ts}

	userRow = row{"id": 2, "name": "Bia", "email": "bia@example.com", "phone": "85988887777",
		"password_hash": "hash", "password_changed_at": ts, "status": "pending_email", "created_at": ts, "updated_at": ts}
	collectiveRow = row{"id": 2, "name": "Produtora Dois", "name_key": "produtora dois", "kind": "produtora",
		"status": "pending", "created_at": ts, "updated_at": ts}
	membershipRow = row{"user_id": 1, "collective_id": 1, "role": "owner", "status": "approved", "requested_at": ts}
	eventRow      = row{"collective_id": 1, "created_by": 1, "name": "Festa", "starts_on": "2026-12-05", "start_time": "22:00",
		"duration_min": 240, "type": "festa", "is_free": 1, "style": "techno", "created_at": ts, "updated_at": ts}
	sessionRow = row{"token_hash": hash32(), "user_id": 1, "created_at": ts, "last_seen_at": ts, "expires_at": ts}
	tokenRow   = row{"user_id": 1, "purpose": "verify_email", "token_hash": hash32(), "expires_at": ts, "created_at": ts}
	historyRow = row{"user_id": 1, "password_hash": "old", "created_at": ts}
	outboxRow  = row{"to_addr": "ana@example.com", "subject": "Oi", "body_text": "Oi", "next_attempt_at": ts, "created_at": ts}
	auditRow   = row{"action": "user.approve", "target_type": "user", "created_at": ts}
)

// seededTx opens a transaction holding the seed rows. The test rolls it back,
// so cases do not see each other's rows.
func seededTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	t.Cleanup(func() { tx.Rollback() })
	for _, s := range []struct {
		table string
		row   row
	}{{"users", seedUser}, {"collectives", seedCollective}} {
		if err := insertRow(ctx, tx, s.table, s.row); err != nil {
			t.Fatalf("seed %s: %v", s.table, err)
		}
	}
	return tx
}

func TestSchemaConstraints(t *testing.T) {
	db := openMigrated(t)
	long := func(n int) string { return strings.Repeat("x", n) }

	const (
		check  = "CHECK constraint failed"
		fk     = "FOREIGN KEY constraint failed"
		unique = "UNIQUE constraint failed"
	)
	cases := []struct {
		name    string
		table   string
		row     row
		wantErr string // substring of the error; empty means the insert must succeed
	}{
		// users
		{"valid user", "users", userRow, ""},
		{"user status banned", "users", userRow.with(row{"status": "banned"}), check},
		{"user status disabled", "users", userRow.with(row{"status": "disabled"}), ""},
		{"phone with a letter", "users", userRow.with(row{"phone": "8599999888a"}), check},
		{"phone with a space", "users", userRow.with(row{"phone": "85 99998888"}), check},
		{"phone with a plus", "users", userRow.with(row{"phone": "+5585999998888"}), check},
		{"phone with 9 digits", "users", userRow.with(row{"phone": "859999988"}), check},
		{"phone with 10 digits", "users", userRow.with(row{"phone": "8532221111"}), ""},
		{"phone with 12 digits", "users", userRow.with(row{"phone": "859999988887"}), check},
		{"empty name", "users", userRow.with(row{"name": ""}), check},
		{"name of 120 characters", "users", userRow.with(row{"name": long(120)}), ""},
		{"name of 121 characters", "users", userRow.with(row{"name": long(121)}), check},
		{"email of 2 characters", "users", userRow.with(row{"email": "a@"}), check},
		{"email of 255 characters", "users", userRow.with(row{"email": long(255)}), check},
		{"email differing only in case", "users", userRow.with(row{"email": "ANA@Example.COM"}), unique},
		{"is_whatsapp 2", "users", userRow.with(row{"phone_is_whatsapp": 2}), check},
		{"is_admin 2", "users", userRow.with(row{"is_admin": 2}), check},
		{"negative failed_logins", "users", userRow.with(row{"failed_logins": -1}), check},
		{"missing password hash", "users", userRow.with(row{"password_hash": nil}), "NOT NULL"},
		{"approved_by an unknown user", "users", userRow.with(row{"approved_by": 999}), fk},

		// password_history, sessions, tokens
		{"history of an unknown user", "password_history", historyRow.with(row{"user_id": 999}), fk},
		{"valid session", "sessions", sessionRow, ""},
		{"session of an unknown user", "sessions", sessionRow.with(row{"user_id": 999}), fk},
		{"session hash of 31 bytes", "sessions", sessionRow.with(row{"token_hash": hash32()[:31]}), check},
		{"session hash of 33 bytes", "sessions", sessionRow.with(row{"token_hash": append(hash32(), 0)}), check},
		{"session user agent of 300 characters", "sessions", sessionRow.with(row{"user_agent": long(300)}), ""},
		{"session user agent of 301 characters", "sessions", sessionRow.with(row{"user_agent": long(301)}), check},
		{"valid token", "tokens", tokenRow, ""},
		{"token purpose unknown", "tokens", tokenRow.with(row{"purpose": "login"}), check},
		{"token hash of 31 bytes", "tokens", tokenRow.with(row{"token_hash": hash32()[:31]}), check},
		{"change_email without new_email", "tokens", tokenRow.with(row{"purpose": "change_email"}), check},
		{"change_email with new_email", "tokens", tokenRow.with(row{"purpose": "change_email", "new_email": "novo@example.com"}), ""},
		{"verify_email with new_email", "tokens", tokenRow.with(row{"new_email": "novo@example.com"}), check},

		// collectives, memberships
		{"valid collective", "collectives", collectiveRow, ""},
		{"collective kind unknown", "collectives", collectiveRow.with(row{"kind": "banda"}), check},
		{"collective status unknown", "collectives", collectiveRow.with(row{"status": "open"}), check},
		{"collective name of 1 character", "collectives", collectiveRow.with(row{"name": "X"}), check},
		{"collective name of 81 characters", "collectives", collectiveRow.with(row{"name": long(81)}), check},
		{"collective name_key taken", "collectives", collectiveRow.with(row{"name_key": "coletivo um"}), unique},
		{"valid membership", "memberships", membershipRow, ""},
		{"membership of an unknown user", "memberships", membershipRow.with(row{"user_id": 999}), fk},
		{"membership of an unknown collective", "memberships", membershipRow.with(row{"collective_id": 999}), fk},
		{"membership role unknown", "memberships", membershipRow.with(row{"role": "admin"}), check},
		{"membership status unknown", "memberships", membershipRow.with(row{"status": "open"}), check},

		// events
		{"valid free event", "events", eventRow, ""},
		{"valid paid event", "events", eventRow.with(row{"is_free": 0, "price_cents": 2500}), ""},
		{"event of an unknown collective", "events", eventRow.with(row{"collective_id": 999}), fk},
		{"event of an unknown creator", "events", eventRow.with(row{"created_by": 999}), fk},
		{"event duration 10", "events", eventRow.with(row{"duration_min": 10}), check},
		{"event duration 14", "events", eventRow.with(row{"duration_min": 14}), check},
		{"event duration 15", "events", eventRow.with(row{"duration_min": 15}), ""},
		{"event duration 1439", "events", eventRow.with(row{"duration_min": 1439}), ""},
		{"event duration 1440", "events", eventRow.with(row{"duration_min": 1440}), check},
		{"event type unknown", "events", eventRow.with(row{"type": "show"}), check},
		{"free event with a price", "events", eventRow.with(row{"is_free": 1, "price_cents": 1000}), check},
		{"paid event without a price", "events", eventRow.with(row{"is_free": 0}), check},
		{"paid event with price 0", "events", eventRow.with(row{"is_free": 0, "price_cents": 0}), check},
		{"is_free 2", "events", eventRow.with(row{"is_free": 2}), check},
		{"outros without style_other", "events", eventRow.with(row{"style": "outros"}), check},
		{"outros with style_other", "events", eventRow.with(row{"style": "outros", "style_other": "Forró eletrônico"}), ""},
		{"techno with style_other", "events", eventRow.with(row{"style_other": "Forró"}), check},
		{"outros with a substyle", "events", eventRow.with(row{"style": "outros", "style_other": "Forró", "substyle": "x"}), check},
		{"techno with a substyle", "events", eventRow.with(row{"substyle": "acid"}), ""},
		{"empty style", "events", eventRow.with(row{"style": ""}), check},
		{"empty style_other", "events", eventRow.with(row{"style": "outros", "style_other": ""}), check},
		{"empty event name", "events", eventRow.with(row{"name": ""}), check},
		{"event name of 121 characters", "events", eventRow.with(row{"name": long(121)}), check},
		{"starts_on without zero padding", "events", eventRow.with(row{"starts_on": "2026-1-5"}), check},
		{"starts_on in dd/mm/yyyy", "events", eventRow.with(row{"starts_on": "05/12/2026"}), check},
		{"starts_on with trailing text", "events", eventRow.with(row{"starts_on": "2026-12-05T00"}), check},
		{"start_time 23:59", "events", eventRow.with(row{"start_time": "23:59"}), ""},
		{"start_time 24:00", "events", eventRow.with(row{"start_time": "24:00"}), check},
		{"start_time 25:00", "events", eventRow.with(row{"start_time": "25:00"}), check},
		{"start_time 12:60", "events", eventRow.with(row{"start_time": "12:60"}), check},
		{"start_time without zero padding", "events", eventRow.with(row{"start_time": "9:30"}), check},
		{"ticket_url https", "events", eventRow.with(row{"ticket_url": "https://example.com/ingressos?id=1"}), ""},
		{"ticket_url http", "events", eventRow.with(row{"ticket_url": "http://example.com"}), ""},
		{"ticket_url javascript", "events", eventRow.with(row{"ticket_url": "javascript:alert(1)"}), check},
		{"ticket_url ftp", "events", eventRow.with(row{"ticket_url": "ftp://example.com"}), check},
		{"ticket_url without a scheme", "events", eventRow.with(row{"ticket_url": "example.com"}), check},
		{"ticket_url data", "events", eventRow.with(row{"ticket_url": "data:text/html,x"}), check},
		{"ticket_url of 500 characters", "events", eventRow.with(row{"ticket_url": "https://" + long(492)}), ""},
		{"ticket_url of 501 characters", "events", eventRow.with(row{"ticket_url": "https://" + long(493)}), check},
		{"artists of 2000 characters", "events", eventRow.with(row{"artists": long(2000)}), ""},
		{"artists of 2001 characters", "events", eventRow.with(row{"artists": long(2001)}), check},
		{"description of 4000 characters", "events", eventRow.with(row{"description": long(4000)}), ""},
		{"description of 4001 characters", "events", eventRow.with(row{"description": long(4001)}), check},
		{"substyle of 61 characters", "events", eventRow.with(row{"substyle": long(61)}), check},

		// tables without constraints beyond NOT NULL
		{"valid outbox row", "email_outbox", outboxRow, ""},
		{"outbox row without a recipient", "email_outbox", outboxRow.with(row{"to_addr": nil}), "NOT NULL"},
		{"valid audit row", "audit_log", auditRow, ""},
		{"audit row of an unknown actor", "audit_log", auditRow.with(row{"actor_id": 999}), fk},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := seededTx(t, db)
			err := insertRow(context.Background(), tx, tc.table, tc.row)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("insert failed: %v", err)
			case tc.wantErr != "" && err == nil:
				t.Errorf("insert succeeded, want an error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Errorf("insert error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestSchemaDeleteCascadesAndSetsNull(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)
	tx := seededTx(t, db)

	// User 2 owns nothing but was approved by user 1; the collective, the event
	// and the audit entry point at user 1 too.
	for _, s := range []struct {
		table string
		row   row
	}{
		{"users", userRow.with(row{"approved_by": 1})},
		{"memberships", membershipRow},
		{"sessions", sessionRow},
		{"tokens", tokenRow},
		{"password_history", historyRow},
		{"events", eventRow},
		{"audit_log", auditRow.with(row{"actor_id": 1})},
	} {
		if err := insertRow(ctx, tx, s.table, s.row); err != nil {
			t.Fatalf("insert %s: %v", s.table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE collectives SET approved_by = 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = 1"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	count := func(query string) int {
		t.Helper()
		var n int
		if err := tx.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	for _, table := range []string{"memberships", "sessions", "tokens", "password_history"} {
		if n := count("SELECT COUNT(*) FROM " + table); n != 0 {
			t.Errorf("%s has %d rows after its user was deleted, want 0 (ON DELETE CASCADE)", table, n)
		}
	}
	for query, what := range map[string]string{
		"SELECT COUNT(*) FROM events WHERE created_by IS NULL":                              "events",
		"SELECT COUNT(*) FROM collectives WHERE created_by IS NULL AND approved_by IS NULL": "collectives",
		"SELECT COUNT(*) FROM users WHERE id = 2 AND approved_by IS NULL":                   "users.approved_by",
		"SELECT COUNT(*) FROM audit_log WHERE actor_id IS NULL":                             "audit_log",
	} {
		if n := count(query); n != 1 {
			t.Errorf("%s: %d rows kept with a NULL creator, want 1 (ON DELETE SET NULL)", what, n)
		}
	}
}

func TestSchemaDeletingACollectiveRemovesItsEventsAndMemberships(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)
	tx := seededTx(t, db)
	for _, s := range []struct {
		table string
		row   row
	}{{"memberships", membershipRow}, {"events", eventRow}} {
		if err := insertRow(ctx, tx, s.table, s.row); err != nil {
			t.Fatalf("insert %s: %v", s.table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM collectives WHERE id = 1"); err != nil {
		t.Fatalf("delete collective: %v", err)
	}
	for _, table := range []string{"memberships", "events"} {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s has %d rows (%v) after its collective was deleted, want 0", table, n, err)
		}
	}
}

func TestSchemaIndexes(t *testing.T) {
	db := openMigrated(t)
	rows, err := db.Query(`
		SELECT m.name || ':' || (SELECT group_concat(name, ',') FROM (SELECT name FROM pragma_index_info(il.name) ORDER BY seqno))
		FROM sqlite_master m, pragma_index_list(m.name) il
		WHERE m.type = 'table' AND il.origin = 'c'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := []string{
		"audit_log:created_at",
		"collectives:status,kind",
		"email_outbox:sent_at,next_attempt_at",
		"events:collective_id",
		"events:created_by",
		"events:starts_on",
		"memberships:collective_id,status",
		"password_history:user_id,created_at",
		"sessions:expires_at",
		"sessions:user_id",
		"tokens:user_id,purpose",
		"users:status",
	}
	if !slices.Equal(got, want) {
		t.Errorf("indexes =\n%v\nwant\n%v", got, want)
	}
}
