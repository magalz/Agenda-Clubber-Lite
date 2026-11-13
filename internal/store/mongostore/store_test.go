package mongostore_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongostore"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
	"github.com/magalz/agenda-clubber-lite/internal/store/storetest"
)

// The contract suite runs against a real MongoDB; without MONGO_TEST_URI it
// skips (and fails when CI is set).

func newStore(t *testing.T) domain.Store { return mongotest.New(t) }

func TestStore(t *testing.T) { storetest.Run(t, newStore) }

// --- Open ---

// passwordMarker stands for a password inside a URI: it must never show up in
// an error.
const passwordMarker = "PWMARKER-7f3a"

// wantSafeError requires an error from Open that carries nothing of uri: not its
// password, user, host, port or options, neither in its text nor in a wrapped
// error that a caller could unwrap and print.
func wantSafeError(t *testing.T, err error, uri string) {
	t.Helper()
	if err == nil {
		t.Fatal("Open succeeded, want an error")
	}
	if inner := errors.Unwrap(err); inner != nil {
		t.Errorf("the error wraps another one, which may carry the URI: %v", inner)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok && len(joined.Unwrap()) > 0 {
		t.Errorf("the error wraps other errors, which may carry the URI: %v", joined.Unwrap())
	}
	msg := err.Error()
	for _, leak := range []string{passwordMarker, "user", "127.0.0.1", "notaport", ":1/", "directConnection", "mongodb://", uri} {
		if strings.Contains(msg, leak) {
			t.Errorf("the error leaks %q from the URI: %v", leak, err)
		}
	}
}

func TestOpenRefusesAMalformedURI(t *testing.T) {
	uri := "mongodb://user:" + passwordMarker + "@127.0.0.1:notaport/"
	_, err := mongostore.Open(t.Context(), uri, "acltest_x")
	wantSafeError(t, err, uri)
}

func TestOpenRefusesAnUnreachableServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	uri := "mongodb://user:" + passwordMarker + "@127.0.0.1:1/?directConnection=true"
	_, err := mongostore.Open(ctx, uri, "acltest_x")
	wantSafeError(t, err, uri)
}

func TestOpenRefusesAnEmptyDatabaseName(t *testing.T) {
	if _, err := mongostore.Open(t.Context(), "mongodb://127.0.0.1:1/?directConnection=true", ""); err == nil {
		t.Fatal("Open succeeded with an empty database name")
	}
}

func TestRequireReplicaSet(t *testing.T) {
	if err := mongostore.RequireReplicaSet(bson.M{"setName": "rs0", "isWritablePrimary": true}); err != nil {
		t.Errorf("a replica set member: %v", err)
	}
	for name, hello := range map[string]bson.M{
		"standalone":     {"isWritablePrimary": true},
		"empty set name": {"setName": ""},
		"set name type":  {"setName": 7},
	} {
		err := mongostore.RequireReplicaSet(hello)
		if err == nil {
			t.Errorf("%s: want an error", name)
		} else if !strings.Contains(err.Error(), "replica set") {
			t.Errorf("%s: the error does not say what to do: %v", name, err)
		}
	}
}

// --- schema ---

var collections = []string{"users", "tokens", "sessions", "collectives", "memberships", "outbox", "audit_entries"}

// wantIndexes are the index names of each collection. There is no TTL index:
// the contract says a backend never expires data on its own.
var wantIndexes = map[string][]string{
	"users":         {"_id_", "email_unique", "status_created_at"},
	"tokens":        {"_id_", "hash_unique", "user_id_purpose"},
	"sessions":      {"_id_", "user_id"},
	"collectives":   {"_id_", "name_key_unique", "status_kind_name_key"},
	"memberships":   {"_id_", "collective_id_status", "status_requested_at", "user_id_collective_id_unique"},
	"outbox":        {"_id_", "sent_at_gave_up_next_attempt_at"},
	"audit_entries": {"_id_", "at_desc_id_desc"},
}

func indexes(t *testing.T, s *mongostore.Store, collection string) []bson.M {
	t.Helper()
	cur, err := s.DB().Collection(collection).Indexes().List(t.Context())
	if err != nil {
		t.Fatalf("list the indexes of %s: %v", collection, err)
	}
	var out []bson.M
	if err := cur.All(t.Context(), &out); err != nil {
		t.Fatalf("read the indexes of %s: %v", collection, err)
	}
	return out
}

func indexNames(t *testing.T, s *mongostore.Store) map[string][]string {
	t.Helper()
	got := map[string][]string{}
	for _, c := range collections {
		for _, ix := range indexes(t, s, c) {
			got[c] = append(got[c], ix["name"].(string))
		}
		slices.Sort(got[c])
	}
	return got
}

func TestEnsureSchemaCreatesTheIndexes(t *testing.T) {
	s := mongotest.New(t)
	got := indexNames(t, s)
	for _, c := range collections {
		if !slices.Equal(got[c], wantIndexes[c]) {
			t.Errorf("indexes of %s = %v, want %v", c, got[c], wantIndexes[c])
		}
		for _, ix := range indexes(t, s, c) {
			name := ix["name"].(string)
			if _, ttl := ix["expireAfterSeconds"]; ttl {
				t.Errorf("%s.%s is a TTL index: a backend never expires data on its own", c, name)
			}
			unique, _ := ix["unique"].(bool)
			if want := strings.HasSuffix(name, "_unique"); unique != want {
				t.Errorf("%s.%s unique = %v, want %v", c, name, unique, want)
			}
		}
	}
}

func TestEnsureSchemaIsIdempotent(t *testing.T) {
	s := mongotest.New(t) // has run EnsureSchema once
	before := indexNames(t, s)
	for i := range 2 {
		if err := s.EnsureSchema(t.Context()); err != nil {
			t.Fatalf("EnsureSchema #%d: %v", i+2, err)
		}
	}
	after := indexNames(t, s)
	for _, c := range collections {
		if !slices.Equal(before[c], after[c]) {
			t.Errorf("indexes of %s changed: %v, then %v", c, before[c], after[c])
		}
	}
}

func TestEnsureSchemaUpdatesTheValidatorOfAnExistingCollection(t *testing.T) {
	s := mongotest.New(t)
	ctx := t.Context()
	// Loosen the validator the way an older version of the schema might have it.
	loose := bson.D{{Key: "collMod", Value: "users"}, {Key: "validator", Value: bson.D{}}, {Key: "validationLevel", Value: "off"}}
	if err := s.DB().RunCommand(ctx, loose).Err(); err != nil {
		t.Fatalf("collMod: %v", err)
	}
	if _, err := s.DB().Collection("users").InsertOne(ctx, bson.M{"unexpected": true}); err != nil {
		t.Fatalf("insert with the validator off: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if _, err := s.DB().Collection("users").InsertOne(ctx, bson.M{"unexpected": true}); !isValidationFailure(err) {
		t.Errorf("insert after EnsureSchema: error = %v, want a document validation failure", err)
	}
}

func isValidationFailure(err error) bool {
	var we mongo.WriteException
	return errors.As(err, &we) && we.HasErrorCode(121)
}

func TestSchemaRefusesMalformedDocuments(t *testing.T) {
	s := mongotest.New(t)
	for _, c := range collections {
		_, err := s.DB().Collection(c).InsertOne(t.Context(), bson.M{"unexpected": true})
		if !isValidationFailure(err) {
			t.Errorf("insert into %s: error = %v, want a document validation failure", c, err)
		}
	}

	// A stored document with one field of the wrong type is refused too.
	u := newUser("ana")
	if err := s.Repos().Users.Create(t.Context(), &u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	var raw bson.M
	if err := s.DB().Collection("users").FindOne(t.Context(), bson.M{}).Decode(&raw); err != nil {
		t.Fatalf("read the user: %v", err)
	}
	raw["_id"] = bson.NewObjectID()
	raw["email"] = "other@example.com"
	raw["phone"] = 11987654321
	if _, err := s.DB().Collection("users").InsertOne(t.Context(), raw); !isValidationFailure(err) {
		t.Errorf("insert with a numeric phone: error = %v, want a document validation failure", err)
	}
}

// rawCollective stores a collective and returns its document with a new _id and a
// new name_key, ready to be inserted again (the name key is unique).
func rawCollective(t *testing.T, s *mongostore.Store) bson.M {
	t.Helper()
	name := "c" + bson.NewObjectID().Hex()
	c := domain.Collective{Name: name, NameKey: domain.NameKey(name), Kind: domain.CollectiveKindColetivo,
		Status: domain.CollectiveStatusPending, CreatedAt: now, UpdatedAt: now}
	if err := s.Repos().Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("create collective: %v", err)
	}
	var raw bson.M
	if err := s.DB().Collection("collectives").FindOne(t.Context(), bson.M{"name_key": c.NameKey}).Decode(&raw); err != nil {
		t.Fatalf("read the collective: %v", err)
	}
	raw["_id"] = bson.NewObjectID()
	raw["name_key"] = "copy " + c.NameKey
	return raw
}

func TestSchemaChecksTheCollectiveFlagsAndTheRevision(t *testing.T) {
	s := mongotest.New(t)
	coll := s.DB().Collection("collectives")

	for field, bad := range map[string]any{"events_blocked": "yes", "locked": 1, "rev": "1"} {
		raw := rawCollective(t, s)
		raw[field] = bad
		if _, err := coll.InsertOne(t.Context(), raw); !isValidationFailure(err) {
			t.Errorf("insert with a malformed %s: error = %v, want a document validation failure", field, err)
		}
	}

	raw := rawCollective(t, s)
	raw["events_blocked"], raw["locked"], raw["rev"] = true, true, int64(1)
	if _, err := coll.InsertOne(t.Context(), raw); err != nil {
		t.Errorf("insert with both flags set and a revision: %v", err)
	}
}

func TestCollectiveFlagsRoundTripAndAreStoredOnlyWhenSet(t *testing.T) {
	s := mongotest.New(t)
	repo := s.Repos().Collectives
	plain := domain.Collective{Name: "Plain", NameKey: "plain", Kind: domain.CollectiveKindColetivo,
		Status: domain.CollectiveStatusPending, CreatedAt: now, UpdatedAt: now}
	flagged := domain.Collective{Name: "Flagged", NameKey: "flagged", Kind: domain.CollectiveKindProdutora,
		Status: domain.CollectiveStatusPending, EventsBlocked: true, Locked: true, CreatedAt: now, UpdatedAt: now}
	for _, c := range []*domain.Collective{&plain, &flagged} {
		if err := repo.Create(t.Context(), c); err != nil {
			t.Fatalf("create %s: %v", c.Name, err)
		}
	}

	for _, c := range []domain.Collective{plain, flagged} {
		got, err := repo.ByID(t.Context(), c.ID)
		if err != nil {
			t.Fatalf("ByID %s: %v", c.Name, err)
		}
		if got.EventsBlocked != c.EventsBlocked || got.Locked != c.Locked {
			t.Errorf("%s: flags = (%v, %v), want (%v, %v)", c.Name, got.EventsBlocked, got.Locked, c.EventsBlocked, c.Locked)
		}
	}

	for name, want := range map[string]bool{"plain": false, "flagged": true} {
		var raw bson.M
		if err := s.DB().Collection("collectives").FindOne(t.Context(), bson.M{"name_key": name}).Decode(&raw); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		_, blocked := raw["events_blocked"]
		_, locked := raw["locked"]
		if blocked != want || locked != want {
			t.Errorf("%s: stored events_blocked = %v, locked = %v, want both %v", name, blocked, locked, want)
		}
		if _, rev := raw["rev"]; rev {
			t.Errorf("%s: Create stored a rev", name)
		}
	}
}

// rawAuditEntry is a document the audit_entries validator accepts.
func rawAuditEntry() bson.M {
	return bson.M{"_id": "entry-1", "at": now, "action": string(domain.AuditAccountApproved), "actor_id": "actor-1"}
}

func TestSchemaChecksAuditEntries(t *testing.T) {
	s := mongotest.New(t)
	coll := s.DB().Collection("audit_entries")

	full := rawAuditEntry()
	full["_id"] = "entry-0"
	for _, k := range []string{"actor_name", "user_id", "user_name", "collective_id", "collective_name", "detail", "reason"} {
		full[k] = "x"
	}
	if _, err := coll.InsertOne(t.Context(), full); err != nil {
		t.Errorf("insert a complete entry: %v", err)
	}
	if _, err := coll.InsertOne(t.Context(), rawAuditEntry()); err != nil {
		t.Errorf("insert an entry with only the required fields: %v", err)
	}

	bad := map[string]func(bson.M){
		"an ObjectID _id":     func(d bson.M) { d["_id"] = bson.NewObjectID() },
		"an unknown action":   func(d bson.M) { d["action"] = "account_deleted" },
		"no action":           func(d bson.M) { delete(d, "action") },
		"no at":               func(d bson.M) { delete(d, "at") },
		"no actor_id":         func(d bson.M) { delete(d, "actor_id") },
		"a string at":         func(d bson.M) { d["at"] = "2026-11-12" },
		"an unexpected field": func(d bson.M) { d["email"] = "ana@example.com" },
	}
	// The text fields hold strings, and so do the IDs of the snapshots.
	for _, k := range []string{"actor_id", "actor_name", "user_id", "user_name", "collective_id", "collective_name", "detail", "reason"} {
		bad["a non-string "+k] = func(d bson.M) { d[k] = 7 }
	}
	for name, mutate := range bad {
		d := rawAuditEntry()
		d["_id"] = "bad: " + name
		mutate(d)
		if _, err := coll.InsertOne(t.Context(), d); !isValidationFailure(err) {
			t.Errorf("insert with %s: error = %v, want a document validation failure", name, err)
		}
	}
}

var now = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

func newUser(name string) domain.User {
	return domain.User{
		Name:              name,
		Email:             name + "@example.com",
		Phone:             "11987654321",
		PasswordHash:      "hash-" + name,
		PasswordChangedAt: now,
		Status:            domain.UserStatusActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

// --- WithTx ---

// The contract of WithTx is in the suite; this checks how this backend does it.
func TestWithTxHandsFnAContextWithASession(t *testing.T) {
	s := mongotest.New(t)
	var seen bool
	err := s.WithTx(t.Context(), func(ctx context.Context, _ domain.Repos) error {
		seen = mongo.SessionFromContext(ctx) != nil
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	if !seen {
		t.Error("the context of fn carries no session")
	}
}
