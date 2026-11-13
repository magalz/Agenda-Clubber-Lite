package mongostore

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type memberships struct{ s *Store }

var _ domain.Memberships = memberships{}

func (r memberships) coll() *mongo.Collection { return r.s.db.Collection(colMemberships) }

func (r memberships) Create(ctx context.Context, m domain.Membership) error {
	if err := m.Validate(); err != nil {
		return err
	}
	userID, err := parseID(m.UserID)
	if err != nil {
		return err
	}
	collectiveID, err := parseID(m.CollectiveID)
	if err != nil {
		return err
	}
	adminBy, err := refID(m.AdminApprovedBy)
	if err != nil {
		return err
	}
	ownerBy, err := refID(m.OwnerApprovedBy)
	if err != nil {
		return err
	}
	for _, ref := range []struct {
		collection string
		id         *bson.ObjectID
	}{
		{colUsers, &userID},
		{colCollectives, &collectiveID},
		{colUsers, adminBy},
		{colUsers, ownerBy},
	} {
		if err := r.s.mustExist(ctx, ref.collection, ref.id); err != nil {
			return err
		}
	}
	if _, err := r.coll().InsertOne(ctx, membershipToDoc(m, userID, collectiveID, adminBy, ownerBy)); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrAlreadyMember
		}
		return wrap("create membership", err)
	}
	return nil
}

// ListByUser joins each membership with its collective and sorts by the
// collective's name key.
func (r memberships) ListByUser(ctx context.Context, userID string) ([]domain.MembershipView, error) {
	oid, err := parseID(userID)
	if err != nil {
		return nil, nil // an unknown user has no memberships
	}
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"user_id": oid}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colCollectives, "localField": "collective_id", "foreignField": "_id", "as": "collective",
		}}},
		{{Key: "$unwind", Value: "$collective"}},
		{{Key: "$sort", Value: bson.D{{Key: "collective.name_key", Value: 1}}}},
	})
	if err != nil {
		return nil, wrap("list memberships", err)
	}
	var docs []struct {
		Membership membershipDoc `bson:",inline"`
		Collective collectiveDoc `bson:"collective"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list memberships", err)
	}
	views := make([]domain.MembershipView, len(docs))
	for i, d := range docs {
		views[i] = domain.MembershipView{
			Membership:       d.Membership.membership(),
			CollectiveName:   d.Collective.Name,
			CollectiveKind:   domain.CollectiveKind(d.Collective.Kind),
			CollectiveStatus: domain.CollectiveStatus(d.Collective.Status),
		}
	}
	return views, nil
}

// OwnerEmails joins the approved owner memberships of the collective with their
// users and keeps the active ones.
func (r memberships) OwnerEmails(ctx context.Context, collectiveID string) ([]string, error) {
	oid, err := parseID(collectiveID)
	if err != nil {
		return nil, nil // an unknown collective has no owners
	}
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"collective_id": oid,
			"role":          string(domain.RoleOwner),
			"status":        string(domain.MembershipStatusApproved),
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colUsers, "localField": "user_id", "foreignField": "_id", "as": "user",
		}}},
		{{Key: "$unwind", Value: "$user"}},
		{{Key: "$match", Value: bson.M{"user.status": string(domain.UserStatusActive)}}},
		{{Key: "$sort", Value: bson.D{{Key: "user.email", Value: 1}}}},
		{{Key: "$project", Value: bson.M{"_id": 0, "email": "$user.email"}}},
	})
	if err != nil {
		return nil, wrap("list owner emails", err)
	}
	var docs []struct {
		Email string `bson:"email"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list owner emails", err)
	}
	emails := make([]string, len(docs))
	for i, d := range docs {
		emails[i] = d.Email
	}
	return emails, nil
}

// UserCanCreateEvents is true for an active user with an approved owner
// membership in an approved collective.
func (r memberships) UserCanCreateEvents(ctx context.Context, userID string) (bool, error) {
	oid, err := parseID(userID)
	if err != nil {
		return false, nil // an unknown user cannot
	}
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"user_id": oid,
			"role":    string(domain.RoleOwner),
			"status":  string(domain.MembershipStatusApproved),
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colCollectives, "localField": "collective_id", "foreignField": "_id", "as": "collective",
		}}},
		{{Key: "$match", Value: bson.M{"collective.status": string(domain.CollectiveStatusApproved)}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colUsers, "localField": "user_id", "foreignField": "_id", "as": "user",
		}}},
		{{Key: "$match", Value: bson.M{"user.status": string(domain.UserStatusActive)}}},
		{{Key: "$limit", Value: 1}},
	})
	if err != nil {
		return false, wrap("check event creation rights", err)
	}
	defer cur.Close(ctx)
	ok := cur.Next(ctx)
	if err := cur.Err(); err != nil {
		return false, wrap("check event creation rights", err)
	}
	return ok, nil
}

// Get is a stub until T3.4a implements it.
func (r memberships) Get(ctx context.Context, userID, collectiveID string) (domain.Membership, error) {
	return domain.Membership{}, errNotImplemented
}

// Update is a stub until T3.4a implements it.
func (r memberships) Update(ctx context.Context, m domain.Membership) error {
	return errNotImplemented
}

// Delete is a stub until T3.4a implements it.
func (r memberships) Delete(ctx context.Context, userID, collectiveID string) error {
	return errNotImplemented
}

// ListByCollective is a stub until T3.4b implements it.
func (r memberships) ListByCollective(ctx context.Context, collectiveID string) ([]domain.MemberView, error) {
	return nil, errNotImplemented
}

// ListAwaitingAdmin is a stub until T3.4b implements it.
func (r memberships) ListAwaitingAdmin(ctx context.Context, limit int) ([]domain.MembershipView, error) {
	return nil, errNotImplemented
}
