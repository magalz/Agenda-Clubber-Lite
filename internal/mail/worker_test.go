package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// These tests need MONGO_TEST_URI (see mongotest); without it they skip.

// workerT0 is a whole second, so MongoDB's millisecond precision changes nothing.
var workerT0 = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

var discardLog = slog.New(slog.DiscardHandler)

// fakeClock is a clock the test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// fakeSender records every Send. send decides the result; nil succeeds.
type fakeSender struct {
	mu    sync.Mutex
	calls []domain.OutboxMessage
	send  func(ctx context.Context, m domain.OutboxMessage) error
}

func (f *fakeSender) Send(ctx context.Context, m domain.OutboxMessage) error {
	f.mu.Lock()
	f.calls = append(f.calls, m)
	send := f.send
	f.mu.Unlock()
	if send == nil {
		return nil
	}
	return send(ctx, m)
}

func (f *fakeSender) sent() []domain.OutboxMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// flakyOutbox fails the calls whose error is set and passes the rest on.
type flakyOutbox struct {
	domain.Outbox
	claimErr, sentErr, failedErr error
}

func (f flakyOutbox) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.OutboxMessage, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	return f.Outbox.Claim(ctx, now, lease, limit)
}

func (f flakyOutbox) MarkSent(ctx context.Context, id string, now time.Time) error {
	if f.sentErr != nil {
		return f.sentErr
	}
	return f.Outbox.MarkSent(ctx, id, now)
}

func (f flakyOutbox) MarkFailed(ctx context.Context, id, lastError string, retryAt *time.Time) error {
	if f.failedErr != nil {
		return f.failedErr
	}
	return f.Outbox.MarkFailed(ctx, id, lastError, retryAt)
}

// syncBuffer is a log destination that several goroutines may write.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newOutbox(t *testing.T) domain.Outbox {
	t.Helper()
	return mongotest.New(t).Repos().Outbox
}

// enqueue stores a message for to that is due at next. Its subject and bodies
// are distinctive, so a test can look for them in the logs.
func enqueue(t *testing.T, ob domain.Outbox, to string, next time.Time) domain.OutboxMessage {
	t.Helper()
	m := domain.OutboxMessage{
		To:            to,
		Subject:       "Assunto secreto de " + to,
		Text:          "Corpo secreto de " + to + ": https://agenda.example/confirmar/token-" + to,
		HTML:          "<p>Corpo secreto de " + to + "</p>",
		NextAttemptAt: next,
		CreatedAt:     next,
	}
	if err := ob.Enqueue(t.Context(), &m); err != nil {
		t.Fatalf("enqueue for %s: %v", to, err)
	}
	return m
}

// peek returns the oldest message that is due at at, or none. The outbox has no
// Get, so Claim is the only way to look. A lease of 0 has run out when Claim
// returns, so peeking leaves nothing leased; the limit is 1 because a call that
// leases for 0 may return the same message again.
func peek(t *testing.T, ob domain.Outbox, at time.Time) []domain.OutboxMessage {
	t.Helper()
	msgs, err := ob.Claim(t.Context(), at, 0, 1)
	if err != nil {
		t.Fatalf("peek at %s: %v", at.Format(time.RFC3339), err)
	}
	return msgs
}

func newWorker(ob domain.Outbox, s Sender, c *fakeClock, log *slog.Logger) *Worker {
	return &Worker{Outbox: ob, Sender: s, Log: log, Now: c.Now}
}

func wantRun(t *testing.T, w *Worker, ctx context.Context, wantSent, wantFailed int) {
	t.Helper()
	sent, failed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error = %v, want nil", err)
	}
	if sent != wantSent || failed != wantFailed {
		t.Fatalf("RunOnce = sent %d, failed %d; want sent %d, failed %d", sent, failed, wantSent, wantFailed)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitReturn(t *testing.T, done <-chan struct{}, within time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("%s did not return within %s", what, within)
	}
}

func TestWorkerSendsADueMessageOnce(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{}
	w := newWorker(ob, sender, clock, discardLog)

	due := enqueue(t, ob, "ana@example.com", workerT0)
	later := enqueue(t, ob, "bia@example.com", workerT0.Add(time.Hour))

	wantRun(t, w, t.Context(), 1, 0)
	got := sender.sent()
	if len(got) != 1 {
		t.Fatalf("Send called %d times, want 1", len(got))
	}
	if got[0].ID != due.ID || got[0].To != due.To || got[0].Subject != due.Subject || got[0].Text != due.Text || got[0].HTML != due.HTML {
		t.Errorf("Send got %+v, want the due message %+v", got[0], due)
	}

	wantRun(t, w, t.Context(), 0, 0)
	if n := len(sender.sent()); n != 1 {
		t.Errorf("Send called %d times after a second run, want 1: a sent message is claimed again", n)
	}

	// Of the two, only the message that was not due is still waiting.
	rest := peek(t, ob, workerT0.Add(24*time.Hour))
	if len(rest) != 1 || rest[0].ID != later.ID {
		t.Errorf("messages left in the outbox = %+v, want only %s", rest, later.ID)
	}
}

func TestWorkerSkipsALeasedMessageUntilItsLeaseRunsOut(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{}
	w := newWorker(ob, sender, clock, discardLog)

	m := enqueue(t, ob, "ana@example.com", workerT0)
	// Another worker claims it for 5 minutes.
	if got, err := ob.Claim(t.Context(), workerT0, 5*time.Minute, 1); err != nil || len(got) != 1 {
		t.Fatalf("claim by the other worker = %v, %v; want 1 message", got, err)
	}

	clock.Set(workerT0.Add(time.Minute))
	wantRun(t, w, t.Context(), 0, 0)
	clock.Set(workerT0.Add(5*time.Minute - time.Millisecond))
	wantRun(t, w, t.Context(), 0, 0)
	if n := len(sender.sent()); n != 0 {
		t.Fatalf("Send called %d times while the lease held, want 0", n)
	}

	clock.Set(workerT0.Add(5 * time.Minute))
	wantRun(t, w, t.Context(), 1, 0)
	if got := sender.sent(); len(got) != 1 || got[0].ID != m.ID {
		t.Errorf("Send got %+v, want the message %s once the lease ran out", got, m.ID)
	}
}

func TestWorkerRetrySchedule(t *testing.T) {
	want := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	if !slices.Equal(RetryDelays, want) {
		t.Fatalf("RetryDelays = %v, want %v", RetryDelays, want)
	}
	if MaxAttempts != len(want)+1 {
		t.Fatalf("MaxAttempts = %d, want %d", MaxAttempts, len(want)+1)
	}

	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	failure := errors.New("smtp: 451 try again later")
	sender := &fakeSender{send: func(context.Context, domain.OutboxMessage) error { return failure }}
	w := newWorker(ob, sender, clock, discardLog)
	m := enqueue(t, ob, "ana@example.com", workerT0)

	due := workerT0
	for i, delay := range want {
		clock.Set(due)
		wantRun(t, w, t.Context(), 0, 1)

		retryAt := due.Add(delay)
		if early := peek(t, ob, retryAt.Add(-time.Millisecond)); len(early) != 0 {
			t.Fatalf("attempt %d: the message is due before %s (retry delay %s)", i+1, retryAt.Format(time.RFC3339), delay)
		}
		got := peek(t, ob, retryAt)
		if len(got) != 1 {
			t.Fatalf("attempt %d: %d messages due at the retry time, want 1", i+1, len(got))
		}
		if got[0].ID != m.ID || got[0].Attempts != i+1 || got[0].LastError != failure.Error() || !got[0].NextAttemptAt.Equal(retryAt) || got[0].GaveUp {
			t.Fatalf("attempt %d: stored message = %+v, want attempts %d, last error %q, next attempt %s",
				i+1, got[0], i+1, failure.Error(), retryAt.Format(time.RFC3339))
		}
		due = retryAt
	}

	// The 6th attempt fails too, and the message is given up.
	clock.Set(due)
	wantRun(t, w, t.Context(), 0, 1)
	if n := len(sender.sent()); n != MaxAttempts {
		t.Errorf("Send called %d times, want %d", n, MaxAttempts)
	}
	far := due.Add(1000 * time.Hour)
	if left := peek(t, ob, far); len(left) != 0 {
		t.Fatalf("a message that gave up is claimed again: %+v", left)
	}
	clock.Set(far)
	wantRun(t, w, t.Context(), 0, 0)
	if n := len(sender.sent()); n != MaxAttempts {
		t.Errorf("Send called %d times after giving up, want %d", n, MaxAttempts)
	}
}

func TestWorkerCountsSentAndFailed(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{send: func(_ context.Context, m domain.OutboxMessage) error {
		if m.To == "bia@example.com" {
			return errors.New("smtp: 550 mailbox unavailable")
		}
		return nil
	}}
	w := newWorker(ob, sender, clock, discardLog)
	enqueue(t, ob, "ana@example.com", workerT0)
	enqueue(t, ob, "bia@example.com", workerT0)

	wantRun(t, w, t.Context(), 1, 1)
	if n := len(sender.sent()); n != 2 {
		t.Errorf("Send called %d times, want 2: a failure must not stop the run", n)
	}
}

func TestWorkerRecordsAFailureWhenCanceledMidSend(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	started := make(chan struct{}, 1)
	sender := &fakeSender{send: func(ctx context.Context, m domain.OutboxMessage) error {
		started <- struct{}{}
		<-ctx.Done()
		return fmt.Errorf("send: %w", ctx.Err())
	}}
	w := newWorker(ob, sender, clock, discardLog)
	m := enqueue(t, ob, "ana@example.com", workerT0)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		sent, failed int
		err          error
	}
	res := make(chan result, 1)
	go func() {
		sent, failed, err := w.RunOnce(ctx)
		res <- result{sent, failed, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the send never started")
	}
	cancel()

	select {
	case r := <-res:
		if r.err != nil || r.sent != 0 || r.failed != 1 {
			t.Fatalf("RunOnce = %+v, want sent 0, failed 1, no error", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunOnce did not return after the cancel")
	}

	if early := peek(t, ob, workerT0.Add(time.Minute-time.Millisecond)); len(early) != 0 {
		t.Fatalf("the message is due before the 1 minute retry: %+v", early)
	}
	got := peek(t, ob, workerT0.Add(time.Minute))
	if len(got) != 1 || got[0].ID != m.ID || got[0].Attempts != 1 || !strings.Contains(got[0].LastError, "context canceled") {
		t.Fatalf("stored message = %+v, want %s with attempts 1 and the cancel as its last error", got, m.ID)
	}
}

func TestWorkerBoundsEachSend(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{send: func(ctx context.Context, m domain.OutboxMessage) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	w := newWorker(ob, sender, clock, discardLog)
	w.SendTimeout = 50 * time.Millisecond
	enqueue(t, ob, "ana@example.com", workerT0)

	// A worker that ignored SendTimeout would wait for this context instead.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	wantRun(t, w, ctx, 0, 1)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("RunOnce took %s with a 50ms SendTimeout", took)
	}
	got := peek(t, ob, workerT0.Add(time.Minute))
	if len(got) != 1 || !strings.Contains(got[0].LastError, "deadline exceeded") {
		t.Fatalf("stored message = %+v, want the timeout as its last error", got)
	}
}

func TestWorkerLeaseDefaultsToFiveMinutes(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{}
	sender.send = func(context.Context, domain.OutboxMessage) error {
		// While the message is in flight the lease must hold, to the millisecond.
		if got := peek(t, ob, workerT0.Add(5*time.Minute-time.Millisecond)); len(got) != 0 {
			t.Errorf("the message is claimable 1ms before 5 minutes after the claim: %+v", got)
		}
		if got := peek(t, ob, workerT0.Add(5*time.Minute)); len(got) != 1 {
			t.Errorf("the message is not claimable 5 minutes after the claim: %+v", got)
		}
		return nil
	}
	w := newWorker(ob, sender, clock, discardLog)
	enqueue(t, ob, "ana@example.com", workerT0)

	wantRun(t, w, t.Context(), 1, 0)
}

func TestWorkerLogsNeverHoldTheMessage(t *testing.T) {
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	failure := errors.New("smtp: 451 try again later")
	sender := &fakeSender{send: func(_ context.Context, m domain.OutboxMessage) error {
		if m.To == "bia@example.com" {
			return failure
		}
		return nil
	}}
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w := newWorker(ob, sender, clock, log)
	ok := enqueue(t, ob, "ana@example.com", workerT0)
	bad := enqueue(t, ob, "bia@example.com", workerT0)

	// Six rounds, 7 hours apart (more than the longest retry delay): one message
	// is sent, the other fails every time and is given up.
	for i := range MaxAttempts {
		clock.Set(workerT0.Add(time.Duration(i) * 7 * time.Hour))
		sent, failed, err := w.RunOnce(t.Context())
		if err != nil {
			t.Fatalf("round %d: RunOnce error = %v", i+1, err)
		}
		wantSent := 0
		if i == 0 {
			wantSent = 1
		}
		if sent != wantSent || failed != 1 {
			t.Fatalf("round %d: sent %d, failed %d; want sent %d", i+1, sent, failed, wantSent)
		}
	}

	out := logs.String()
	for _, want := range []string{"id=" + ok.ID, "id=" + bad.ID, "gave_up=true", "attempt=6", "retry_at=", failure.Error()} {
		if !strings.Contains(out, want) {
			t.Errorf("the log does not contain %q:\n%s", want, out)
		}
	}
	for _, m := range []domain.OutboxMessage{ok, bad} {
		for _, secret := range []string{m.To, m.Subject, m.Text, m.HTML, "token-", "agenda.example"} {
			if strings.Contains(out, secret) {
				t.Errorf("the log contains %q, which belongs to the message:\n%s", secret, out)
			}
		}
	}
}

func TestWorkerRunOnceReturnsAClaimError(t *testing.T) {
	boom := errors.New("claim: store down")
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{}
	w := newWorker(flakyOutbox{Outbox: ob, claimErr: boom}, sender, clock, discardLog)
	enqueue(t, ob, "ana@example.com", workerT0)

	sent, failed, err := w.RunOnce(t.Context())
	if !errors.Is(err, boom) || sent != 0 || failed != 0 {
		t.Fatalf("RunOnce = %d, %d, %v; want 0, 0 and the claim error", sent, failed, err)
	}
	if n := len(sender.sent()); n != 0 {
		t.Errorf("Send called %d times after a failed claim, want 0", n)
	}
}

func TestWorkerSendsAgainWhenMarkSentFails(t *testing.T) {
	boom := errors.New("mark sent: store down")
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{}
	w := newWorker(flakyOutbox{Outbox: ob, sentErr: boom}, sender, clock, discardLog)
	enqueue(t, ob, "ana@example.com", workerT0)

	// The mail goes out but the outbox does not learn of it: not a sent message.
	sent, failed, err := w.RunOnce(t.Context())
	if !errors.Is(err, boom) || sent != 0 || failed != 1 {
		t.Fatalf("RunOnce = %d, %d, %v; want 0, 1 and the mark error", sent, failed, err)
	}
	// The lease holds, so the message is not sent again in the same run or soon after.
	wantRun(t, w, t.Context(), 0, 0)
	if n := len(sender.sent()); n != 1 {
		t.Fatalf("Send called %d times while the lease held, want 1", n)
	}

	// Once the lease has run out, at-least-once delivery sends it again.
	w.Outbox = ob
	clock.Set(workerT0.Add(5 * time.Minute))
	wantRun(t, w, t.Context(), 1, 0)
	if n := len(sender.sent()); n != 2 {
		t.Errorf("Send called %d times after the lease ran out, want 2", n)
	}
}

func TestWorkerGoesOnWhenMarkFailedFails(t *testing.T) {
	boom := errors.New("mark failed: store down")
	ob := newOutbox(t)
	clock := &fakeClock{t: workerT0}
	sender := &fakeSender{send: func(context.Context, domain.OutboxMessage) error { return errors.New("smtp: 451") }}
	w := newWorker(flakyOutbox{Outbox: ob, failedErr: boom}, sender, clock, discardLog)
	enqueue(t, ob, "ana@example.com", workerT0)
	enqueue(t, ob, "bia@example.com", workerT0)

	sent, failed, err := w.RunOnce(t.Context())
	if !errors.Is(err, boom) || sent != 0 || failed != 2 {
		t.Fatalf("RunOnce = %d, %d, %v; want 0, 2 and the mark error", sent, failed, err)
	}
	if n := len(sender.sent()); n != 2 {
		t.Errorf("Send called %d times, want 2: a failed mark must not end the run", n)
	}
}

func TestWorkerRunDrainsAtOnceAndStopsOnCancel(t *testing.T) {
	ob := newOutbox(t)
	sender := &fakeSender{}
	// An hour between runs: only the run at the start can send the message.
	w := &Worker{Outbox: ob, Sender: sender, Log: discardLog, Interval: time.Hour}
	enqueue(t, ob, "ana@example.com", time.Now().Add(-time.Minute))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	waitFor(t, "the first run to send the message", func() bool { return len(sender.sent()) == 1 })

	cancel()
	waitReturn(t, done, time.Second, "Run after the cancel")
}

func TestWorkerRunPollsEveryInterval(t *testing.T) {
	ob := newOutbox(t)
	sender := &fakeSender{}
	w := &Worker{Outbox: ob, Sender: sender, Log: discardLog, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	// Messages that arrive while Run waits are picked up by a later poll.
	for i := range 2 {
		enqueue(t, ob, fmt.Sprintf("ana%d@example.com", i), time.Now().Add(-time.Second))
		waitFor(t, fmt.Sprintf("message %d to be sent", i+1), func() bool { return len(sender.sent()) == i+1 })
	}

	cancel()
	waitReturn(t, done, time.Second, "Run after the cancel")

	// Run is over: it sends nothing more.
	enqueue(t, ob, "bia@example.com", time.Now().Add(-time.Second))
	time.Sleep(100 * time.Millisecond)
	if n := len(sender.sent()); n != 2 {
		t.Errorf("Send called %d times after Run returned, want 2", n)
	}
}

func TestWorkerRunKeepsGoingAfterAnError(t *testing.T) {
	ob := newOutbox(t)
	sender := &fakeSender{}
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	flaky := &claimFailsOnce{Outbox: ob, err: errors.New("claim: store down")}
	w := &Worker{Outbox: flaky, Sender: sender, Log: log, Interval: 10 * time.Millisecond}
	enqueue(t, ob, "ana@example.com", time.Now().Add(-time.Second))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	waitFor(t, "the message to be sent after the failed claim", func() bool { return len(sender.sent()) == 1 })
	cancel()
	waitReturn(t, done, time.Second, "Run after the cancel")

	if !strings.Contains(logs.String(), "store down") {
		t.Errorf("the claim error is not in the log:\n%s", logs.String())
	}
}

// claimFailsOnce fails its first Claim.
type claimFailsOnce struct {
	domain.Outbox
	err  error
	once sync.Once
}

func (c *claimFailsOnce) Claim(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]domain.OutboxMessage, error) {
	var err error
	c.once.Do(func() { err = c.err })
	if err != nil {
		return nil, err
	}
	return c.Outbox.Claim(ctx, now, lease, limit)
}
