package storetest

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const leaseFor = 10 * time.Minute

func (e env) claim(now time.Time, lease time.Duration, limit int) []domain.OutboxMessage {
	e.t.Helper()
	got, err := e.r.Outbox.Claim(e.ctx, now, lease, limit)
	noErr(e.t, fmt.Sprintf("Claim(%v, %v, %d)", now, lease, limit), err)
	return got
}

// RunOutbox runs the contract of domain.Outbox.
func RunOutbox(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"Enqueue assigns an ID, sets Attempts to 0 and stores every other field as given", func(t *testing.T, e env) {
			m := newMessage("Ana@Example.com", func(m *domain.OutboxMessage) {
				m.ID = "chosen-by-the-caller"
				m.Attempts = 3
				m.NextAttemptAt = at(-time.Minute)
				m.CreatedAt = at(-time.Hour)
			})
			noErr(t, "Enqueue", e.r.Outbox.Enqueue(e.ctx, &m))
			if m.ID == "" || m.ID == "chosen-by-the-caller" {
				t.Fatalf("ID = %q, want one assigned by the store", m.ID)
			}
			if m.Attempts != 0 {
				t.Errorf("Attempts = %d after Enqueue, want 0", m.Attempts)
			}
			second := e.message("bia@example.com")
			if second.ID == "" || second.ID == m.ID {
				t.Errorf("second ID = %q, want one that differs from %q", second.ID, m.ID)
			}

			got := e.claim(base, leaseFor, 1)
			if len(got) != 1 {
				t.Fatalf("Claim returned %d messages, want 1", len(got))
			}
			want := m
			want.ClaimedUntil = ptr(base.Add(leaseFor))
			wantEqual(t, "claimed message", normMessage(got[0]), normMessage(want))
		}},
		{"Enqueue validates the message and stores nothing", func(t *testing.T, e env) {
			for _, tc := range []struct {
				field  string
				mutate func(*domain.OutboxMessage)
			}{
				{"to", func(m *domain.OutboxMessage) { m.To = "not an email" }},
				{"subject", func(m *domain.OutboxMessage) { m.Subject = "" }},
				{"text", func(m *domain.OutboxMessage) { m.Text = "  " }},
				{"attempts", func(m *domain.OutboxMessage) { m.Attempts = -1 }},
			} {
				m := newMessage("ana@example.com", tc.mutate)
				wantFieldErrors(t, "Enqueue with a bad "+tc.field, e.r.Outbox.Enqueue(e.ctx, &m), tc.field)
			}
			if got := e.claim(at(1000*time.Hour), leaseFor, 10); len(got) != 0 {
				t.Errorf("Claim returned %d messages after the failed Enqueues, want none", len(got))
			}
		}},
		{"Claim returns the due messages, oldest NextAttemptAt first", func(t *testing.T, e env) {
			m2 := e.message("b@example.com", due(-time.Minute))
			m1 := e.message("a@example.com", due(-time.Hour))
			m3 := e.message("c@example.com", due(-2*time.Minute))
			got := e.claim(base, leaseFor, 10)
			wantIDs(t, "Claim", got, m1, m3, m2)
		}},
		{"Claim returns a message that is due now and not one due later", func(t *testing.T, e env) {
			now := e.message("a@example.com", due(0))
			e.message("b@example.com", due(time.Millisecond))
			wantIDs(t, "Claim", e.claim(base, leaseFor, 10), now)
		}},
		{"Claim leases the messages it returns until the lease runs out", func(t *testing.T, e env) {
			m := e.message("a@example.com")
			first := e.claim(base, leaseFor, 10)
			wantIDs(t, "first Claim", first, m)
			sameTimePtr(t, "ClaimedUntil", first[0].ClaimedUntil, base.Add(leaseFor))

			wantIDs(t, "Claim at the same time", e.claim(base, leaseFor, 10))
			wantIDs(t, "Claim just before the lease runs out", e.claim(base.Add(leaseFor-time.Millisecond), leaseFor, 10))

			again := e.claim(base.Add(leaseFor), leaseFor, 10)
			wantIDs(t, "Claim when the lease runs out", again, m)
			sameTimePtr(t, "new ClaimedUntil", again[0].ClaimedUntil, base.Add(2*leaseFor))
		}},
		{"Claim returns at most limit messages", func(t *testing.T, e env) {
			var all []domain.OutboxMessage
			for i := 5; i >= 1; i-- {
				all = append(all, e.message(fmt.Sprintf("m%d@example.com", i), due(-time.Duration(i)*time.Minute)))
			}
			wantIDs(t, "first Claim", e.claim(base, leaseFor, 2), all[0], all[1])
			wantIDs(t, "second Claim", e.claim(base, leaseFor, 2), all[2], all[3])
			wantIDs(t, "third Claim", e.claim(base, leaseFor, 2), all[4])
			wantIDs(t, "fourth Claim", e.claim(base, leaseFor, 2))
		}},
		{"Claim with a limit below 1 returns and claims nothing", func(t *testing.T, e env) {
			a, b := e.message("a@example.com", due(-time.Minute)), e.message("b@example.com")
			for _, limit := range []int{0, -1} {
				wantIDs(t, fmt.Sprintf("Claim with limit %d", limit), e.claim(base, leaseFor, limit))
			}
			wantIDs(t, "Claim with a limit", e.claim(base, leaseFor, 10), a, b)
		}},
		{"Claim skips the messages that were sent or given up", func(t *testing.T, e env) {
			sent, gaveUp, live := e.message("a@example.com"), e.message("b@example.com"), e.message("c@example.com")
			noErr(t, "MarkSent", e.r.Outbox.MarkSent(e.ctx, sent.ID, base))
			noErr(t, "MarkFailed", e.r.Outbox.MarkFailed(e.ctx, gaveUp.ID, "smtp refused", nil))
			wantIDs(t, "Claim", e.claim(at(1000*time.Hour), leaseFor, 10), live)
		}},
		{"Claim does not change Attempts", func(t *testing.T, e env) {
			e.message("a@example.com")
			for i := range 3 {
				got := e.claim(at(time.Duration(i)*leaseFor), leaseFor, 10)
				if len(got) != 1 || got[0].Attempts != 0 {
					t.Fatalf("Claim %d = %+v, want the message with Attempts 0", i, got)
				}
			}
		}},
		{"concurrent Claims never return the same message twice", func(t *testing.T, e env) {
			want := map[string]bool{}
			for i := range 10 {
				want[e.message(fmt.Sprintf("m%d@example.com", i)).ID] = true
			}
			results := parallel(5, func(int) ([]domain.OutboxMessage, error) {
				return e.r.Outbox.Claim(e.ctx, base, time.Hour, 3)
			})
			seen := map[string]bool{}
			count := 0
			for _, r := range results {
				noErr(t, "Claim", r.err)
				if len(r.val) > 3 {
					t.Errorf("a Claim returned %d messages, want at most 3", len(r.val))
				}
				for _, m := range r.val {
					count++
					if seen[m.ID] {
						t.Errorf("message %s was claimed twice", m.ID)
					}
					seen[m.ID] = true
				}
			}
			for _, m := range e.claim(base, time.Hour, 20) {
				count++
				if seen[m.ID] {
					t.Errorf("message %s was claimed twice", m.ID)
				}
				seen[m.ID] = true
			}
			if count != len(want) {
				t.Errorf("the Claims returned %d messages in all, want %d", count, len(want))
			}
			for id := range want {
				if !seen[id] {
					t.Errorf("message %s was never claimed", id)
				}
			}
		}},
		{"MarkSent stops the message from being claimed", func(t *testing.T, e env) {
			m := e.message("a@example.com")
			wantIDs(t, "Claim", e.claim(base, leaseFor, 10), m)
			noErr(t, "MarkSent", e.r.Outbox.MarkSent(e.ctx, m.ID, at(time.Minute)))
			wantIDs(t, "Claim after MarkSent", e.claim(at(1000*time.Hour), leaseFor, 10))
		}},
		{"MarkSent returns ErrNotFound for an unknown ID", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("MarkSent(%q)", id), e.r.Outbox.MarkSent(e.ctx, id, base), domain.ErrNotFound)
			}
		}},
		{"MarkFailed with a retry time counts the attempt, clears the lease and reschedules", func(t *testing.T, e env) {
			m := e.message("a@example.com")
			wantIDs(t, "Claim", e.claim(base, time.Hour, 10), m)

			retry := at(time.Minute)
			noErr(t, "MarkFailed", e.r.Outbox.MarkFailed(e.ctx, m.ID, "smtp timeout", &retry))

			wantIDs(t, "Claim before the retry time", e.claim(at(30*time.Second), time.Hour, 10))
			got := e.claim(at(2*time.Minute), time.Hour, 10)
			wantIDs(t, "Claim after the retry time", got, m)
			if got[0].Attempts != 1 || got[0].LastError != "smtp timeout" || got[0].GaveUp {
				t.Errorf("message = %+v, want Attempts 1, LastError %q and not given up", got[0], "smtp timeout")
			}
			sameTime(t, "NextAttemptAt", got[0].NextAttemptAt, retry)
			unsetTime(t, "SentAt", got[0].SentAt)
		}},
		{"MarkFailed adds up the attempts", func(t *testing.T, e env) {
			m := e.message("a@example.com")
			for i := 1; i <= 3; i++ {
				retry := at(time.Duration(i) * time.Minute)
				noErr(t, "MarkFailed", e.r.Outbox.MarkFailed(e.ctx, m.ID, fmt.Sprintf("failure %d", i), &retry))
			}
			got := e.claim(at(time.Hour), leaseFor, 10)
			if len(got) != 1 || got[0].Attempts != 3 || got[0].LastError != "failure 3" {
				t.Errorf("Claim = %+v, want the message with Attempts 3 and the last error", got)
			}
		}},
		{"MarkFailed without a retry time gives the message up", func(t *testing.T, e env) {
			m := e.message("a@example.com")
			noErr(t, "MarkFailed", e.r.Outbox.MarkFailed(e.ctx, m.ID, "mailbox does not exist", nil))
			wantIDs(t, "Claim", e.claim(at(1000*time.Hour), leaseFor, 10))
		}},
		{"MarkFailed cuts LastError to 500 runes", func(t *testing.T, e env) {
			m := e.message("a@example.com")
			long := strings.Repeat("é", 400) + strings.Repeat("ã", 300)
			retry := at(time.Minute)
			noErr(t, "MarkFailed", e.r.Outbox.MarkFailed(e.ctx, m.ID, long, &retry))
			got := e.claim(at(time.Hour), leaseFor, 10)
			if len(got) != 1 {
				t.Fatalf("Claim returned %d messages, want 1", len(got))
			}
			if n := utf8.RuneCountInString(got[0].LastError); n != 500 {
				t.Errorf("LastError has %d runes, want 500", n)
			}
			if want := string([]rune(long)[:500]); got[0].LastError != want {
				t.Errorf("LastError is not the first 500 runes of the error")
			}
		}},
		{"MarkFailed returns ErrNotFound for an unknown ID", func(t *testing.T, e env) {
			retry := at(time.Minute)
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("MarkFailed(%q)", id), e.r.Outbox.MarkFailed(e.ctx, id, "boom", &retry), domain.ErrNotFound)
				isErr(t, fmt.Sprintf("MarkFailed(%q) without retry", id), e.r.Outbox.MarkFailed(e.ctx, id, "boom", nil), domain.ErrNotFound)
			}
		}},
	})
}
