package mongostore

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Stubs that make the package compile for the failing tests; the next commit
// replaces them.

type stubUsers struct{}

func (stubUsers) Create(context.Context, *domain.User) error { return errNotImplemented }
func (stubUsers) ByID(context.Context, string) (domain.User, error) {
	return domain.User{}, errNotImplemented
}
func (stubUsers) ByEmail(context.Context, string) (domain.User, error) {
	return domain.User{}, errNotImplemented
}
func (stubUsers) SetStatus(context.Context, string, domain.UserStatus, time.Time) error {
	return errNotImplemented
}
func (stubUsers) MarkEmailVerified(context.Context, string, time.Time) error {
	return errNotImplemented
}
func (stubUsers) ChangePassword(context.Context, string, string, time.Time) error {
	return errNotImplemented
}
func (stubUsers) RecentPasswordHashes(context.Context, string) ([]string, error) {
	return nil, errNotImplemented
}
func (stubUsers) RecordLoginFailure(context.Context, string, time.Time, int, time.Duration) (bool, error) {
	return false, errNotImplemented
}
func (stubUsers) ResetLoginFailures(context.Context, string) error { return errNotImplemented }
func (stubUsers) ListAdminEmails(context.Context) ([]string, error) {
	return nil, errNotImplemented
}
func (stubUsers) Delete(context.Context, string) error { return errNotImplemented }

type stubTokens struct{}

func (stubTokens) Create(context.Context, *domain.Token) error { return errNotImplemented }
func (stubTokens) Consume(context.Context, domain.TokenPurpose, []byte, time.Time) (domain.Token, error) {
	return domain.Token{}, errNotImplemented
}

type stubSessions struct{}

func (stubSessions) Create(context.Context, domain.Session) error { return errNotImplemented }
func (stubSessions) Get(context.Context, []byte) (domain.Session, error) {
	return domain.Session{}, errNotImplemented
}
func (stubSessions) Touch(context.Context, []byte, time.Time, time.Time) error {
	return errNotImplemented
}
func (stubSessions) Delete(context.Context, []byte) error { return errNotImplemented }
func (stubSessions) DeleteForUser(context.Context, string, []byte) error {
	return errNotImplemented
}
func (stubSessions) DeleteExpired(context.Context, time.Time) (int, error) {
	return 0, errNotImplemented
}

type stubCollectives struct{}

func (stubCollectives) Create(context.Context, *domain.Collective) error { return errNotImplemented }
func (stubCollectives) ByID(context.Context, string) (domain.Collective, error) {
	return domain.Collective{}, errNotImplemented
}
func (stubCollectives) ByNameKey(context.Context, string) (domain.Collective, error) {
	return domain.Collective{}, errNotImplemented
}
func (stubCollectives) SearchApproved(context.Context, string, domain.CollectiveKind, int) ([]domain.Collective, error) {
	return nil, errNotImplemented
}
func (stubCollectives) Delete(context.Context, string) error { return errNotImplemented }

type stubMemberships struct{}

func (stubMemberships) Create(context.Context, domain.Membership) error { return errNotImplemented }
func (stubMemberships) ListByUser(context.Context, string) ([]domain.MembershipView, error) {
	return nil, errNotImplemented
}
func (stubMemberships) OwnerEmails(context.Context, string) ([]string, error) {
	return nil, errNotImplemented
}
func (stubMemberships) UserCanCreateEvents(context.Context, string) (bool, error) {
	return false, errNotImplemented
}
