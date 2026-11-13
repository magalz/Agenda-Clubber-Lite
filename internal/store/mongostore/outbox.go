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

type outbox struct{ s *Store }

var _ domain.Outbox = outbox{}

func (r outbox) coll() *mongo.Collection { return r.s.db.Collection(colOutbox) }

// Enqueue stores m with Attempts 0 and a new ID.
func (r outbox) Enqueue(ctx context.Context, m *domain.OutboxMessage) error {
	if err := m.Validate(); err != nil {
		return err
	}
	m.Attempts = 0
	oid := bson.NewObjectID()
	if _, err := r.coll().InsertOne(ctx, outboxToDoc(*m, oid)); err != nil {
		return wrap("enqueue message", err)
	}
	m.ID = oid.Hex()
	return nil
}

// Claim leases the due messages one find-and-update at a time: the filter and
// the lease are a single atomic step on the server, so of several concurrent
// calls exactly one gets a message. A call that fails half way has leased some
// messages it does not return; their leases run out and a later Claim returns
// them.
func (r outbox) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.OutboxMessage, error) {
	filter := bson.M{
		"sent_at":         nil,
		"gave_up":         false,
		"next_attempt_at": bson.M{"$lte": now},
		"$or": bson.A{
			bson.M{"claimed_until": nil},
			bson.M{"claimed_until": bson.M{"$lte": now}},
		},
	}
	update := bson.M{"$set": bson.M{"claimed_until": now.Add(lease)}}
	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "next_attempt_at", Value: 1}, {Key: "_id", Value: 1}}).
		SetReturnDocument(options.After)

	var claimed []domain.OutboxMessage
	for len(claimed) < limit {
		var d outboxDoc
		err := r.coll().FindOneAndUpdate(ctx, filter, update, opts).Decode(&d)
		if errors.Is(err, mongo.ErrNoDocuments) {
			break
		}
		if err != nil {
			return nil, wrap("claim messages", err)
		}
		claimed = append(claimed, d.message())
	}
	return claimed, nil
}

// MarkSent sets sent_at, which takes the message out of every Claim.
func (r outbox) MarkSent(ctx context.Context, id string, now time.Time) error {
	return r.update(ctx, id, "mark message sent", bson.M{"$set": bson.M{"sent_at": now}})
}

// MarkFailed counts the attempt, keeps the start of the error and clears the
// lease; a nil retryAt gives the message up.
func (r outbox) MarkFailed(ctx context.Context, id string, lastError string, retryAt *time.Time) error {
	// Converting through runes also replaces bytes that are not valid UTF-8,
	// which the server would refuse in a string.
	runes := []rune(lastError)
	if len(runes) > domain.OutboxErrorMax {
		runes = runes[:domain.OutboxErrorMax]
	}
	set := bson.M{"last_error": string(runes)}
	if retryAt != nil {
		set["next_attempt_at"] = *retryAt
	} else {
		set["gave_up"] = true
	}
	return r.update(ctx, id, "mark message failed", bson.M{
		"$inc":   bson.M{"attempts": 1},
		"$set":   set,
		"$unset": bson.M{"claimed_until": ""},
	})
}

// update applies update to one message; an ID that names none is ErrNotFound.
func (r outbox) update(ctx context.Context, id, op string, update bson.M) error {
	oid, err := parseID(id)
	if err != nil {
		return err
	}
	return updateOne(ctx, r.coll(), op, oid, update)
}
