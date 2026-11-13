package mail

import (
	"context"
	"log/slog"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// RetryDelays[i] is the wait after the (i+1)-th failed attempt.
var RetryDelays []time.Duration

// MaxAttempts is how many times a message is sent before it is given up.
const MaxAttempts = 0

// Worker drains the outbox.
type Worker struct {
	Outbox      domain.Outbox
	Sender      Sender
	Log         *slog.Logger
	Now         func() time.Time
	Interval    time.Duration
	Lease       time.Duration
	SendTimeout time.Duration
}

// RunOnce sends the messages that are due.
func (w *Worker) RunOnce(ctx context.Context) (sent, failed int, err error) { return 0, 0, nil }

// Run calls RunOnce every Interval until ctx is canceled.
func (w *Worker) Run(ctx context.Context) {}
