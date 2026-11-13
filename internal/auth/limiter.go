package auth

import (
	"sync"
	"time"
)

const LimiterMaxKeys = 100_000

type limiterEntry struct {
	start time.Time
	hits  int
}

type Limiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	maxKeys   int
	keys      map[string]limiterEntry
	nextSweep time.Time
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{}
}

func (l *Limiter) Allow(key string, now time.Time) bool {
	return false
}
