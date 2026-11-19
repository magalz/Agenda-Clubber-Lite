package mongostore

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type events struct{ s *Store }

var _ domain.Events = events{}

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

// views runs the stages over the events, then joins each event with its
// collective, as "collective", for the name: the order of the stages is the order
// of the result, and a $limit among them cuts before the join. Collectives.Delete
// removes the events of its collective, but a Create that runs at the same time
// can still leave one behind (see Create): an event whose collective is gone is
// still read, with an empty name. op names the operation in the error.
func (r events) views(ctx context.Context, op string, stages ...bson.D) ([]domain.EventView, error) {
	cur, err := r.coll().Aggregate(ctx, append(mongo.Pipeline(stages),
		bson.D{{Key: "$lookup", Value: bson.M{
			"from": colCollectives, "localField": "collective_id", "foreignField": "_id", "as": "collective",
		}}},
		bson.D{{Key: "$unwind", Value: bson.M{"path": "$collective", "preserveNullAndEmptyArrays": true}}},
	))
	if err != nil {
		return nil, wrap(op, err)
	}
	var docs []struct {
		Event      eventDoc `bson:",inline"`
		Collective struct {
			Name string `bson:"name"`
		} `bson:"collective"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap(op, err)
	}
	views := make([]domain.EventView, len(docs))
	for i, d := range docs {
		views[i] = domain.EventView{Event: d.Event.event(), CollectiveName: d.Collective.Name}
	}
	return views, nil
}

func (r events) ByID(ctx context.Context, id string) (domain.EventView, error) {
	oid, err := parseID(id)
	if err != nil {
		return domain.EventView{}, err
	}
	views, err := r.views(ctx, "read event", bson.D{{Key: "$match", Value: bson.M{"_id": oid}}})
	if err != nil {
		return domain.EventView{}, err
	}
	if len(views) == 0 {
		return domain.EventView{}, domain.ErrNotFound
	}
	return views[0], nil
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
		return r.s.notMatched(ctx, colEvents, oid)
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

// ListBetween matches the range on starts_at, which holds whole milliseconds. The
// driver also cuts From and To to whole milliseconds, so the range is [From, To)
// on the cut values: a From and a To that fall in the same millisecond are an
// empty range, not an error. The FieldErrors it returns have the keys "city" (a
// City without a State) and "to" (a From that is not before To). State and City
// only ever travel as values.
func (r events) ListBetween(ctx context.Context, f domain.EventFilter, limit int) ([]domain.EventView, error) {
	var problems domain.FieldErrors
	if f.City != "" && f.State == "" {
		problems.Add("city", domain.MsgInvalid)
	}
	if !f.From.Before(f.To) {
		problems.Add("to", domain.MsgInvalid)
	}
	if err := problems.Err(); err != nil {
		return nil, err
	}
	match := bson.M{"starts_at": bson.M{"$gte": f.From, "$lt": f.To}}
	if f.State != "" {
		match["state"] = f.State
	}
	if f.City != "" {
		match["city"] = f.City
	}
	return r.views(ctx, "list events between",
		bson.D{{Key: "$match", Value: match}},
		bson.D{{Key: "$sort", Value: bson.D{key("starts_at"), key("_id")}}},
		bson.D{{Key: "$limit", Value: int64(min(max(limit, 1), domain.ListLimitMax))}},
	)
}

// ListByCollectives drops the IDs that cannot name a collective: they match no
// event, as an ID that names none does. The sort on _id (the index
// collective_id_starts_at does not end with it) is a blocking sort that keeps
// only limit events.
func (r events) ListByCollectives(ctx context.Context, ids []string, limit int) ([]domain.EventView, error) {
	oids := make(bson.A, 0, len(ids))
	for _, id := range ids {
		if oid, err := parseID(id); err == nil {
			oids = append(oids, oid)
		}
	}
	return r.views(ctx, "list events of collectives",
		bson.D{{Key: "$match", Value: bson.M{"collective_id": bson.M{"$in": oids}}}},
		bson.D{{Key: "$sort", Value: bson.D{keyDesc("starts_at"), keyDesc("_id")}}},
		bson.D{{Key: "$limit", Value: int64(min(max(limit, 1), domain.ListLimitMax))}},
	)
}

// ListPlaces groups on the two fields and sorts on them, in the byte order of
// the UTF-8 text (the database has no collation).
func (r events) ListPlaces(ctx context.Context) ([]domain.Place, error) {
	cur, err := r.coll().Aggregate(ctx, mongo.Pipeline{
		{{Key: "$group", Value: bson.M{"_id": bson.D{{Key: "state", Value: "$state"}, {Key: "city", Value: "$city"}}}}},
		{{Key: "$sort", Value: bson.D{key("_id.state"), key("_id.city")}}},
	})
	if err != nil {
		return nil, wrap("list event places", err)
	}
	var docs []struct {
		ID struct {
			State string `bson:"state"`
			City  string `bson:"city"`
		} `bson:"_id"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, wrap("list event places", err)
	}
	places := make([]domain.Place, len(docs))
	for i, d := range docs {
		places[i] = domain.Place{State: d.ID.State, City: d.ID.City}
	}
	return places, nil
}
