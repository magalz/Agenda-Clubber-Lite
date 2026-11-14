package events

import "github.com/magalz/agenda-clubber-lite/internal/domain"

// mayCreate answers whether actor may create an event in c: nil, ErrNotAllowed
// or ErrBlocked. Stub until the rule is written.
func mayCreate(actor domain.User, c domain.Collective, m *domain.Membership) error { return nil }

// mayEdit answers whether actor may edit or delete an event of c: nil,
// ErrNotAllowed or ErrBlocked. Stub until the rule is written.
func mayEdit(actor domain.User, c domain.Collective, m *domain.Membership) error { return nil }
