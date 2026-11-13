package mongostore

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type tokens struct{ s *Store }

var _ domain.Tokens = tokens{}

func (r tokens) coll() *mongo.Collection { return r.s.db.Collection(colTokens) }

// Create retires the unused tokens of the same user and purpose and stores t in
// one transaction (the caller's, when it has one): when the insert fails, the
// retirement is undone with it. A token with the hash of another one is
// domain.ErrConflict.
func (r tokens) Create(ctx context.Context, t *domain.Token) error {
	if err := t.Validate(); err != nil {
		return err
	}
	userID, err := parseID(t.UserID)
	if err != nil {
		return err
	}
	oid := bson.NewObjectID()
	err = r.s.inTx(ctx, func(ctx context.Context) error {
		if err := r.s.mustExist(ctx, colUsers, &userID); err != nil {
			return err
		}
		_, err := r.coll().UpdateMany(ctx,
			bson.M{"user_id": userID, "purpose": string(t.Purpose), "used_at": nil},
			bson.M{"$set": bson.M{"used_at": t.CreatedAt}})
		if err != nil {
			return wrap("retire the tokens of a user", err)
		}
		if _, err := r.coll().InsertOne(ctx, tokenToDoc(*t, oid, userID)); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return domain.ErrConflict
			}
			return wrap("create token", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	t.ID = oid.Hex()
	return nil
}

// Consume is one find-and-update, so of several calls for the same token exactly
// one matches it.
func (r tokens) Consume(ctx context.Context, purpose domain.TokenPurpose, hash []byte, now time.Time) (domain.Token, error) {
	var d tokenDoc
	err := r.coll().FindOneAndUpdate(ctx,
		bson.M{
			"purpose":    string(purpose),
			"hash":       hash,
			"used_at":    nil,
			"expires_at": bson.M{"$gt": now},
		},
		bson.M{"$set": bson.M{"used_at": now}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Token{}, domain.ErrNotFound
		}
		return domain.Token{}, wrap("consume token", err)
	}
	return d.token(), nil
}

// DeleteExpired removes the tokens that are used or have ExpiresAt up to now.
func (r tokens) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	res, err := r.coll().DeleteMany(ctx, bson.M{"$or": bson.A{
		bson.M{"used_at": bson.M{"$ne": nil}},
		bson.M{"expires_at": bson.M{"$lte": now}},
	}})
	if err != nil {
		return 0, wrap("delete expired tokens", err)
	}
	return int(res.DeletedCount), nil
}
