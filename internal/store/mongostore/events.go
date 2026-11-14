package mongostore

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type events struct{ s *Store }

var _ domain.Events = events{}

// errNotImplemented is what the list methods of events return until the task that
// writes them replaces the stub. The collection, its validator and its indexes
// already exist (schema.go).
var errNotImplemented = errors.New("mongostore: events: not implemented")

func (r events) coll() *mongo.Collection { return r.s.db.Collection(colEvents) }

// Create checks the references and inserts the event in one transaction (the
// caller's, when it has one). The check is a read in that transaction, so a
// concurrent delete of the collective or of a user can still slip between the
// two steps (known gap ACL-21): the event then keeps a reference that names
// nothing.
func (r events) Create(ctx context.Context, e *domain.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	collectiveID, err := parseID(e.CollectiveID)
	if err != nil {
		return err
	}
	createdBy, err := parseID(e.CreatedBy) // an empty CreatedBy is not found
	if err != nil {
		return err
	}
	updatedBy, err := refID(e.UpdatedBy)
	if err != nil {
		return err
	}
	oid := bson.NewObjectID()
	doc := eventToDoc(*e, oid, collectiveID, &createdBy, updatedBy)
	doc.Version = 1
	err = r.s.inTx(ctx, func(ctx context.Context) error {
		if err := r.s.mustExist(ctx, colCollectives, &collectiveID); err != nil {
			return err
		}
		if err := r.s.mustExist(ctx, colUsers, &createdBy); err != nil {
			return err
		}
		if err := r.s.mustExist(ctx, colUsers, updatedBy); err != nil {
			return err
		}
		if _, err := r.coll().InsertOne(ctx, doc); err != nil {
			return wrap("create event", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.ID, e.Version = oid.Hex(), 1
	return nil
}

// ByID joins the event with its collective, as "collective", for the name. An
// event whose collective is gone (the cascade of a delete is a later task, and a
// concurrent create can leave one behind) is still read, with an empty name.
func (r events) ByID(ctx context.Context, id string) (domain.EventView, error) {
	oid, err := parseID(id)
	if err != nil {
		return domain.EventView{}, err
	}
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"_id": oid}}},
		{{Key: "$lookup", Value: bson.M{
			"from": colCollectives, "localField": "collective_id", "foreignField": "_id", "as": "collective",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$collective", "preserveNullAndEmptyArrays": true}}},
	})
	if err != nil {
		return domain.EventView{}, wrap("read event", err)
	}
	var docs []struct {
		Event      eventDoc `bson:",inline"`
		Collective struct {
			Name string `bson:"name"`
		} `bson:"collective"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return domain.EventView{}, wrap("read event", err)
	}
	if len(docs) == 0 {
		return domain.EventView{}, domain.ErrNotFound
	}
	return domain.EventView{Event: docs[0].Event.event(), CollectiveName: docs[0].Collective.Name}, nil
}

// Update looks at the editor first, then writes with the version in the filter:
// a filter that matches nothing is a conflict when the event exists and not found
// when it does not. The check of the editor is a read in the same transaction.
func (r events) Update(ctx context.Context, e domain.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	oid, err := parseID(e.ID)
	if err != nil {
		return err
	}
	updatedBy, err := parseID(e.UpdatedBy) // an empty UpdatedBy is not found
	if err != nil {
		return err
	}
	return r.s.inTx(ctx, func(ctx context.Context) error {
		if err := r.s.mustExist(ctx, colUsers, &updatedBy); err != nil {
			return err
		}
		res, err := r.coll().UpdateOne(ctx, bson.M{"_id": oid, "version": e.Version}, eventUpdate(e, updatedBy))
		if err != nil {
			return wrap("update event", err)
		}
		if res.MatchedCount > 0 {
			return nil
		}
		found, err := r.s.exists(ctx, colEvents, oid)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrNotFound
		}
		return domain.ErrConflict
	})
}

// eventUpdate is the update document of Update: the editable fields set, an
// optional text that is empty removed (never stored as null or as ""), and the
// version incremented. Every key is a constant.
func eventUpdate(e domain.Event, updatedBy bson.ObjectID) bson.M {
	set := bson.M{
		"name":         e.Name,
		"type":         string(e.Type),
		"starts_at":    e.StartsAt,
		"duration_min": e.DurationMin,
		"free":         e.Free,
		"price_cents":  e.PriceCents,
		"style":        e.Style,
		"state":        e.State,
		"city":         e.City,
		"updated_by":   updatedBy,
		"updated_at":   e.UpdatedAt,
	}
	unset := bson.M{}
	for key, value := range map[string]string{
		"type_other":  e.TypeOther,
		"ticket_url":  e.TicketURL,
		"substyle":    e.Substyle,
		"style_other": e.StyleOther,
		"venue":       e.Venue,
		"artists":     e.Artists,
		"description": e.Description,
	} {
		if value != "" {
			set[key] = value
		} else {
			unset[key] = ""
		}
	}
	update := bson.M{"$set": set, "$inc": bson.M{"version": int64(1)}}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	return update
}

func (r events) Delete(ctx context.Context, id string) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	res, err := r.coll().DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return wrap("delete event", err)
	}
	if res.DeletedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (events) ListBetween(context.Context, domain.EventFilter, int) ([]domain.EventView, error) {
	return nil, errNotImplemented
}

func (events) ListByCollectives(context.Context, []string, int) ([]domain.EventView, error) {
	return nil, errNotImplemented
}

func (events) ListPlaces(context.Context) ([]domain.Place, error) {
	return nil, errNotImplemented
}
