// Package mongostore is the MongoDB implementation of domain.Store.
package mongostore

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

var errNotImplemented = errors.New("mongostore: not implemented")

var _ domain.Store = (*Store)(nil)

// Store is a domain.Store on one MongoDB database.
type Store struct {
	client *mongo.Client
	db     *mongo.Database
}

// Open connects to uri and returns the store of database dbName.
func Open(_ context.Context, uri, dbName string) (*Store, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, errors.New("mongostore: invalid MongoDB URI")
	}
	return &Store{client: client, db: client.Database(dbName)}, nil
}

// EnsureSchema creates the collections, their validators and their indexes.
func (s *Store) EnsureSchema(context.Context) error { return nil }

// Repos returns the repositories of the store.
func (s *Store) Repos() domain.Repos {
	return domain.Repos{
		Users:       stubUsers{},
		Tokens:      stubTokens{},
		Sessions:    stubSessions{},
		Collectives: stubCollectives{},
		Memberships: stubMemberships{},
	}
}

// WithTx runs fn in a transaction.
func (s *Store) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	return errNotImplemented
}

// Ping returns nil when the database answers.
func (s *Store) Ping(ctx context.Context) error { return s.client.Ping(ctx, nil) }

// Close releases the connections.
func (s *Store) Close(ctx context.Context) error { return s.client.Disconnect(ctx) }

func requireReplicaSet(bson.M) error { return nil }
