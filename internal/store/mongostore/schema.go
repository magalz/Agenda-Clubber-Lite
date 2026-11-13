package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// codeNamespaceExists is the server's answer to creating a collection that is
// already there.
const codeNamespaceExists = 48

// Property types of a $jsonSchema validator.
var (
	tObjectID = bson.M{"bsonType": "objectId"}
	tString   = bson.M{"bsonType": "string"}
	tBool     = bson.M{"bsonType": "bool"}
	tDate     = bson.M{"bsonType": "date"}
	tBinary   = bson.M{"bsonType": "binData"}
	// A Go int is stored as int32 when it fits and the server adds as int32 or int64.
	tInt = bson.M{"bsonType": bson.A{"int", "long"}}
)

func tEnum[T ~string](values ...T) bson.M {
	enum := make(bson.A, len(values))
	for i, v := range values {
		enum[i] = string(v)
	}
	return bson.M{"bsonType": "string", "enum": enum}
}

// validator builds the $jsonSchema of a collection: _id has the given type, the
// fields in required must be present, and nothing outside properties is allowed.
func validator(id bson.M, required []string, properties bson.M) bson.M {
	props := bson.M{"_id": id}
	for k, v := range properties {
		props[k] = v
	}
	req := bson.A{"_id"}
	for _, k := range required {
		req = append(req, k)
	}
	return bson.M{"$jsonSchema": bson.M{
		"bsonType":             "object",
		"required":             req,
		"properties":           props,
		"additionalProperties": false,
	}}
}

// collectionSpec is a collection with its validator and its indexes. An index
// whose name ends in _unique is unique. There is no TTL index: the contract says
// a backend never expires data on its own.
type collectionSpec struct {
	name      string
	validator bson.M
	indexes   []mongo.IndexModel
}

func index(name string, unique bool, keys bson.D) mongo.IndexModel {
	opts := options.Index().SetName(name)
	if unique {
		opts.SetUnique(true)
	}
	return mongo.IndexModel{Keys: keys, Options: opts}
}

func key(field string) bson.E { return bson.E{Key: field, Value: 1} }

func specs() []collectionSpec {
	return []collectionSpec{
		{
			name: colUsers,
			validator: validator(tObjectID,
				[]string{"name", "email", "phone", "phone_is_whatsapp", "password_hash", "password_changed_at",
					"status", "is_admin", "failed_logins", "created_at", "updated_at"},
				bson.M{
					"name":                tString,
					"email":               tString,
					"phone":               tString,
					"phone_is_whatsapp":   tBool,
					"password_hash":       tString,
					"password_history":    bson.M{"bsonType": "array", "items": tString, "maxItems": 1},
					"password_changed_at": tDate,
					"status": tEnum(domain.UserStatusPendingEmail, domain.UserStatusPendingApproval,
						domain.UserStatusActive, domain.UserStatusDisabled),
					"is_admin":          tBool,
					"failed_logins":     tInt,
					"locked_until":      tDate,
					"email_verified_at": tDate,
					"approved_at":       tDate,
					"approved_by":       tObjectID,
					"created_at":        tDate,
					"updated_at":        tDate,
				}),
			indexes: []mongo.IndexModel{
				index("email_unique", true, bson.D{key("email")}),
			},
		},
		{
			name: colTokens,
			validator: validator(tObjectID,
				[]string{"user_id", "purpose", "hash", "expires_at", "created_at"},
				bson.M{
					"user_id": tObjectID,
					"purpose": tEnum(domain.TokenPurposeVerifyEmail, domain.TokenPurposeResetPassword,
						domain.TokenPurposeChangeEmail),
					"hash":       tBinary,
					"new_email":  tString,
					"expires_at": tDate,
					"used_at":    tDate,
					"created_at": tDate,
				}),
			indexes: []mongo.IndexModel{
				index("hash_unique", true, bson.D{key("hash")}),
				index("user_id_purpose", false, bson.D{key("user_id"), key("purpose")}),
			},
		},
		{
			name: colSessions,
			validator: validator(tBinary,
				[]string{"user_id", "created_at", "last_seen_at", "expires_at", "user_agent"},
				bson.M{
					"user_id":      tObjectID,
					"created_at":   tDate,
					"last_seen_at": tDate,
					"expires_at":   tDate,
					"user_agent":   tString,
				}),
			indexes: []mongo.IndexModel{
				index("user_id", false, bson.D{key("user_id")}),
			},
		},
		{
			name: colCollectives,
			validator: validator(tObjectID,
				[]string{"name", "name_key", "kind", "status", "created_at", "updated_at"},
				bson.M{
					"name":        tString,
					"name_key":    tString,
					"kind":        tEnum(domain.CollectiveKindColetivo, domain.CollectiveKindProdutora),
					"status":      tEnum(domain.CollectiveStatusPending, domain.CollectiveStatusApproved, domain.CollectiveStatusRejected),
					"created_by":  tObjectID,
					"approved_by": tObjectID,
					"approved_at": tDate,
					"created_at":  tDate,
					"updated_at":  tDate,
				}),
			indexes: []mongo.IndexModel{
				index("name_key_unique", true, bson.D{key("name_key")}),
				index("status_kind_name_key", false, bson.D{key("status"), key("kind"), key("name_key")}),
			},
		},
		{
			name: colMemberships,
			validator: validator(tObjectID,
				[]string{"user_id", "collective_id", "role", "status", "requested_at"},
				bson.M{
					"user_id":           tObjectID,
					"collective_id":     tObjectID,
					"role":              tEnum(domain.RoleOwner, domain.RoleMember),
					"status":            tEnum(domain.MembershipStatusPending, domain.MembershipStatusApproved, domain.MembershipStatusRejected),
					"admin_approved_by": tObjectID,
					"admin_approved_at": tDate,
					"owner_approved_by": tObjectID,
					"owner_approved_at": tDate,
					"requested_at":      tDate,
					"decided_at":        tDate,
				}),
			indexes: []mongo.IndexModel{
				index("user_id_collective_id_unique", true, bson.D{key("user_id"), key("collective_id")}),
				index("collective_id_status", false, bson.D{key("collective_id"), key("status")}),
			},
		},
		{
			name: colOutbox,
			validator: validator(tObjectID,
				[]string{"to", "subject", "text", "attempts", "next_attempt_at", "gave_up", "created_at"},
				bson.M{
					"to":              tString,
					"subject":         tString,
					"text":            tString,
					"html":            tString,
					"attempts":        tInt,
					"next_attempt_at": tDate,
					"claimed_until":   tDate,
					"sent_at":         tDate,
					"gave_up":         tBool,
					"last_error":      tString,
					"created_at":      tDate,
				}),
			indexes: []mongo.IndexModel{
				index("sent_at_gave_up_next_attempt_at", false, bson.D{key("sent_at"), key("gave_up"), key("next_attempt_at")}),
			},
		},
	}
}

// EnsureSchema creates the collections with their validators and their indexes.
// It is safe to run on every start and from several processes at once: a
// collection that exists gets its validator replaced (so a new version of the
// schema reaches an old database), and an index that exists is left alone.
func (s *Store) EnsureSchema(ctx context.Context) error {
	existing, err := s.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("mongostore: list collections: %w", err)
	}
	have := make(map[string]bool, len(existing))
	for _, name := range existing {
		have[name] = true
	}
	all := specs()
	for _, spec := range all {
		if !have[spec.name] {
			opts := options.CreateCollection().
				SetValidator(spec.validator).
				SetValidationLevel("strict").
				SetValidationAction("error")
			err := s.db.CreateCollection(ctx, spec.name, opts)
			if err == nil {
				continue
			}
			var ce mongo.CommandError
			if !errors.As(err, &ce) || ce.Code != codeNamespaceExists {
				return fmt.Errorf("mongostore: create collection %s: %w", spec.name, err)
			}
			// Another process created it first: bring its validator up to date.
		}
		cmd := bson.D{
			{Key: "collMod", Value: spec.name},
			{Key: "validator", Value: spec.validator},
			{Key: "validationLevel", Value: "strict"},
			{Key: "validationAction", Value: "error"},
		}
		if err := s.db.RunCommand(ctx, cmd).Err(); err != nil {
			return fmt.Errorf("mongostore: update the validator of %s: %w", spec.name, err)
		}
	}
	for _, spec := range all {
		if _, err := s.db.Collection(spec.name).Indexes().CreateMany(ctx, spec.indexes); err != nil {
			return fmt.Errorf("mongostore: create the indexes of %s: %w", spec.name, err)
		}
	}
	return nil
}
