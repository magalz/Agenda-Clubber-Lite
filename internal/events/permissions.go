package events

import "github.com/magalz/agenda-clubber-lite/internal/domain"

// The permission table of the package doc, as two checks. The write flows call
// them inside their transaction with what they just read, and the views call
// them to decide which buttons to show, so a page offers exactly what the
// server allows. m is the actor's membership in c, or nil when there is none.

// mayCreate answers whether actor may create an event in c: nil, ErrNotAllowed
// or ErrBlocked. Only an approved Administrador of an approved collective
// creates, with an active account; the site admin creates only as such an
// Administrador, but the event block never stops the admin.
func mayCreate(actor domain.User, c domain.Collective, m *domain.Membership) error {
	if actor.Status != domain.UserStatusActive || c.Status != domain.CollectiveStatusApproved || !approvedOwner(m) {
		return ErrNotAllowed
	}
	if c.EventsBlocked && !actor.IsAdmin {
		return ErrBlocked
	}
	return nil
}

// mayEdit answers whether actor may edit or delete an event of c: nil,
// ErrNotAllowed or ErrBlocked. The site admin may, on any collective and despite
// the event block; otherwise only an approved Administrador of the approved
// collective, and not while its events are blocked. The account must be active.
func mayEdit(actor domain.User, c domain.Collective, m *domain.Membership) error {
	if actor.Status != domain.UserStatusActive {
		return ErrNotAllowed
	}
	if actor.IsAdmin {
		return nil
	}
	if c.Status != domain.CollectiveStatusApproved || !approvedOwner(m) {
		return ErrNotAllowed
	}
	if c.EventsBlocked {
		return ErrBlocked
	}
	return nil
}

// approvedOwner reports whether m is an approved membership with the role owner
// (an Administrador).
func approvedOwner(m *domain.Membership) bool {
	return m != nil && m.Role == domain.RoleOwner && m.Status == domain.MembershipStatusApproved
}
