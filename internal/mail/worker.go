package mail

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// RetryDelays[i] is the wait after the (i+1)-th failed attempt. It has
// MaxAttempts-1 entries: the last attempt is not followed by a retry.
var RetryDelays = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// MaxAttempts is how many times a message is sent before it is given up.
const MaxAttempts = 6

const (
	defaultInterval    = 5 * time.Second
	defaultLease       = 5 * time.Minute
	defaultSendTimeout = 3 * time.Minute

	// markTimeout bounds the write that records the result of a send.
	markTimeout = 10 * time.Second
)

// Worker drains the outbox: it claims a due message with a lease, sends it
// through Sender and records the result. A failed message is retried after the
// next of RetryDelays and is given up after MaxAttempts attempts.
//
// Delivery is at-least-once. When the send succeeds but recording it fails, the
// message is sent again once its lease has run out. A Lease longer than
// SendTimeout plus the time to record a result keeps a second worker from
// sending a message that is still in flight; the defaults do.
//
// The logs name a message by its ID only: its recipient, subject and bodies
// carry personal data and the links in them carry tokens. The error of a Sender
// is logged and stored as it is, so a Sender keeps all of that out of it.
type Worker struct {
	Outbox domain.Outbox
	Sender Sender
	Log    *slog.Logger // nil means slog.Default()

	Now         func() time.Time // nil means time.Now in UTC
	Interval    time.Duration    // wait between runs of Run; 0 means 5 seconds
	Lease       time.Duration    // how long a claimed message is held; 0 means 5 minutes
	SendTimeout time.Duration    // bound of one Send; 0 means 3 minutes
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now().UTC()
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// orDefault returns d, or def when d is not positive.
func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// RunOnce sends the messages that are due, one at a time, until none is left or
// ctx is done, and returns how many it sent and how many failed.
//
// A message whose result could not be recorded counts as failed: after a send
// that went out, the message stays in the outbox and is sent again when its
// lease runs out. err is the first error of the store. A Claim that fails ends
// the run; a result that cannot be recorded does not.
func (w *Worker) RunOnce(ctx context.Context) (sent, failed int, err error) {
	for ctx.Err() == nil {
		// One message per claim, so its lease starts when its send does.
		msgs, claimErr := w.Outbox.Claim(ctx, w.now(), orDefault(w.Lease, defaultLease), 1)
		if claimErr != nil {
			if err == nil {
				err = fmt.Errorf("mail: claim outbox messages: %w", claimErr)
			}
			return sent, failed, err
		}
		if len(msgs) == 0 {
			return sent, failed, err
		}
		ok, markErr := w.deliver(ctx, msgs[0])
		if ok {
			sent++
		} else {
			failed++
		}
		if markErr != nil && err == nil {
			err = markErr
		}
	}
	return sent, failed, err
}

// deliver sends m and records the result. It reports true when the message was sent
// and recorded as sent, and the error when it could not record the result.
func (w *Worker) deliver(ctx context.Context, m domain.OutboxMessage) (bool, error) {
	sendCtx, cancel := context.WithTimeout(ctx, orDefault(w.SendTimeout, defaultSendTimeout))
	sendErr := w.Sender.Send(sendCtx, m)
	cancel()

	// A shutdown that canceled ctx during the send must not lose the result.
	markCtx, cancelMark := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer cancelMark()
	now := w.now()

	if sendErr == nil {
		if err := w.Outbox.MarkSent(markCtx, m.ID, now); err != nil {
			w.log().Error("mail: sent a message but could not record it; it is sent again when its lease runs out",
				"id", m.ID, "error", err)
			return false, fmt.Errorf("mail: mark message sent: %w", err)
		}
		w.log().Info("mail: message sent", "id", m.ID)
		return true, nil
	}

	// m.Attempts counts the attempts before this one, which MarkFailed adds.
	attempt := m.Attempts + 1
	var retryAt *time.Time
	if attempt < MaxAttempts {
		at := now.Add(RetryDelays[m.Attempts])
		retryAt = &at
	}
	if err := w.Outbox.MarkFailed(markCtx, m.ID, sendErr.Error(), retryAt); err != nil {
		// No verdict is logged: the message was not retried or given up as far as
		// the outbox knows, it comes back when its lease runs out.
		w.log().Error("mail: could not record a failed send; it is sent again when its lease runs out",
			"id", m.ID, "error", err)
		return false, fmt.Errorf("mail: mark message failed: %w", err)
	}
	if retryAt != nil {
		w.log().Warn("mail: send failed, will retry", "id", m.ID, "attempt", attempt, "retry_at", *retryAt, "error", sendErr)
	} else {
		w.log().Warn("mail: send failed, giving up", "id", m.ID, "attempt", attempt, "gave_up", true, "error", sendErr)
	}
	return false, nil
}

// Run calls RunOnce at once and then every Interval, until ctx is canceled. It
// returns after the message in flight has been dealt with. An error of a run
// is logged and the next run goes on.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(orDefault(w.Interval, defaultInterval))
	defer ticker.Stop()
	for ctx.Err() == nil {
		if _, _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			w.log().Error("mail: outbox run failed", "error", err)
		}
		select {
		case <-ctx.Done(): // the loop condition ends Run
		case <-ticker.C:
		}
	}
}
