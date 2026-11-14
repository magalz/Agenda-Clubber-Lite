package events

import (
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// TestPermissions checks mayCreate and mayEdit against the permission table of
// the package doc, one row per case.
func TestPermissions(t *testing.T) {
	active := domain.User{Status: domain.UserStatusActive}
	admin := domain.User{Status: domain.UserStatusActive, IsAdmin: true}
	disabled := domain.User{Status: domain.UserStatusDisabled}
	disabledAdmin := domain.User{Status: domain.UserStatusDisabled, IsAdmin: true}

	approved := domain.Collective{Status: domain.CollectiveStatusApproved}
	blocked := domain.Collective{Status: domain.CollectiveStatusApproved, EventsBlocked: true}
	pending := domain.Collective{Status: domain.CollectiveStatusPending}

	owner := &domain.Membership{Role: domain.RoleOwner, Status: domain.MembershipStatusApproved}
	member := &domain.Membership{Role: domain.RoleMember, Status: domain.MembershipStatusApproved}
	pendingOwner := &domain.Membership{Role: domain.RoleOwner, Status: domain.MembershipStatusPending}

	tests := []struct {
		name         string
		actor        domain.User
		c            domain.Collective
		m            *domain.Membership
		create, edit error
	}{
		{"Administrador", active, approved, owner, nil, nil},
		{"Administrador of a blocked collective", active, blocked, owner, ErrBlocked, ErrBlocked},
		{"Administrador of a pending collective", active, pending, owner, ErrNotAllowed, ErrNotAllowed},
		{"Administrador with a disabled account", disabled, approved, owner, ErrNotAllowed, ErrNotAllowed},
		{"pending Administrador", active, approved, pendingOwner, ErrNotAllowed, ErrNotAllowed},
		{"Membro", active, approved, member, ErrNotAllowed, ErrNotAllowed},
		{"Membro of a blocked collective", active, blocked, member, ErrNotAllowed, ErrNotAllowed},
		{"no membership", active, approved, nil, ErrNotAllowed, ErrNotAllowed},
		{"admin without membership", admin, approved, nil, ErrNotAllowed, nil},
		{"admin, blocked collective", admin, blocked, nil, ErrNotAllowed, nil},
		{"admin as Administrador of a blocked collective", admin, blocked, owner, nil, nil},
		{"admin as Membro", admin, approved, member, ErrNotAllowed, nil},
		{"disabled admin", disabledAdmin, approved, owner, ErrNotAllowed, ErrNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mayCreate(tt.actor, tt.c, tt.m); got != tt.create {
				t.Errorf("mayCreate = %v, want %v", got, tt.create)
			}
			if got := mayEdit(tt.actor, tt.c, tt.m); got != tt.edit {
				t.Errorf("mayEdit = %v, want %v", got, tt.edit)
			}
		})
	}
}
