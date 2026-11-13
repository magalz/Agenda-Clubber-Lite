package mongostore_test

import (
	"context"
	"crypto/sha256"
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

// The contract suite runs against a real MongoDB; without MONGO_TEST_URI these
// tests skip (and fail when CI is set). Outbox, Tx and Run join them with the
// outbox repository.

func newStore(t *testing.T) domain.Store { return mongotest.New(t) }

func TestUsers(t *testing.T)       { storetest.RunUsers(t, newStore) }
func TestTokens(t *testing.T)      { storetest.RunTokens(t, newStore) }
func TestSessions(t *testing.T)    { storetest.RunSessions(t, newStore) }
func TestCollectives(t *testing.T) { storetest.RunCollectives(t, newStore) }
func TestMemberships(t *testing.T) { storetest.RunMemberships(t, newStore) }

// --- Open ---

// passwordMarker stands for a password inside a URI: it must never show up in
// an error.
const passwordMarker = "PWMARKER-7f3a"

func wantSafeError(t *testing.T, err error, uri string) {
	t.Helper()
	if err == nil {
		t.Fatal("Open succeeded, want an error")
	}
	msg := err.Error()
	for _, leak := range []string{passwordMarker, "user:", uri} {
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

func TestCloseIsIdempotent(t *testing.T) {
	s := mongotest.New(t)
	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	for i := range 2 {
		if err := s.Close(t.Context()); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}
	if err := s.Ping(t.Context()); err == nil {
		t.Error("Ping after Close succeeded")
	}
}

// --- schema ---

var collections = []string{"users", "tokens", "sessions", "collectives", "memberships", "outbox"}

// wantIndexes are the index names of each collection. There is no TTL index:
// the contract says a backend never expires data on its own.
var wantIndexes = map[string][]string{
	"users":       {"_id_", "email_unique"},
	"tokens":      {"_id_", "hash_unique", "user_id_purpose"},
	"sessions":    {"_id_", "user_id"},
	"collectives": {"_id_", "name_key_unique", "status_kind_name_key"},
	"memberships": {"_id_", "collective_id_status", "user_id_collective_id_unique"},
	"outbox":      {"_id_", "sent_at_gave_up_next_attempt_at"},
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

// --- values that start with $ ---

// A caller's string that starts with $ is a field path inside an aggregation
// update unless it is quoted, so the store must keep it as plain text.
func TestDollarValuesAreStoredAsText(t *testing.T) {
	s := mongotest.New(t)
	r := s.Repos()
	ctx := t.Context()

	u := newUser("ana")
	u.Name = "$gt"
	u.PasswordHash = "$password_hash"
	if err := r.Users.Create(ctx, &u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	for _, hash := range []string{"$$ROOT", "$$REMOVE", "$name", "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"} {
		if err := r.Users.ChangePassword(ctx, u.ID, hash, now); err != nil {
			t.Fatalf("ChangePassword(%q): %v", hash, err)
		}
		got, err := r.Users.RecentPasswordHashes(ctx, u.ID)
		if err != nil {
			t.Fatalf("RecentPasswordHashes: %v", err)
		}
		if len(got) != 2 || got[0] != hash {
			t.Errorf("RecentPasswordHashes after ChangePassword(%q) = %q", hash, got)
		}
	}
	stored, err := r.Users.ByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if stored.Name != "$gt" {
		t.Errorf("Name = %q, want $gt", stored.Name)
	}
	if stored.PasswordHash != "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA" {
		t.Errorf("PasswordHash = %q", stored.PasswordHash)
	}

	// The same value as a lookup key finds nothing: it is a string, not an operator.
	if _, err := r.Users.ByEmail(ctx, "$ne"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByEmail($ne) error = %v, want ErrNotFound", err)
	}
	if _, err := r.Collectives.ByNameKey(ctx, "$ne"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByNameKey($ne) error = %v, want ErrNotFound", err)
	}
}

// --- WithTx ---

var (
	now     = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
)

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

func newToken(userID, tag string) domain.Token {
	return domain.Token{
		UserID:    userID,
		Purpose:   domain.TokenPurposeResetPassword,
		Hash:      sha256Of(tag),
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	}
}

func sha256Of(tag string) []byte {
	h := sha256.Sum256([]byte(tag))
	return h[:]
}

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

// A method that is atomic by itself (the cascade of Users.Delete, the
// retirement of tokens in Tokens.Create) joins the transaction of its caller,
// so the caller's rollback undoes it. A transaction of its own would have
// committed it.
func TestSelfAtomicCallsJoinTheCallersTransaction(t *testing.T) {
	s := mongotest.New(t)
	r := s.Repos()
	ctx := t.Context()

	ana := newUser("ana")
	if err := r.Users.Create(ctx, &ana); err != nil {
		t.Fatalf("create user: %v", err)
	}
	old := newToken(ana.ID, "old")
	if err := r.Tokens.Create(ctx, &old); err != nil {
		t.Fatalf("create token: %v", err)
	}

	err := s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		fresh := newToken(ana.ID, "fresh")
		if err := r.Tokens.Create(ctx, &fresh); err != nil { // retires "old"
			return err
		}
		if err := r.Users.Delete(ctx, ana.ID); err != nil { // cascades
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("WithTx error = %v, want errBoom", err)
	}
	if _, err := r.Users.ByID(ctx, ana.ID); err != nil {
		t.Errorf("the rolled-back Users.Delete removed the user: %v", err)
	}
	if _, err := r.Tokens.Consume(ctx, old.Purpose, old.Hash, now.Add(time.Minute)); err != nil {
		t.Errorf("the rolled-back Tokens.Create retired the old token: %v", err)
	}
	if _, err := r.Tokens.Consume(ctx, old.Purpose, sha256Of("fresh"), now.Add(time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the rolled-back Tokens.Create left the new token: error = %v", err)
	}
}

func TestWithTxCommitsWhatFnWrote(t *testing.T) {
	s := mongotest.New(t)
	ctx := t.Context()
	var id string
	err := s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		u := newUser("ana")
		if err := r.Users.Create(ctx, &u); err != nil {
			return err
		}
		id = u.ID
		tok := newToken(u.ID, "t1")
		return r.Tokens.Create(ctx, &tok)
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	r := s.Repos()
	if _, err := r.Users.ByID(ctx, id); err != nil {
		t.Errorf("the committed user: %v", err)
	}
	if _, err := r.Tokens.Consume(ctx, domain.TokenPurposeResetPassword, sha256Of("t1"), now.Add(time.Minute)); err != nil {
		t.Errorf("the committed token: %v", err)
	}
}
