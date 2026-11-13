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

type users struct{ s *Store }

var _ domain.Users = users{}

func (r users) coll() *mongo.Collection { return r.s.db.Collection(colUsers) }

func (r users) Create(ctx context.Context, u *domain.User) error {
	if err := u.Validate(); err != nil {
		return err
	}
	approvedBy, err := refID(u.ApprovedBy)
	if err != nil {
		return err
	}
	if err := r.s.mustExist(ctx, colUsers, approvedBy); err != nil {
		return err
	}
	oid := bson.NewObjectID()
	if _, err := r.coll().InsertOne(ctx, userToDoc(*u, oid, approvedBy)); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrEmailTaken
		}
		return wrap("create user", err)
	}
	u.ID = oid.Hex()
	return nil
}

func (r users) ByID(ctx context.Context, id string) (domain.User, error) {
	oid, err := parseID(id)
	if err != nil {
		return domain.User{}, err
	}
	return r.find(ctx, bson.M{"_id": oid})
}

func (r users) ByEmail(ctx context.Context, email string) (domain.User, error) {
	normalized, err := domain.NormalizeEmail(email)
	if err != nil {
		return domain.User{}, domain.ErrNotFound
	}
	return r.find(ctx, bson.M{"email": normalized})
}

// find returns the user that matches filter.
func (r users) find(ctx context.Context, filter bson.M) (domain.User, error) {
	var d userDoc
	if err := r.coll().FindOne(ctx, filter).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.User{}, domain.ErrNotFound
		}
		return domain.User{}, wrap("read user", err)
	}
	return d.user(), nil
}

// update applies update to the user with this ID; a user that does not exist is
// domain.ErrNotFound. update is a document or an aggregation pipeline.
func (r users) update(ctx context.Context, id string, update any) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	return updateOne(ctx, r.coll(), "update user", oid, update)
}

func (r users) SetStatus(ctx context.Context, id string, status domain.UserStatus, now time.Time) error {
	if !status.Valid() {
		return domain.FieldErrors{"status": domain.MsgInvalid}
	}
	return r.update(ctx, id, bson.M{"$set": bson.M{"status": string(status), "updated_at": now}})
}

func (r users) MarkEmailVerified(ctx context.Context, id string, now time.Time) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	res, err := r.coll().UpdateOne(ctx,
		bson.M{"_id": oid, "status": string(domain.UserStatusPendingEmail)},
		bson.M{"$set": bson.M{
			"email_verified_at": now,
			"updated_at":        now,
			"status":            string(domain.UserStatusPendingApproval),
		}})
	if err != nil {
		return wrap("verify user email", err)
	}
	if res.MatchedCount > 0 {
		return nil
	}
	// Nothing matched: either there is no such user, or it is not pending_email.
	found, err := r.s.exists(ctx, colUsers, oid)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrNotFound
	}
	return domain.ErrConflict
}

// ChangePassword is one update with an aggregation pipeline, so the hash that
// leaves is the one that was current when the update ran. The new hash is quoted
// with $literal: a string that starts with $ is a field path in a pipeline.
func (r users) ChangePassword(ctx context.Context, id, newHash string, now time.Time) error {
	if newHash == "" {
		return domain.FieldErrors{"password_hash": domain.MsgRequired}
	}
	return r.update(ctx, id, mongo.Pipeline{
		{{Key: "$set", Value: bson.M{
			"password_history":    bson.A{"$password_hash"},
			"password_hash":       bson.M{"$literal": newHash},
			"password_changed_at": now,
			"updated_at":          now,
			"failed_logins":       0,
		}}},
		{{Key: "$unset", Value: "locked_until"}},
	})
}

func (r users) RecentPasswordHashes(ctx context.Context, id string) ([]string, error) {
	oid, err := parseID(id)
	if err != nil {
		return nil, err
	}
	var d userDoc
	err = r.coll().FindOne(ctx, bson.M{"_id": oid},
		options.FindOne().SetProjection(bson.M{"password_hash": 1, "password_history": 1})).Decode(&d)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrNotFound
		}
		return nil, wrap("read password hashes", err)
	}
	return append([]string{d.PasswordHash}, d.PasswordHistory...), nil
}

// RecordLoginFailure is one atomic update: the count, the lock and the reset of
// the count happen in a single pipeline, so concurrent failures never lose one.
// A lock leaves the count at 0, and an unlocked failure leaves it at 1 or more,
// which is how the caller learns which of the two happened.
func (r users) RecordLoginFailure(ctx context.Context, id string, now time.Time, maxFailures int, lockFor time.Duration) (bool, error) {
	oid, err := parseID(id)
	if err != nil {
		return false, err
	}
	pipeline := mongo.Pipeline{
		{{Key: "$set", Value: bson.M{
			"failed_logins": bson.M{"$add": bson.A{bson.M{"$ifNull": bson.A{"$failed_logins", 0}}, 1}},
		}}},
		{{Key: "$set", Value: bson.M{"lock_now": bson.M{"$gte": bson.A{"$failed_logins", maxFailures}}}}},
		{{Key: "$set", Value: bson.M{
			"locked_until":  bson.M{"$cond": bson.A{"$lock_now", now.Add(lockFor), "$locked_until"}},
			"failed_logins": bson.M{"$cond": bson.A{"$lock_now", 0, "$failed_logins"}},
		}}},
		{{Key: "$unset", Value: "lock_now"}},
	}
	var after struct {
		FailedLogins int `bson:"failed_logins"`
	}
	err = r.coll().FindOneAndUpdate(ctx, bson.M{"_id": oid}, pipeline,
		options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.M{"failed_logins": 1})).Decode(&after)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, domain.ErrNotFound
		}
		return false, wrap("record login failure", err)
	}
	return after.FailedLogins == 0, nil
}

func (r users) ResetLoginFailures(ctx context.Context, id string) error {
	return r.update(ctx, id, bson.M{"$set": bson.M{"failed_logins": 0}})
}

func (r users) ListAdminEmails(ctx context.Context) ([]string, error) {
	cur, err := r.coll().Find(ctx,
		bson.M{"is_admin": true, "status": string(domain.UserStatusActive)},
		options.Find().SetSort(bson.D{{Key: "email", Value: 1}}).SetProjection(bson.M{"email": 1}))
	if err != nil {
		return nil, wrap("list admin emails", err)
	}
	var docs []struct {
		Email string `bson:"email"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list admin emails", err)
	}
	emails := make([]string, len(docs))
	for i, d := range docs {
		emails[i] = d.Email
	}
	return emails, nil
}

// Delete removes the user, the user's sessions, tokens and memberships, and
// clears every reference to the user, in one transaction (the caller's, when it
// has one).
func (r users) Delete(ctx context.Context, id string) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	return r.s.inTx(ctx, func(ctx context.Context) error {
		res, err := r.coll().DeleteOne(ctx, bson.M{"_id": oid})
		if err != nil {
			return wrap("delete user", err)
		}
		if res.DeletedCount == 0 {
			return domain.ErrNotFound
		}
		for _, col := range []string{colSessions, colTokens, colMemberships} {
			if _, err := r.s.db.Collection(col).DeleteMany(ctx, bson.M{"user_id": oid}); err != nil {
				return wrap("delete the "+col+" of a user", err)
			}
		}
		for _, ref := range []struct{ collection, field string }{
			{colUsers, "approved_by"},
			{colCollectives, "created_by"},
			{colCollectives, "approved_by"},
			{colMemberships, "admin_approved_by"},
			{colMemberships, "owner_approved_by"},
		} {
			if err := r.s.clearRef(ctx, ref.collection, ref.field, oid); err != nil {
				return err
			}
		}
		return nil
	})
}

// clearRef removes field from every document of the collection where it names
// the user: an empty reference is a field that is not there.
func (s *Store) clearRef(ctx context.Context, collection, field string, id bson.ObjectID) error {
	_, err := s.db.Collection(collection).UpdateMany(ctx, bson.M{field: id}, bson.M{"$unset": bson.M{field: ""}})
	if err != nil {
		return wrap("clear "+collection+"."+field, err)
	}
	return nil
}

// ListByStatus is a stub until T3.2 implements it.
func (r users) ListByStatus(ctx context.Context, status domain.UserStatus, limit int) ([]domain.User, error) {
	return nil, errNotImplemented
}

// Approve is a stub until T3.2 implements it.
func (r users) Approve(ctx context.Context, id, by string, now time.Time) error {
	return errNotImplemented
}
