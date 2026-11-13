package mongostore

import "go.mongodb.org/mongo-driver/v2/mongo"

// What the external tests (package mongostore_test) may reach.

var RequireReplicaSet = requireReplicaSet

// DB is the database of the store, for tests that look at the collections.
func (s *Store) DB() *mongo.Database { return s.db }
