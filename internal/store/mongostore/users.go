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
	return r.s.notMatched(ctx, colUsers, oid)
}

// ChangePassword is one update with an aggregation pipeline, so the hash that
// leaves is the one that was current when the update ran. The new hash is quoted
// with $literal: a string that starts with $ is a field path in a pipeline. The
// same update clears the lock and the password-reminder marker.
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
		{{Key: "$unset", Value: bson.A{"locked_until", "password_reminded_at"}}},
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
// clears every reference to the user (the creator and editor of the events
// included: the events stay), in one transaction (the caller's, when it has
// one).
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
			{colEvents, "created_by"},
			{colEvents, "updated_by"},
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

// ListByStatus clamps limit itself: MongoDB reads a limit of 0 as "no limit" and
// a negative one as its absolute value. The status_created_at index finds the
// status in created_at order; _id breaks the ties (the hex form of an ObjectID
// sorts as its bytes do) and is sorted in memory, over at most ListLimitMax rows.
func (r users) ListByStatus(ctx context.Context, status domain.UserStatus, limit int) ([]domain.User, error) {
	if !status.Valid() {
		return nil, domain.FieldErrors{"status": domain.MsgInvalid}
	}
	limit = min(max(limit, 1), domain.ListLimitMax)
	cur, err := r.coll().Find(ctx, bson.M{"status": string(status)},
		options.Find().SetSort(bson.D{key("created_at"), key("_id")}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrap("list users by status", err)
	}
	var docs []userDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list users by status", err)
	}
	list := make([]domain.User, len(docs))
	for i, d := range docs {
		list[i] = d.user()
	}
	return list, nil
}

// Approve is one update filtered on the status, so of several calls on one user
// exactly one matches. The approver is checked first and nothing changes when it
// is missing; an empty by names nobody, so it is ErrNotFound like any ID the
// store never issued. A user whose status and approver are both wrong answers
// ErrNotFound, because the references are checked before the state. The check of
// the approver is not isolated from a concurrent Delete of it, the same gap as a
// Create (ACL-21).
func (r users) Approve(ctx context.Context, id, by string, now time.Time) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	approver, err := parseID(by)
	if err != nil {
		return err
	}
	if err := r.s.mustExist(ctx, colUsers, &approver); err != nil {
		return err
	}
	res, err := r.coll().UpdateOne(ctx,
		bson.M{"_id": oid, "status": string(domain.UserStatusPendingApproval)},
		bson.M{"$set": bson.M{
			"status":      string(domain.UserStatusActive),
			"approved_by": approver,
			"approved_at": now,
			"updated_at":  now,
		}})
	if err != nil {
		return wrap("approve user", err)
	}
	if res.MatchedCount > 0 {
		return nil
	}
	return r.s.notMatched(ctx, colUsers, oid)
}

// UpdateProfile writes only the fields the service validated; UpdatedAt moves
// with them, as it does on every other mutating Users method.
func (r users) UpdateProfile(ctx context.Context, id, name, phone string, phoneIsWhatsApp bool, now time.Time) error {
	return r.update(ctx, id, bson.M{"$set": bson.M{
		"name":              name,
		"phone":             phone,
		"phone_is_whatsapp": phoneIsWhatsApp,
		"updated_at":        now,
	}})
}

// SetEmail writes the normalized address. The unique index on email rejects a
// value another user holds; writing the value the account already holds is not
// a duplicate key, so a no-op self-update succeeds.
func (r users) SetEmail(ctx context.Context, id, email string, now time.Time) error {
	normalized, err := domain.NormalizeEmail(email)
	if err != nil {
		return domain.ErrNotFound
	}
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	res, err := r.coll().UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{
		"email":      normalized,
		"updated_at": now,
	}})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrEmailTaken
		}
		return wrap("set user email", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// MarkPasswordReminded moves UpdatedAt too, like every other mutating Users
// method.
func (r users) MarkPasswordReminded(ctx context.Context, id string, now time.Time) error {
	return r.update(ctx, id, bson.M{"$set": bson.M{
		"password_reminded_at": now,
		"updated_at":           now,
	}})
}

// ListByStatusCreatedBefore uses the status_created_at index of ListByStatus
// with a strictly earlier bound on created_at; _id breaks the ties.
func (r users) ListByStatusCreatedBefore(ctx context.Context, status domain.UserStatus, cutoff time.Time, limit int) ([]domain.User, error) {
	if !status.Valid() {
		return nil, domain.FieldErrors{"status": domain.MsgInvalid}
	}
	limit = min(max(limit, 1), domain.ListLimitMax)
	cur, err := r.coll().Find(ctx,
		bson.M{"status": string(status), "created_at": bson.M{"$lt": cutoff}},
		options.Find().SetSort(bson.D{key("created_at"), key("_id")}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrap("list users by status and creation time", err)
	}
	var docs []userDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list users by status and creation time", err)
	}
	list := make([]domain.User, len(docs))
	for i, d := range docs {
		list[i] = d.user()
	}
	return list, nil
}

// ListActivePasswordChangedBetween uses a half-open window on
// password_changed_at, sorted first by that time and then by _id.
func (r users) ListActivePasswordChangedBetween(ctx context.Context, from, to time.Time, limit int) ([]domain.User, error) {
	limit = min(max(limit, 1), domain.ListLimitMax)
	cur, err := r.coll().Find(ctx,
		bson.M{
			"status":              string(domain.UserStatusActive),
			"password_changed_at": bson.M{"$gte": from, "$lt": to},
		},
		options.Find().SetSort(bson.D{key("password_changed_at"), key("_id")}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrap("list active users by password change time", err)
	}
	var docs []userDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list active users by password change time", err)
	}
	list := make([]domain.User, len(docs))
	for i, d := range docs {
		list[i] = d.user()
	}
	return list, nil
}
