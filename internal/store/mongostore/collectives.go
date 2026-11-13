package mongostore

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type collectives struct{ s *Store }

var _ domain.Collectives = collectives{}

func (r collectives) coll() *mongo.Collection { return r.s.db.Collection(colCollectives) }

func (r collectives) Create(ctx context.Context, c *domain.Collective) error {
	if err := c.Validate(); err != nil {
		return err
	}
	createdBy, err := refID(c.CreatedBy)
	if err != nil {
		return err
	}
	approvedBy, err := refID(c.ApprovedBy)
	if err != nil {
		return err
	}
	if err := r.s.mustExist(ctx, colUsers, createdBy); err != nil {
		return err
	}
	if err := r.s.mustExist(ctx, colUsers, approvedBy); err != nil {
		return err
	}
	oid := bson.NewObjectID()
	if _, err := r.coll().InsertOne(ctx, collectiveToDoc(*c, oid, createdBy, approvedBy)); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return domain.ErrNameTaken
		}
		return wrap("create collective", err)
	}
	c.ID = oid.Hex()
	return nil
}

func (r collectives) ByID(ctx context.Context, id string) (domain.Collective, error) {
	oid, err := parseID(id)
	if err != nil {
		return domain.Collective{}, err
	}
	return r.find(ctx, bson.M{"_id": oid})
}

func (r collectives) ByNameKey(ctx context.Context, key string) (domain.Collective, error) {
	return r.find(ctx, bson.M{"name_key": key})
}

func (r collectives) find(ctx context.Context, filter bson.M) (domain.Collective, error) {
	var d collectiveDoc
	if err := r.coll().FindOne(ctx, filter).Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Collective{}, domain.ErrNotFound
		}
		return domain.Collective{}, wrap("read collective", err)
	}
	return d.collective(), nil
}

// SearchApproved matches the key as a plain substring with $indexOfCP, never as
// a regular expression, so no query is a pattern. The key is quoted with
// $literal because a string that starts with $ is a field path in an expression.
func (r collectives) SearchApproved(ctx context.Context, query string, kind domain.CollectiveKind, limit int) ([]domain.Collective, error) {
	limit = min(max(limit, domain.SearchLimitMin), domain.SearchLimitMax)
	filter := bson.M{
		"status": string(domain.CollectiveStatusApproved),
		"$expr": bson.M{"$gte": bson.A{
			bson.M{"$indexOfCP": bson.A{"$name_key", bson.M{"$literal": domain.NameKey(query)}}},
			0,
		}},
	}
	if kind != "" {
		filter["kind"] = string(kind)
	}
	cur, err := r.coll().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "name_key", Value: 1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrap("search collectives", err)
	}
	var docs []collectiveDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("search collectives", err)
	}
	found := make([]domain.Collective, len(docs))
	for i, d := range docs {
		found[i] = d.collective()
	}
	return found, nil
}

// Delete removes the collective and its memberships in one transaction (the
// caller's, when it has one).
func (r collectives) Delete(ctx context.Context, id string) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	return r.s.inTx(ctx, func(ctx context.Context) error {
		res, err := r.coll().DeleteOne(ctx, bson.M{"_id": oid})
		if err != nil {
			return wrap("delete collective", err)
		}
		if res.DeletedCount == 0 {
			return domain.ErrNotFound
		}
		if _, err := r.s.db.Collection(colMemberships).DeleteMany(ctx, bson.M{"collective_id": oid}); err != nil {
			return wrap("delete the memberships of a collective", err)
		}
		return nil
	})
}
