// Package storetest is the contract suite every domain.Store implementation
// must pass. It lives in a regular (non-test) package so that a backend's own
// tests can import it:
//
//	func TestStore(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) domain.Store {
//			// return a new, empty, isolated store; register its cleanup with t.Cleanup
//		})
//	}
//
// Run calls every group; RunUsers, RunTokens, RunSessions, RunCollectives,
// RunMemberships, RunOutbox and RunTx run one group each. A group is a set of
// subtests and each subtest gets a store of its own from the NewStore function.
//
// The suite checks the behavior written in the doc comments of the interfaces in
// internal/domain, and nothing the comments leave open. Errors are compared with
// errors.Is (FieldErrors with errors.As) and times with Truncate(time.Millisecond)
// and Equal, as the contract says.
package storetest

import (
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// NewStore returns an empty store that no other test shares. The function that
// implements it registers the store's cleanup with t.Cleanup.
type NewStore func(t *testing.T) domain.Store

// Run runs the whole suite.
func Run(t *testing.T, newStore NewStore) {
	t.Helper()
	t.Run("Users", func(t *testing.T) { RunUsers(t, newStore) })
	t.Run("Tokens", func(t *testing.T) { RunTokens(t, newStore) })
	t.Run("Sessions", func(t *testing.T) { RunSessions(t, newStore) })
	t.Run("Collectives", func(t *testing.T) { RunCollectives(t, newStore) })
	t.Run("Memberships", func(t *testing.T) { RunMemberships(t, newStore) })
	t.Run("Outbox", func(t *testing.T) { RunOutbox(t, newStore) })
	t.Run("Tx", func(t *testing.T) { RunTx(t, newStore) })
}
