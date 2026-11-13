// Package mongotest gives a test an empty MongoDB database of its own, behind a
// mongostore.Store. The tests need a single-node replica set (transactions need
// one); MONGO_TEST_URI names it, for example
// mongodb://127.0.0.1:27017/?directConnection=true.
//
// The database is named acltest_ and 16 random hex digits, so tests that share
// one server do not meet. The helper drops that database, and only that one,
// when the test ends.
package mongotest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/store/mongostore"
)

// cleanupTimeout bounds the drop of the database when the test ends.
const cleanupTimeout = 30 * time.Second

// New returns a store on a new, empty database with the schema in place. Without
// MONGO_TEST_URI the test is skipped, except when CI is set: there a missing
// server must fail the build, not hide the tests.
func New(t *testing.T) *mongostore.Store {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("MONGO_TEST_URI is not set, and CI is: the database tests must run in CI")
		}
		t.Skip("MONGO_TEST_URI not set: start a single-node replica set and set it")
	}

	var suffix [8]byte
	_, _ = rand.Read(suffix[:]) // never fails: see crypto/rand.Read
	name := "acltest_" + hex.EncodeToString(suffix[:])

	// Registered before anything is created, so a failed setup is cleaned up too.
	// It does not use the store: a test may have closed it, and the drop must
	// still happen.
	t.Cleanup(func() { dropDatabase(t, uri, name) })

	s, err := mongostore.Open(t.Context(), uri, name)
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Errorf("close the test store: %v", err)
		}
	})
	if err := s.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("create the schema: %v", err)
	}
	return s
}

// dropDatabase drops the database name, which New created.
func dropDatabase(t *testing.T, uri, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Errorf("drop %s: cannot connect", name) // the error may echo the URI
		return
	}
	defer func() { _ = client.Disconnect(ctx) }()
	if err := client.Database(name).Drop(ctx); err != nil {
		t.Errorf("drop %s: %v", name, err)
	}
}
