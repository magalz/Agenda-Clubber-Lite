package mongostore

import (
	"context"
	"errors"

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
	return membershipViews(ctx, cur, "list memberships")
}

// membershipViews reads the cursor of a pipeline that joined each membership with
// its collective, as "collective".
func membershipViews(ctx context.Context, cur *mongo.Cursor, what string) ([]domain.MembershipView, error) {
	var docs []struct {
		Membership membershipDoc `bson:",inline"`
		Collective collectiveDoc `bson:"collective"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap(what, err)
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
// membership in an approved collective whose events are not blocked. The
// events_blocked field is stored only when true, so a missing one is not blocked.
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
		{{Key: "$match", Value: bson.M{
			"collective.status":         string(domain.CollectiveStatusApproved),
			"collective.events_blocked": bson.M{"$ne": true},
		}}},
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

// pairFilter selects the membership of a user in a collective. An ID the store
// never issued names no membership, so it is domain.ErrNotFound.
func pairFilter(userID, collectiveID string) (bson.M, error) {
	uid, err := parseID(userID)
	if err != nil {
		return nil, err
	}
	cid, err := parseID(collectiveID)
	if err != nil {
		return nil, err
	}
	return bson.M{"user_id": uid, "collective_id": cid}, nil
}

func (r memberships) Get(ctx context.Context, userID, collectiveID string) (domain.Membership, error) {
	filter, err := pairFilter(userID, collectiveID)
	if err != nil {
		return domain.Membership{}, err
	}
	var d membershipDoc
	if err := r.coll().FindOne(ctx, filter).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Membership{}, domain.ErrNotFound
		}
		return domain.Membership{}, wrap("get membership", err)
	}
	return d.membership(), nil
}

// Update replaces the mutable fields of the membership. A nil time or an empty
// approver is unset in the document; requested_at is never written.
func (r memberships) Update(ctx context.Context, m domain.Membership) error {
	if err := m.Validate(); err != nil {
		return err
	}
	filter, err := pairFilter(m.UserID, m.CollectiveID)
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
	for _, id := range []*bson.ObjectID{adminBy, ownerBy} {
		if err := r.s.mustExist(ctx, colUsers, id); err != nil {
			return err
		}
	}
	set := bson.M{"role": string(m.Role), "status": string(m.Status)}
	unset := bson.M{}
	for _, f := range []struct {
		field   string
		value   any
		present bool
	}{
		{"admin_approved_by", adminBy, adminBy != nil},
		{"admin_approved_at", m.AdminApprovedAt, m.AdminApprovedAt != nil},
		{"owner_approved_by", ownerBy, ownerBy != nil},
		{"owner_approved_at", m.OwnerApprovedAt, m.OwnerApprovedAt != nil},
		{"decided_at", m.DecidedAt, m.DecidedAt != nil},
	} {
		if f.present {
			set[f.field] = f.value
		} else {
			unset[f.field] = ""
		}
	}
	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	res, err := r.coll().UpdateOne(ctx, filter, update)
	if err != nil {
		return wrap("update membership", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r memberships) Delete(ctx context.Context, userID, collectiveID string) error {
	filter, err := pairFilter(userID, collectiveID)
	if err != nil {
		return err
	}
	res, err := r.coll().DeleteOne(ctx, filter)
	if err != nil {
		return wrap("delete membership", err)
	}
	if res.DeletedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListByCollective joins each membership of the collective with its user. MongoDB
// sorts strings as their bytes sort, and "owner" sorts after "member", so the
// owners first come from an explicit rank. The user is cut down to its name and
// status before the documents leave the server, so no password hash travels.
func (r memberships) ListByCollective(ctx context.Context, collectiveID string) ([]domain.MemberView, error) {
	oid, err := parseID(collectiveID)
	if err != nil {
		return nil, nil // an unknown collective has no memberships
	}
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"collective_id": oid}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colUsers, "localField": "user_id", "foreignField": "_id", "as": "user",
		}}},
		{{Key: "$unwind", Value: "$user"}},
		{{Key: "$addFields", Value: bson.M{
			"owner_rank": bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$role", string(domain.RoleOwner)}}, 0, 1}},
			"user_name":  "$user.name", "user_status": "$user.status",
		}}},
		{{Key: "$sort", Value: bson.D{key("owner_rank"), key("user_name"), key("user_id")}}},
		{{Key: "$project", Value: bson.M{"user": 0, "owner_rank": 0}}},
	})
	if err != nil {
		return nil, wrap("list members", err)
	}
	var docs []struct {
		Membership membershipDoc `bson:",inline"`
		UserName   string        `bson:"user_name"`
		UserStatus string        `bson:"user_status"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list members", err)
	}
	views := make([]domain.MemberView, len(docs))
	for i, d := range docs {
		views[i] = domain.MemberView{
			Membership: d.Membership.membership(),
			UserName:   d.UserName,
			UserStatus: domain.UserStatus(d.UserStatus),
		}
	}
	return views, nil
}

// ListAwaitingAdmin finds the pending memberships whose admin side is open and
// keeps those of an active or pending_approval user. The sort runs after that
// join, in memory, over the memberships with the user already dropped; the
// collective is joined only for the rows that survive the limit. It clamps limit
// itself: MongoDB reads a limit of 0 as "no limit" and a negative one as its
// absolute value.
func (r memberships) ListAwaitingAdmin(ctx context.Context, limit int) ([]domain.MembershipView, error) {
	limit = min(max(limit, 1), domain.ListLimitMax)
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"status":            string(domain.MembershipStatusPending),
			"admin_approved_at": bson.M{"$exists": false},
		}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colUsers, "localField": "user_id", "foreignField": "_id", "as": "user",
		}}},
		{{Key: "$unwind", Value: "$user"}},
		{{Key: "$match", Value: bson.M{"user.status": bson.M{"$in": bson.A{
			string(domain.UserStatusActive), string(domain.UserStatusPendingApproval),
		}}}}},
		{{Key: "$project", Value: bson.M{"user": 0}}},
		{{Key: "$sort", Value: bson.D{key("requested_at"), key("user_id"), key("collective_id")}}},
		{{Key: "$limit", Value: int64(limit)}},
		{{Key: "$lookup", Value: bson.M{
			"from": colCollectives, "localField": "collective_id", "foreignField": "_id", "as": "collective",
		}}},
		{{Key: "$unwind", Value: "$collective"}},
	})
	if err != nil {
		return nil, wrap("list memberships awaiting the admin", err)
	}
	return membershipViews(ctx, cur, "list memberships awaiting the admin")
}
