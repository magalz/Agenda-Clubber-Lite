package domain

import (
	"context"
	"time"
)

// The interfaces below are the storage contract. Business code depends only on
// them, and every backend must pass the suite in internal/store/storetest.
//
// Conventions that hold for every method:
//
//   - Order of checks on a Create: first the entity's Validate (its FieldErrors
//     are returned as they are; callers match them with errors.As), then the
//     references to other entities (ErrNotFound), then uniqueness (ErrEmailTaken,
//     ErrNameTaken, ErrAlreadyMember, ErrConflict). A failed call stores nothing.
//   - An ID that the store never issued is ErrNotFound, whatever its form: empty
//     or malformed IDs included. A backend never returns its own parse error.
//   - A reference field that is not empty (CreatedBy, ApprovedBy, ...) must name
//     an existing user, else the Create fails with ErrNotFound. An empty one means
//     "nobody": unknown, or the user was deleted.
//   - A list that matches nothing is empty (possibly nil) and is not an error.
//   - Times are stored in UTC with at least millisecond precision. A backend may
//     truncate to milliseconds, so a time read back can differ from the one
//     written by less than a millisecond (compare with Truncate(time.Millisecond)
//     and Equal). Where a rule compares a stored time with a time the caller
//     passes (an expiry, a lease), the rule is exact at the boundary: callers
//     pass whole milliseconds when it matters.
//   - A backend never expires, deletes or changes data on its own (no TTL
//     indexes): an expired session or token stays until a method removes it.
//   - Every call is atomic by itself. Several calls are atomic together only
//     inside Store.WithTx.

// Users stores accounts.
type Users interface {
	// Create validates u and returns its FieldErrors, assigns u.ID (an ID the
	// caller set is ignored) and stores every other field as given. It returns
	// ErrEmailTaken when another user already has u.Email (Validate requires the
	// NormalizeEmail form, so uniqueness ignores case in effect) and ErrNotFound
	// when u.ApprovedBy is not empty and names no user.
	Create(ctx context.Context, u *User) error

	// ByID returns the user, or ErrNotFound.
	ByID(ctx context.Context, id string) (User, error)

	// ByEmail returns the user whose email is NormalizeEmail(email). An email
	// that cannot be normalized, or that nobody uses, is ErrNotFound.
	ByEmail(ctx context.Context, email string) (User, error)

	// SetStatus sets Status and UpdatedAt (to now). A status that is not defined
	// is a FieldErrors with the key "status". A missing user is ErrNotFound.
	SetStatus(ctx context.Context, id string, status UserStatus, now time.Time) error

	// MarkEmailVerified sets EmailVerifiedAt and UpdatedAt (to now) and moves
	// the status from pending_email to pending_approval. A user in any other
	// status is ErrConflict and is left unchanged. A missing user is ErrNotFound.
	MarkEmailVerified(ctx context.Context, id string, now time.Time) error

	// ChangePassword makes newHash the current hash and keeps the hash it
	// replaces as the single previous one (the one before that is forgotten). It
	// sets PasswordChangedAt and UpdatedAt (to now), sets FailedLogins to 0 and
	// clears LockedUntil. An empty newHash is a FieldErrors with the key
	// "password_hash". A missing user is ErrNotFound.
	ChangePassword(ctx context.Context, id, newHash string, now time.Time) error

	// RecentPasswordHashes returns the current hash first, then the previous one
	// if there is one: one or two entries. A missing user is ErrNotFound.
	RecentPasswordHashes(ctx context.Context, id string) ([]string, error)

	// RecordLoginFailure adds 1 to FailedLogins in one atomic step, so
	// concurrent calls never lose a count. When the count reaches maxFailures
	// the same step sets LockedUntil to now+lockFor, sets FailedLogins back to 0
	// and the call returns locked=true; otherwise locked is false. Callers pass
	// maxFailures >= 1 and lockFor > 0. A missing user is ErrNotFound.
	RecordLoginFailure(ctx context.Context, id string, now time.Time, maxFailures int, lockFor time.Duration) (locked bool, err error)

	// ResetLoginFailures sets FailedLogins to 0 and leaves LockedUntil alone:
	// a lock ends when it expires or when the password changes. A missing user
	// is ErrNotFound.
	ResetLoginFailures(ctx context.Context, id string) error

	// ListAdminEmails returns the email of every user with IsAdmin set and
	// status active, in ascending order.
	ListAdminEmails(ctx context.Context) ([]string, error)

	// Delete removes the user and, with it, the user's sessions, tokens and
	// memberships. Every "who did it" reference to the user then reads as empty:
	// User.ApprovedBy, Collective.CreatedBy, Collective.ApprovedBy,
	// Membership.AdminApprovedBy and Membership.OwnerApprovedBy. The collectives
	// and the other users that pointed at it stay. A missing user is ErrNotFound.
	Delete(ctx context.Context, id string) error
}

// Tokens stores one-time tokens, looked up by the SHA-256 of the token.
type Tokens interface {
	// Create validates t and returns its FieldErrors, assigns t.ID (an ID the
	// caller set is ignored) and stores every other field as given. The user
	// t.UserID must exist, else ErrNotFound. Before storing t, it marks every
	// token of the same user and purpose that is still unused as used, with
	// UsedAt set to t.CreatedAt, so only the newest link works. A token whose
	// Hash is already stored is ErrConflict, and then nothing changes: the
	// earlier tokens are not retired.
	Create(ctx context.Context, t *Token) error

	// Consume finds the token with this purpose and hash that is unused and has
	// ExpiresAt after now (strictly), sets its UsedAt to now and returns it as
	// stored after that change. It is atomic: when calls race for one token,
	// exactly one succeeds and the others get ErrNotFound. Every other case
	// (unknown hash, other purpose, used, expired) is ErrNotFound and consumes
	// nothing.
	Consume(ctx context.Context, purpose TokenPurpose, hash []byte, now time.Time) (Token, error)
}

// Sessions stores logins, identified by the SHA-256 of the cookie value.
type Sessions interface {
	// Create validates s and returns its FieldErrors, then stores it. The user
	// s.UserID must exist, else ErrNotFound. A session with the same Hash is
	// ErrConflict.
	Create(ctx context.Context, s Session) error

	// Get returns the session even when it has expired: checking ExpiresAt is
	// the caller's job. An unknown hash is ErrNotFound.
	Get(ctx context.Context, hash []byte) (Session, error)

	// Touch sets LastSeenAt and ExpiresAt. An unknown hash is ErrNotFound.
	Touch(ctx context.Context, hash []byte, lastSeen, expiresAt time.Time) error

	// Delete removes the session. It is idempotent: an unknown hash is not an
	// error.
	Delete(ctx context.Context, hash []byte) error

	// DeleteForUser removes every session of the user except the one whose hash
	// is keep; a nil or empty keep removes them all. A user without sessions, or
	// an unknown user, is not an error.
	DeleteForUser(ctx context.Context, userID string, keep []byte) error

	// DeleteExpired removes every session whose ExpiresAt is not after now (a
	// session is expired when ExpiresAt <= now, the same rule Tokens.Consume
	// uses) and returns how many it removed.
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}

// Collectives stores party collectives and production companies.
type Collectives interface {
	// Create validates c and returns its FieldErrors, assigns c.ID (an ID the
	// caller set is ignored) and stores every other field as given. It returns
	// ErrNotFound when c.CreatedBy or c.ApprovedBy is not empty and names no
	// user, and ErrNameTaken when another collective, in any status, has the
	// same NameKey.
	Create(ctx context.Context, c *Collective) error

	// ByID returns the collective, or ErrNotFound.
	ByID(ctx context.Context, id string) (Collective, error)

	// ByNameKey returns the collective whose NameKey is exactly key, or
	// ErrNotFound. Callers pass NameKey(name).
	ByNameKey(ctx context.Context, key string) (Collective, error)

	// SearchApproved returns the collectives with status approved whose NameKey
	// contains NameKey(query) as a plain substring (an empty key matches all;
	// the query is never a pattern, so ".", "(" or ".*" only match themselves).
	// A kind that is not empty restricts the result to that kind. The result is
	// in ascending NameKey order (byte order) and has at most limit entries,
	// with limit clamped to the range 1 to 50.
	SearchApproved(ctx context.Context, query string, kind CollectiveKind, limit int) ([]Collective, error)

	// Delete removes the collective and its memberships; the users stay. A
	// missing collective is ErrNotFound.
	Delete(ctx context.Context, id string) error
}

// MembershipView is a membership with the fields of its collective that a list
// of the user's memberships shows.
type MembershipView struct {
	Membership
	CollectiveName   string
	CollectiveKind   CollectiveKind
	CollectiveStatus CollectiveStatus
}

// Memberships stores the links between users and collectives.
type Memberships interface {
	// Create validates m and returns its FieldErrors, then stores it. The user
	// m.UserID and the collective m.CollectiveID must exist, and so must
	// m.AdminApprovedBy and m.OwnerApprovedBy when they are not empty, else
	// ErrNotFound. A second membership for the same user and collective, whatever
	// its role or status, is ErrAlreadyMember.
	Create(ctx context.Context, m Membership) error

	// ListByUser returns every membership of the user, whatever its status, in
	// ascending order of the collective's NameKey. An unknown user has none.
	ListByUser(ctx context.Context, userID string) ([]MembershipView, error)

	// OwnerEmails returns the email of every user with status active that has a
	// membership with role owner and status approved in the collective, in
	// ascending order. The collective's own status is not considered. An unknown
	// collective has none.
	OwnerEmails(ctx context.Context, collectiveID string) ([]string, error)

	// UserCanCreateEvents reports whether the user has status active and holds a
	// membership with role owner and status approved in a collective with status
	// approved. An unknown user cannot.
	UserCanCreateEvents(ctx context.Context, userID string) (bool, error)
}

// Outbox stores the emails waiting to be sent. A worker claims messages, sends
// them and records the result; a claim is a lease, so a worker that dies
// does not hold its messages forever.
type Outbox interface {
	// Enqueue validates m and returns its FieldErrors, assigns m.ID (an ID the
	// caller set is ignored), sets m.Attempts to 0 and stores every other field
	// as given.
	Enqueue(ctx context.Context, m *OutboxMessage) error

	// Claim returns at most limit messages that are not sent, not given up, due
	// (NextAttemptAt <= now) and not leased (ClaimedUntil unset or <= now), the
	// oldest NextAttemptAt first, and sets ClaimedUntil to now+lease on each of
	// them in the same atomic step as it selects them: a concurrent or later
	// Claim does not return a message while its lease holds, and returns it again
	// once the lease has run out. Claim does not change Attempts. A limit below 1
	// returns nothing and claims nothing.
	Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]OutboxMessage, error)

	// MarkSent sets SentAt to now, so the message is never claimed again. An
	// unknown ID is ErrNotFound.
	MarkSent(ctx context.Context, id string, now time.Time) error

	// MarkFailed adds 1 to Attempts, sets LastError (cut to its first 500
	// runes) and clears the lease. With retryAt it sets NextAttemptAt to
	// *retryAt; with nil retryAt it sets GaveUp and the message is never claimed
	// again. An unknown ID is ErrNotFound.
	MarkFailed(ctx context.Context, id string, lastError string, retryAt *time.Time) error
}

// Repos bundles the repositories of one store, or of one transaction.
type Repos struct {
	Users       Users
	Tokens      Tokens
	Sessions    Sessions
	Collectives Collectives
	Memberships Memberships
	Outbox      Outbox
}

// Store is a database backend.
type Store interface {
	// Repos returns the repositories. Each call on them runs by itself, outside
	// any transaction.
	Repos() Repos

	// WithTx runs fn in a transaction and commits when fn returns nil. When fn
	// returns an error, everything fn did through r is rolled back and WithTx
	// returns that error (as it is, or wrapping it, so errors.Is and errors.As
	// still match); fn is not run again for it.
	//
	// fn may run more than once, because the backend retries a transaction that
	// failed for a transient reason, so it must have no side effects outside the
	// store (no email sent, no counter changed) and must not keep state from one
	// run to the next. Inside fn:
	//
	//   - use only r, never Store.Repos, which would run outside the transaction;
	//   - reads see what fn already wrote;
	//   - an ErrNotFound from any call (a read, or an update or delete that
	//     matches nothing) leaves the transaction usable;
	//   - any other error from a write (ErrEmailTaken, ErrNameTaken, ErrAlreadyMember,
	//     ErrConflict) may leave it unusable, as it does in MongoDB: fn returns
	//     that error instead of carrying on.
	WithTx(ctx context.Context, fn func(ctx context.Context, r Repos) error) error

	// Ping returns nil when the database answers.
	Ping(ctx context.Context) error

	// Close releases the connections. The Store and its repositories must not
	// be used after it. Closing a Store that is already closed returns nil.
	Close(ctx context.Context) error
}
