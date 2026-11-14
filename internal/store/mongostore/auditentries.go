package mongostore

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type auditEntries struct{ s *Store }

var _ domain.AuditEntries = auditEntries{}

func (r auditEntries) coll() *mongo.Collection { return r.s.db.Collection(colAuditEntries) }

// Create stores e under a new ID, the hex form of a new ObjectID kept as a
// string. The IDs of the actor, the user and the collective are stored as given:
// an entry never refers to another collection, so none is looked up.
func (r auditEntries) Create(ctx context.Context, e *domain.AuditEntry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	id := bson.NewObjectID().Hex()
	if _, err := r.coll().InsertOne(ctx, auditEntryToDoc(*e, id)); err != nil {
		return wrap("create audit entry", err)
	}
	e.ID = id
	return nil
}

// List reads the newest entries through the at_desc_id_desc index. The hex form
// of an ObjectID has a fixed width, so its string order is its creation order.
func (r auditEntries) List(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	limit = min(max(limit, 1), domain.ListLimitMax)
	cur, err := r.coll().Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "at", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, wrap("list audit entries", err)
	}
	var docs []auditEntryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list audit entries", err)
	}
	list := make([]domain.AuditEntry, len(docs))
	for i, d := range docs {
		list[i] = d.auditEntry()
	}
	return list, nil
}
