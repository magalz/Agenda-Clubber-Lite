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
//
// The driver decodes a BSON datetime to UTC (it is never asked to use the local
// time zone), so the converters back to the domain copy times as they are.

const (
	colUsers       = "users"
	colTokens      = "tokens"
	colSessions    = "sessions"
	colCollectives = "collectives"
	colMemberships = "memberships"
	colOutbox      = "outbox"

	colAuditEntries = "audit_entries"
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
		PasswordChangedAt: d.PasswordChangedAt,
		Status:            domain.UserStatus(d.Status),
		IsAdmin:           d.IsAdmin,
		FailedLogins:      d.FailedLogins,
		LockedUntil:       d.LockedUntil,
		EmailVerifiedAt:   d.EmailVerifiedAt,
		ApprovedAt:        d.ApprovedAt,
		ApprovedBy:        hexOrEmpty(d.ApprovedBy),
		CreatedAt:         d.CreatedAt,
		UpdatedAt:         d.UpdatedAt,
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
		ExpiresAt: d.ExpiresAt,
		UsedAt:    d.UsedAt,
		CreatedAt: d.CreatedAt,
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
		CreatedAt:  d.CreatedAt,
		LastSeenAt: d.LastSeenAt,
		ExpiresAt:  d.ExpiresAt,
		UserAgent:  d.UserAgent,
	}
}

// --- collectives ---

// collectiveDoc keeps each flag only when it is set (a false flag is left out) and
// Rev, which the domain never sees: Update bumps it so that two transactions that
// both write the collective conflict. Create leaves it out.
type collectiveDoc struct {
	ID            bson.ObjectID  `bson:"_id"`
	Name          string         `bson:"name"`
	NameKey       string         `bson:"name_key"`
	Kind          string         `bson:"kind"`
	Status        string         `bson:"status"`
	CreatedBy     *bson.ObjectID `bson:"created_by,omitempty"`
	ApprovedBy    *bson.ObjectID `bson:"approved_by,omitempty"`
	ApprovedAt    *time.Time     `bson:"approved_at,omitempty"`
	EventsBlocked bool           `bson:"events_blocked,omitempty"`
	Locked        bool           `bson:"locked,omitempty"`
	Rev           int64          `bson:"rev,omitempty"`
	CreatedAt     time.Time      `bson:"created_at"`
	UpdatedAt     time.Time      `bson:"updated_at"`
}

func collectiveToDoc(c domain.Collective, oid bson.ObjectID, createdBy, approvedBy *bson.ObjectID) collectiveDoc {
	return collectiveDoc{
		ID:            oid,
		Name:          c.Name,
		NameKey:       c.NameKey,
		Kind:          string(c.Kind),
		Status:        string(c.Status),
		CreatedBy:     createdBy,
		ApprovedBy:    approvedBy,
		ApprovedAt:    c.ApprovedAt,
		EventsBlocked: c.EventsBlocked,
		Locked:        c.Locked,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}

func (d collectiveDoc) collective() domain.Collective {
	return domain.Collective{
		ID:            d.ID.Hex(),
		Name:          d.Name,
		NameKey:       d.NameKey,
		Kind:          domain.CollectiveKind(d.Kind),
		Status:        domain.CollectiveStatus(d.Status),
		CreatedBy:     hexOrEmpty(d.CreatedBy),
		ApprovedBy:    hexOrEmpty(d.ApprovedBy),
		ApprovedAt:    d.ApprovedAt,
		EventsBlocked: d.EventsBlocked,
		Locked:        d.Locked,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
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
		AdminApprovedAt: d.AdminApprovedAt,
		OwnerApprovedBy: hexOrEmpty(d.OwnerApprovedBy),
		OwnerApprovedAt: d.OwnerApprovedAt,
		RequestedAt:     d.RequestedAt,
		DecidedAt:       d.DecidedAt,
	}
}

// --- outbox ---

// outboxDoc is stored by the outbox repository (outbox.go). ClaimedUntil is the
// lease and is unset while nobody holds one.
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
		NextAttemptAt: d.NextAttemptAt,
		ClaimedUntil:  d.ClaimedUntil,
		SentAt:        d.SentAt,
		GaveUp:        d.GaveUp,
		LastError:     d.LastError,
		CreatedAt:     d.CreatedAt,
	}
}

// --- audit entries ---

// auditEntryDoc is an immutable log line. Its _id, actor_id, user_id and
// collective_id are plain strings and not ObjectIDs: they are snapshots that are
// never checked against another collection and must survive the deletion of the
// user or the collective they name. Optional text is left out when empty.
type auditEntryDoc struct {
	ID             string    `bson:"_id"`
	At             time.Time `bson:"at"`
	Action         string    `bson:"action"`
	ActorID        string    `bson:"actor_id"`
	ActorName      string    `bson:"actor_name,omitempty"`
	UserID         string    `bson:"user_id,omitempty"`
	UserName       string    `bson:"user_name,omitempty"`
	CollectiveID   string    `bson:"collective_id,omitempty"`
	CollectiveName string    `bson:"collective_name,omitempty"`
	Detail         string    `bson:"detail,omitempty"`
	Reason         string    `bson:"reason,omitempty"`
}

// auditEntryToDoc builds the document of e under the ID id.
func auditEntryToDoc(e domain.AuditEntry, id string) auditEntryDoc {
	return auditEntryDoc{
		ID:             id,
		At:             e.At,
		Action:         string(e.Action),
		ActorID:        e.ActorID,
		ActorName:      e.ActorName,
		UserID:         e.UserID,
		UserName:       e.UserName,
		CollectiveID:   e.CollectiveID,
		CollectiveName: e.CollectiveName,
		Detail:         e.Detail,
		Reason:         e.Reason,
	}
}

func (d auditEntryDoc) auditEntry() domain.AuditEntry {
	return domain.AuditEntry{
		ID:             d.ID,
		At:             d.At,
		Action:         domain.AuditAction(d.Action),
		ActorID:        d.ActorID,
		ActorName:      d.ActorName,
		UserID:         d.UserID,
		UserName:       d.UserName,
		CollectiveID:   d.CollectiveID,
		CollectiveName: d.CollectiveName,
		Detail:         d.Detail,
		Reason:         d.Reason,
	}
}
