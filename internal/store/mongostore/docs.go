package mongostore

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The collections and the documents stored in them. Field names are snake_case.
// An ID is a bson.ObjectID that the domain sees as its hex form; a field that
// holds nobody (an empty reference) or no time is left out of the document, never
// stored as null or as a zero value. schema.go holds the validators that match
// these structs: change them together.

const (
	colUsers       = "users"
	colTokens      = "tokens"
	colSessions    = "sessions"
	colCollectives = "collectives"
	colMemberships = "memberships"
	colOutbox      = "outbox"
)

// parseID turns an ID the store issued into its ObjectID. Anything else (empty,
// malformed, another case of the same digits) is domain.ErrNotFound, as the
// contract asks: a backend never returns its own parse error.
func parseID(id string) (bson.ObjectID, error) {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil || oid.Hex() != id {
		return bson.ObjectID{}, domain.ErrNotFound
	}
	return oid, nil
}

// refID converts a reference field: empty is "nobody" and becomes nil; an ID that
// cannot name a user is domain.ErrNotFound, as a reference to a missing one is.
func refID(id string) (*bson.ObjectID, error) {
	if id == "" {
		return nil, nil
	}
	oid, err := parseID(id)
	if err != nil {
		return nil, err
	}
	return &oid, nil
}

// hexOrEmpty is the domain form of a reference field.
func hexOrEmpty(oid *bson.ObjectID) string {
	if oid == nil {
		return ""
	}
	return oid.Hex()
}

// utc normalizes a time read back from the database. The driver returns the
// instant; the contract wants it in UTC.
func utc(t time.Time) time.Time { return t.UTC() }

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// --- users ---

type userDoc struct {
	ID                bson.ObjectID  `bson:"_id"`
	Name              string         `bson:"name"`
	Email             string         `bson:"email"`
	Phone             string         `bson:"phone"`
	PhoneIsWhatsApp   bool           `bson:"phone_is_whatsapp"`
	PasswordHash      string         `bson:"password_hash"`
	PasswordHistory   []string       `bson:"password_history,omitempty"` // at most the previous hash
	PasswordChangedAt time.Time      `bson:"password_changed_at"`
	Status            string         `bson:"status"`
	IsAdmin           bool           `bson:"is_admin"`
	FailedLogins      int            `bson:"failed_logins"`
	LockedUntil       *time.Time     `bson:"locked_until,omitempty"`
	EmailVerifiedAt   *time.Time     `bson:"email_verified_at,omitempty"`
	ApprovedAt        *time.Time     `bson:"approved_at,omitempty"`
	ApprovedBy        *bson.ObjectID `bson:"approved_by,omitempty"`
	CreatedAt         time.Time      `bson:"created_at"`
	UpdatedAt         time.Time      `bson:"updated_at"`
}

// userToDoc builds the document of u under the ID oid.
func userToDoc(u domain.User, oid bson.ObjectID, approvedBy *bson.ObjectID) userDoc {
	return userDoc{
		ID:                oid,
		Name:              u.Name,
		Email:             u.Email,
		Phone:             u.Phone,
		PhoneIsWhatsApp:   u.PhoneIsWhatsApp,
		PasswordHash:      u.PasswordHash,
		PasswordChangedAt: u.PasswordChangedAt,
		Status:            string(u.Status),
		IsAdmin:           u.IsAdmin,
		FailedLogins:      u.FailedLogins,
		LockedUntil:       u.LockedUntil,
		EmailVerifiedAt:   u.EmailVerifiedAt,
		ApprovedAt:        u.ApprovedAt,
		ApprovedBy:        approvedBy,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
	}
}

func (d userDoc) user() domain.User {
	return domain.User{
		ID:                d.ID.Hex(),
		Name:              d.Name,
		Email:             d.Email,
		Phone:             d.Phone,
		PhoneIsWhatsApp:   d.PhoneIsWhatsApp,
		PasswordHash:      d.PasswordHash,
		PasswordChangedAt: utc(d.PasswordChangedAt),
		Status:            domain.UserStatus(d.Status),
		IsAdmin:           d.IsAdmin,
		FailedLogins:      d.FailedLogins,
		LockedUntil:       utcPtr(d.LockedUntil),
		EmailVerifiedAt:   utcPtr(d.EmailVerifiedAt),
		ApprovedAt:        utcPtr(d.ApprovedAt),
		ApprovedBy:        hexOrEmpty(d.ApprovedBy),
		CreatedAt:         utc(d.CreatedAt),
		UpdatedAt:         utc(d.UpdatedAt),
	}
}

// --- tokens ---

type tokenDoc struct {
	ID        bson.ObjectID `bson:"_id"`
	UserID    bson.ObjectID `bson:"user_id"`
	Purpose   string        `bson:"purpose"`
	Hash      []byte        `bson:"hash"`
	NewEmail  string        `bson:"new_email,omitempty"`
	ExpiresAt time.Time     `bson:"expires_at"`
	UsedAt    *time.Time    `bson:"used_at,omitempty"`
	CreatedAt time.Time     `bson:"created_at"`
}

func tokenToDoc(t domain.Token, oid, userID bson.ObjectID) tokenDoc {
	return tokenDoc{
		ID:        oid,
		UserID:    userID,
		Purpose:   string(t.Purpose),
		Hash:      t.Hash,
		NewEmail:  t.NewEmail,
		ExpiresAt: t.ExpiresAt,
		UsedAt:    t.UsedAt,
		CreatedAt: t.CreatedAt,
	}
}

func (d tokenDoc) token() domain.Token {
	return domain.Token{
		ID:        d.ID.Hex(),
		UserID:    d.UserID.Hex(),
		Purpose:   domain.TokenPurpose(d.Purpose),
		Hash:      d.Hash,
		NewEmail:  d.NewEmail,
		ExpiresAt: utc(d.ExpiresAt),
		UsedAt:    utcPtr(d.UsedAt),
		CreatedAt: utc(d.CreatedAt),
	}
}

// --- sessions ---

// sessionDoc is keyed by the 32-byte hash of the cookie: there is no other ID.
type sessionDoc struct {
	Hash       []byte        `bson:"_id"`
	UserID     bson.ObjectID `bson:"user_id"`
	CreatedAt  time.Time     `bson:"created_at"`
	LastSeenAt time.Time     `bson:"last_seen_at"`
	ExpiresAt  time.Time     `bson:"expires_at"`
	UserAgent  string        `bson:"user_agent"`
}

func sessionToDoc(s domain.Session, userID bson.ObjectID) sessionDoc {
	return sessionDoc{
		Hash:       s.Hash,
		UserID:     userID,
		CreatedAt:  s.CreatedAt,
		LastSeenAt: s.LastSeenAt,
		ExpiresAt:  s.ExpiresAt,
		UserAgent:  s.UserAgent,
	}
}

func (d sessionDoc) session() domain.Session {
	return domain.Session{
		Hash:       d.Hash,
		UserID:     d.UserID.Hex(),
		CreatedAt:  utc(d.CreatedAt),
		LastSeenAt: utc(d.LastSeenAt),
		ExpiresAt:  utc(d.ExpiresAt),
		UserAgent:  d.UserAgent,
	}
}

// --- collectives ---

type collectiveDoc struct {
	ID         bson.ObjectID  `bson:"_id"`
	Name       string         `bson:"name"`
	NameKey    string         `bson:"name_key"`
	Kind       string         `bson:"kind"`
	Status     string         `bson:"status"`
	CreatedBy  *bson.ObjectID `bson:"created_by,omitempty"`
	ApprovedBy *bson.ObjectID `bson:"approved_by,omitempty"`
	ApprovedAt *time.Time     `bson:"approved_at,omitempty"`
	CreatedAt  time.Time      `bson:"created_at"`
	UpdatedAt  time.Time      `bson:"updated_at"`
}

func collectiveToDoc(c domain.Collective, oid bson.ObjectID, createdBy, approvedBy *bson.ObjectID) collectiveDoc {
	return collectiveDoc{
		ID:         oid,
		Name:       c.Name,
		NameKey:    c.NameKey,
		Kind:       string(c.Kind),
		Status:     string(c.Status),
		CreatedBy:  createdBy,
		ApprovedBy: approvedBy,
		ApprovedAt: c.ApprovedAt,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

func (d collectiveDoc) collective() domain.Collective {
	return domain.Collective{
		ID:         d.ID.Hex(),
		Name:       d.Name,
		NameKey:    d.NameKey,
		Kind:       domain.CollectiveKind(d.Kind),
		Status:     domain.CollectiveStatus(d.Status),
		CreatedBy:  hexOrEmpty(d.CreatedBy),
		ApprovedBy: hexOrEmpty(d.ApprovedBy),
		ApprovedAt: utcPtr(d.ApprovedAt),
		CreatedAt:  utc(d.CreatedAt),
		UpdatedAt:  utc(d.UpdatedAt),
	}
}

// --- memberships ---

// membershipDoc has no ID of its own in the domain; the pair (user_id,
// collective_id) is unique.
type membershipDoc struct {
	UserID          bson.ObjectID  `bson:"user_id"`
	CollectiveID    bson.ObjectID  `bson:"collective_id"`
	Role            string         `bson:"role"`
	Status          string         `bson:"status"`
	AdminApprovedBy *bson.ObjectID `bson:"admin_approved_by,omitempty"`
	AdminApprovedAt *time.Time     `bson:"admin_approved_at,omitempty"`
	OwnerApprovedBy *bson.ObjectID `bson:"owner_approved_by,omitempty"`
	OwnerApprovedAt *time.Time     `bson:"owner_approved_at,omitempty"`
	RequestedAt     time.Time      `bson:"requested_at"`
	DecidedAt       *time.Time     `bson:"decided_at,omitempty"`
}

func membershipToDoc(m domain.Membership, userID, collectiveID bson.ObjectID, adminBy, ownerBy *bson.ObjectID) membershipDoc {
	return membershipDoc{
		UserID:          userID,
		CollectiveID:    collectiveID,
		Role:            string(m.Role),
		Status:          string(m.Status),
		AdminApprovedBy: adminBy,
		AdminApprovedAt: m.AdminApprovedAt,
		OwnerApprovedBy: ownerBy,
		OwnerApprovedAt: m.OwnerApprovedAt,
		RequestedAt:     m.RequestedAt,
		DecidedAt:       m.DecidedAt,
	}
}

func (d membershipDoc) membership() domain.Membership {
	return domain.Membership{
		UserID:          d.UserID.Hex(),
		CollectiveID:    d.CollectiveID.Hex(),
		Role:            domain.Role(d.Role),
		Status:          domain.MembershipStatus(d.Status),
		AdminApprovedBy: hexOrEmpty(d.AdminApprovedBy),
		AdminApprovedAt: utcPtr(d.AdminApprovedAt),
		OwnerApprovedBy: hexOrEmpty(d.OwnerApprovedBy),
		OwnerApprovedAt: utcPtr(d.OwnerApprovedAt),
		RequestedAt:     utc(d.RequestedAt),
		DecidedAt:       utcPtr(d.DecidedAt),
	}
}

// --- outbox ---

// outboxDoc is stored by the outbox repository, which the next task adds; the
// document and its validator live here so the schema is complete in one place.
type outboxDoc struct {
	ID            bson.ObjectID `bson:"_id"`
	To            string        `bson:"to"`
	Subject       string        `bson:"subject"`
	Text          string        `bson:"text"`
	HTML          string        `bson:"html,omitempty"`
	Attempts      int           `bson:"attempts"`
	NextAttemptAt time.Time     `bson:"next_attempt_at"`
	ClaimedUntil  *time.Time    `bson:"claimed_until,omitempty"`
	SentAt        *time.Time    `bson:"sent_at,omitempty"`
	GaveUp        bool          `bson:"gave_up"`
	LastError     string        `bson:"last_error,omitempty"`
	CreatedAt     time.Time     `bson:"created_at"`
}

func outboxToDoc(m domain.OutboxMessage, oid bson.ObjectID) outboxDoc {
	return outboxDoc{
		ID:            oid,
		To:            m.To,
		Subject:       m.Subject,
		Text:          m.Text,
		HTML:          m.HTML,
		Attempts:      m.Attempts,
		NextAttemptAt: m.NextAttemptAt,
		ClaimedUntil:  m.ClaimedUntil,
		SentAt:        m.SentAt,
		GaveUp:        m.GaveUp,
		LastError:     m.LastError,
		CreatedAt:     m.CreatedAt,
	}
}

func (d outboxDoc) message() domain.OutboxMessage {
	return domain.OutboxMessage{
		ID:            d.ID.Hex(),
		To:            d.To,
		Subject:       d.Subject,
		Text:          d.Text,
		HTML:          d.HTML,
		Attempts:      d.Attempts,
		NextAttemptAt: utc(d.NextAttemptAt),
		ClaimedUntil:  utcPtr(d.ClaimedUntil),
		SentAt:        utcPtr(d.SentAt),
		GaveUp:        d.GaveUp,
		LastError:     d.LastError,
		CreatedAt:     utc(d.CreatedAt),
	}
}
