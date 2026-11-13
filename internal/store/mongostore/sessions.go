package mongostore

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type sessions struct{ s *Store }

var _ domain.Sessions = sessions{}

func (r sessions) coll() *mongo.Collection { return r.s.db.Collection(colSessions) }

func (r sessions) Create(ctx context.Context, s domain.Session) error {
	if err := s.Validate(); err != nil {
		return err
	}
	userID, err := parseID(s.UserID)
	if err != nil {
		return err
	}
	if err := r.s.mustExist(ctx, colUsers, &userID); err != nil {
		return err
	}
	if _, err := r.coll().InsertOne(ctx, sessionToDoc(s, userID)); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrConflict
		}
		return wrap("create session", err)
	}
	return nil
}

// Get returns the session even when it has expired: the caller checks ExpiresAt.
func (r sessions) Get(ctx context.Context, hash []byte) (domain.Session, error) {
	var d sessionDoc
	if err := r.coll().FindOne(ctx, bson.M{"_id": hash}).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Session{}, domain.ErrNotFound
		}
		return domain.Session{}, wrap("read session", err)
	}
	return d.session(), nil
}

func (r sessions) Touch(ctx context.Context, hash []byte, lastSeen, expiresAt time.Time) error {
	res, err := r.coll().UpdateOne(ctx, bson.M{"_id": hash},
		bson.M{"$set": bson.M{"last_seen_at": lastSeen, "expires_at": expiresAt}})
	if err != nil {
		return wrap("touch session", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r sessions) Delete(ctx context.Context, hash []byte) error {
	if _, err := r.coll().DeleteOne(ctx, bson.M{"_id": hash}); err != nil {
		return wrap("delete session", err)
	}
	return nil
}

func (r sessions) DeleteForUser(ctx context.Context, userID string, keep []byte) error {
	oid, err := parseID(userID)
	if err != nil {
		return nil // an unknown user has no sessions
	}
	filter := bson.M{"user_id": oid}
	if len(keep) > 0 {
		filter["_id"] = bson.M{"$ne": keep}
	}
	if _, err := r.coll().DeleteMany(ctx, filter); err != nil {
		return wrap("delete the sessions of a user", err)
	}
	return nil
}

// DeleteExpired removes the sessions with ExpiresAt <= now. Nothing else removes
// them: the store has no TTL index.
func (r sessions) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	res, err := r.coll().DeleteMany(ctx, bson.M{"expires_at": bson.M{"$lte": now}})
	if err != nil {
		return 0, wrap("delete expired sessions", err)
	}
	return int(res.DeletedCount), nil
}
