// Package domain is the database-independent core of the application: the
// entities, their enumerations, the domain errors and the field rules. Business
// code imports only this package; every storage backend maps to and from it and
// must pass the same contract suite.
//
// IDs are opaque strings chosen by the backend. A field that records who did
// something is a user ID that is empty when unknown or when that user was
// deleted. An optional time is a *time.Time that is nil when unset.
package domain

import (
	"errors"
	"time"
)

// UserStatus is the lifecycle state of an account.
type UserStatus string

const (
	UserStatusPendingEmail    UserStatus = "pending_email"
	UserStatusPendingApproval UserStatus = "pending_approval"
	UserStatusActive          UserStatus = "active"
	UserStatusDisabled        UserStatus = "disabled"
)

// Valid reports whether s is one of the defined statuses.
func (s UserStatus) Valid() bool {
	switch s {
	case UserStatusPendingEmail, UserStatusPendingApproval, UserStatusActive, UserStatusDisabled:
		return true
	}
	return false
}

// CollectiveKind tells a collective from a production company.
type CollectiveKind string

const (
	CollectiveKindColetivo  CollectiveKind = "coletivo"
	CollectiveKindProdutora CollectiveKind = "produtora"
)

// Valid reports whether k is one of the defined kinds.
func (k CollectiveKind) Valid() bool {
	switch k {
	case CollectiveKindColetivo, CollectiveKindProdutora:
		return true
	}
	return false
}

// CollectiveStatus is the admin's decision about a collective.
type CollectiveStatus string

const (
	CollectiveStatusPending  CollectiveStatus = "pending"
	CollectiveStatusApproved CollectiveStatus = "approved"
	CollectiveStatusRejected CollectiveStatus = "rejected"
)

// Valid reports whether s is one of the defined statuses.
func (s CollectiveStatus) Valid() bool {
	switch s {
	case CollectiveStatusPending, CollectiveStatusApproved, CollectiveStatusRejected:
		return true
	}
	return false
}

// MembershipStatus is the decision about a user's request to join a collective.
type MembershipStatus string

const (
	MembershipStatusPending  MembershipStatus = "pending"
	MembershipStatusApproved MembershipStatus = "approved"
	MembershipStatusRejected MembershipStatus = "rejected"
)

// Valid reports whether s is one of the defined statuses.
func (s MembershipStatus) Valid() bool {
	switch s {
	case MembershipStatusPending, MembershipStatusApproved, MembershipStatusRejected:
		return true
	}
	return false
}

// Role is what a member may do inside a collective.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// Valid reports whether r is one of the defined roles.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleMember:
		return true
	}
	return false
}

// TokenPurpose is what a one-time token authorizes.
type TokenPurpose string

const (
	TokenPurposeVerifyEmail   TokenPurpose = "verify_email"
	TokenPurposeResetPassword TokenPurpose = "reset_password"
	TokenPurposeChangeEmail   TokenPurpose = "change_email"
)

// Valid reports whether p is one of the defined purposes.
func (p TokenPurpose) Valid() bool {
	switch p {
	case TokenPurposeVerifyEmail, TokenPurposeResetPassword, TokenPurposeChangeEmail:
		return true
	}
	return false
}

// AuditAction is what a decision or request in the audit log did. The values
// are part of every backend's data format.
type AuditAction string

const (
	AuditAccountApproved     AuditAction = "account_approved"
	AuditAccountRejected     AuditAction = "account_rejected"
	AuditAccountBlocked      AuditAction = "account_blocked"
	AuditAccountUnblocked    AuditAction = "account_unblocked"
	AuditCollectiveRequested AuditAction = "collective_requested"
	AuditCollectiveApproved  AuditAction = "collective_approved"
	AuditCollectiveRejected  AuditAction = "collective_rejected"
	AuditCollectiveRenamed   AuditAction = "collective_renamed"
	AuditCollectiveBlocks    AuditAction = "collective_blocks_changed"
	AuditJoinRequested       AuditAction = "join_requested"
	AuditJoinAdminApproved   AuditAction = "join_admin_approved"
	AuditJoinOwnerApproved   AuditAction = "join_owner_approved"
	AuditJoinRejected        AuditAction = "join_rejected"
	AuditMemberPromoted      AuditAction = "member_promoted"
	AuditMemberDemoted       AuditAction = "member_demoted"
)

// Valid reports whether a is one of the defined actions.
func (a AuditAction) Valid() bool {
	switch a {
	case AuditAccountApproved, AuditAccountRejected, AuditAccountBlocked, AuditAccountUnblocked,
		AuditCollectiveRequested, AuditCollectiveApproved, AuditCollectiveRejected,
		AuditCollectiveRenamed, AuditCollectiveBlocks,
		AuditJoinRequested, AuditJoinAdminApproved, AuditJoinOwnerApproved, AuditJoinRejected,
		AuditMemberPromoted, AuditMemberDemoted:
		return true
	}
	return false
}

// Field limits shared by the rules and by the backends. Lengths count runes.
const (
	UserNameMin  = 1
	UserNameMax  = 120
	EmailMin     = 3
	EmailMax     = 254
	PhoneMin     = 10 // digits
	PhoneMax     = 11 // digits
	UserAgentMax = 300

	CollectiveNameMin = 2
	CollectiveNameMax = 80

	// TokenHashLen is the size in bytes of the SHA-256 stored for a token or a
	// session cookie.
	TokenHashLen = 32

	OutboxSubjectMax = 200

	// OutboxErrorMax is how many runes of an error MarkFailed keeps in
	// LastError.
	OutboxErrorMax = 500

	// SearchLimitMin and SearchLimitMax bound the limit of SearchApproved: a
	// limit outside the range is clamped to it.
	SearchLimitMin = 1
	SearchLimitMax = 50

	// ListLimitMax is the upper clamp of the limit of the list methods that take
	// one (SearchApproved has the range above): a limit below 1 is raised to 1
	// and one above ListLimitMax is lowered to it.
	ListLimitMax = 500

	// AuditTextMax is how many runes AuditEntry.Detail and AuditEntry.Reason
	// hold.
	AuditTextMax = 500
)

// Errors a backend returns and callers match with errors.Is.
var (
	ErrNotFound      = errors.New("domain: not found")
	ErrEmailTaken    = errors.New("domain: email already in use")
	ErrNameTaken     = errors.New("domain: name already in use")
	ErrAlreadyMember = errors.New("domain: already a member")
	ErrConflict      = errors.New("domain: conflict with the current state")
)

// User is an account.
type User struct {
	ID                string
	Name              string
	Email             string // normalized, see NormalizeEmail
	Phone             string // digits only
	PhoneIsWhatsApp   bool
	PasswordHash      string
	PasswordChangedAt time.Time
	Status            UserStatus
	IsAdmin           bool
	FailedLogins      int
	LockedUntil       *time.Time
	EmailVerifiedAt   *time.Time
	ApprovedAt        *time.Time
	ApprovedBy        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Collective is a party collective or a production company.
type Collective struct {
	ID         string
	Name       string
	NameKey    string // NameKey(Name), the unit of uniqueness
	Kind       CollectiveKind
	Status     CollectiveStatus
	CreatedBy  string
	ApprovedBy string
	ApprovedAt *time.Time
	// EventsBlocked means the collective's Administradores may not create or
	// edit events. The zero value is not blocked.
	EventsBlocked bool
	// Locked freezes the members and the name: the collective takes no join
	// decision, promotion, demotion or rename, and refuses new join requests.
	// The zero value is not locked.
	Locked    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Membership links a user to a collective. The pair (UserID, CollectiveID) is
// its identity.
type Membership struct {
	UserID          string
	CollectiveID    string
	Role            Role
	Status          MembershipStatus
	AdminApprovedBy string
	AdminApprovedAt *time.Time
	OwnerApprovedBy string
	OwnerApprovedAt *time.Time
	RequestedAt     time.Time
	DecidedAt       *time.Time
}

// Token is a one-time token mailed to a user. Only the SHA-256 of the token is
// kept, never the token itself.
type Token struct {
	ID        string
	UserID    string
	Purpose   TokenPurpose
	Hash      []byte
	NewEmail  string // set exactly when Purpose is TokenPurposeChangeEmail
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// Session is a login. Hash is the SHA-256 of the cookie value and identifies
// the session.
type Session struct {
	Hash       []byte
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
}

// OutboxMessage is an email waiting to be sent.
type OutboxMessage struct {
	ID            string
	To            string
	Subject       string
	Text          string
	HTML          string
	Attempts      int
	NextAttemptAt time.Time
	ClaimedUntil  *time.Time
	SentAt        *time.Time
	GaveUp        bool
	LastError     string
	CreatedAt     time.Time
}

// AuditEntry records one decision or request. It keeps IDs plus snapshots of the
// names at the time, so it stays readable after the user or the collective is
// deleted; it never holds an email address. ActorID is who acted, UserID the
// person the action is about and CollectiveID the collective, the last two
// empty when the action has none. Detail is a short description the code builds
// (for example "old → new" for a rename, the blocks that changed, the role) and
// Reason is the optional free text of whoever decided.
type AuditEntry struct {
	ID             string
	At             time.Time
	Action         AuditAction
	ActorID        string
	ActorName      string
	UserID         string
	UserName       string
	CollectiveID   string
	CollectiveName string
	Detail         string
	Reason         string
}
