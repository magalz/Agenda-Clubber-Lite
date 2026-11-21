package events

import (
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// TestNewWorld checks that the shared cast is stored as described on world, so
// the flows' tests start from a known state.
func TestNewWorld(t *testing.T) {
	w := newWorld(t)
	r := w.S.Repos()
	for _, tc := range []struct {
		who  string
		user domain.User
		want bool
	}{
		{"owner", w.Owner, true},
		{"member", w.Member, false},
		{"outsider", w.Outsider, false},
		{"admin", w.Admin, false},
	} {
		got, err := r.Memberships.UserCanCreateEvents(t.Context(), tc.user.ID)
		if err != nil || got != tc.want {
			t.Errorf("UserCanCreateEvents(%s) = %v, %v; want %v", tc.who, got, err, tc.want)
		}
	}
	if !w.Delta.EventsBlocked || w.Alfa.EventsBlocked {
		t.Errorf("blocks: Alfa %v, Delta %v; want only Delta blocked", w.Alfa.EventsBlocked, w.Delta.EventsBlocked)
	}
}
