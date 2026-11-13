// Package mongostore is the MongoDB implementation of domain.Store. It needs a
// single-node replica set, because transactions need one, and it works through
// the official driver with no ODM: each collection has a document struct (see
// docs.go), a validator and its indexes (see schema.go).
//
// An error from Open never carries the connection string, which may hold a
// password.
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// serverSelectionTimeout bounds how long a call waits for a usable server, so an
// unreachable database fails a request in seconds and not after the driver's
// default of 30.
const serverSelectionTimeout = 10 * time.Second

var _ domain.Store = (*Store)(nil)

// errNotImplemented is what the methods that phase 3 adds return until the task
// that owns each one replaces its stub.
var errNotImplemented = errors.New("mongostore: not implemented")

// Store is a domain.Store on one MongoDB database.
type Store struct {
	client *mongo.Client
	db     *mongo.Database

	mu     sync.Mutex
	closed bool
}

// Open connects to uri, checks that the server is a replica set member and
// returns the store of database dbName. It does not create anything: call
// EnsureSchema for that.
func Open(ctx context.Context, uri, dbName string) (*Store, error) {
	if dbName == "" {
		return nil, errors.New("mongostore: the database name is empty")
	}
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(serverSelectionTimeout).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		// The driver's message may echo the URI, password included.
		return nil, errors.New("mongostore: the MongoDB URI is invalid")
	}
	s := &Store{client: client, db: client.Database(dbName)}
	if err := s.check(ctx); err != nil {
		_ = client.Disconnect(context.WithoutCancel(ctx))
		return nil, err
	}
	return s, nil
}

// check pings the server and requires a replica set member.
func (s *Store) check(ctx context.Context) error {
	if err := s.client.Ping(ctx, readpref.Primary()); err != nil {
		// Not wrapped: the driver's message names the server and may echo the URI.
		return errors.New("mongostore: cannot reach the MongoDB server")
	}
	var hello bson.M
	if err := s.db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return errors.New("mongostore: cannot ask the MongoDB server what it is")
	}
	return requireReplicaSet(hello)
}

// requireReplicaSet reads the answer to the hello command: a server that is not
// a replica set member has no setName, and cannot run transactions.
func requireReplicaSet(hello bson.M) error {
	if name, ok := hello["setName"].(string); !ok || name == "" {
		return errors.New("mongostore: the server is not a replica set member; " +
			"transactions need a single-node replica set (start mongod with --replSet and run rs.initiate())")
	}
	return nil
}

// Repos returns the repositories of the store. They use the transaction in the
// context of a call, if there is one, so the same values serve WithTx.
func (s *Store) Repos() domain.Repos {
	return domain.Repos{
		Users:        users{s},
		Tokens:       tokens{s},
		Sessions:     sessions{s},
		Collectives:  collectives{s},
		Memberships:  memberships{s},
		Outbox:       outbox{s},
		AuditEntries: auditEntries{s},
	}
}

// WithTx runs fn in a transaction with majority read and write concern; see
// domain.Store. The context handed to fn carries the session, so every call on r
// joins the transaction. The driver retries fn when the transaction fails for a
// transient reason, as the interface warns. A call made with a context that
// already carries a transaction (WithTx inside fn) joins that one.
func (s *Store) WithTx(ctx context.Context, fn func(ctx context.Context, r domain.Repos) error) error {
	if mongo.SessionFromContext(ctx) != nil {
		return fn(ctx, s.Repos())
	}
	sess, err := s.client.StartSession()
	if err != nil {
		return fmt.Errorf("mongostore: start a session: %w", err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	opts := options.Transaction().
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	_, err = sess.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		return nil, fn(ctx, s.Repos())
	}, opts)
	return err
}

// inTx runs fn in the transaction of ctx or, when there is none, in a new one:
// a method that must be atomic by itself (several writes) calls it, and so also
// takes part in the transaction of a caller that has one.
func (s *Store) inTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.WithTx(ctx, func(ctx context.Context, _ domain.Repos) error { return fn(ctx) })
}

// Ping returns nil when the database answers.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx, readpref.Primary()); err != nil {
		return fmt.Errorf("mongostore: ping: %w", err)
	}
	return nil
}

// Close releases the connections. It is idempotent: a second call returns nil.
func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.client.Disconnect(ctx)
}

// exists reports whether the collection has a document with this _id.
func (s *Store) exists(ctx context.Context, collection string, id bson.ObjectID) (bool, error) {
	n, err := s.db.Collection(collection).CountDocuments(ctx, bson.M{"_id": id}, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("mongostore: look up in %s: %w", collection, err)
	}
	return n > 0, nil
}

// mustExist returns domain.ErrNotFound when the reference id (an optional
// reference when empty) names nothing in the collection.
func (s *Store) mustExist(ctx context.Context, collection string, id *bson.ObjectID) error {
	if id == nil {
		return nil
	}
	ok, err := s.exists(ctx, collection, *id)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}

// updateOne applies update (a document or an aggregation pipeline) to the
// document of coll whose _id is id; an id that names none is domain.ErrNotFound.
// op names the operation in the error.
func updateOne(ctx context.Context, coll *mongo.Collection, op string, id, update any) error {
	res, err := coll.UpdateOne(ctx, bson.M{"_id": id}, update)
	if err != nil {
		return wrap(op, err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// wrap adds the operation to a driver error and keeps the cause, so the driver's
// error labels still reach WithTransaction.
func wrap(op string, err error) error {
	return fmt.Errorf("mongostore: %s: %w", op, err)
}
