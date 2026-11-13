package auth

import (
	"sync"
	"time"
)

// LimiterMaxKeys is the most keys a Limiter holds at once.
const LimiterMaxKeys = 100_000

// limiterEntry is the fixed window of one key: when it began and its hits so far.
type limiterEntry struct {
	start time.Time
	hits  int
}

// Limiter allows at most limit hits per key in each fixed window. The window of
// a key starts at its first hit and a new one starts with the first hit after it
// ends. It lives in memory (a restart forgets it), is safe for concurrent use,
// and never logs a key. Callers build keys like "login:"+ip.
type Limiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	maxKeys   int
	keys      map[string]limiterEntry
	nextSweep time.Time // the earliest time the next sweep may run
}

// NewLimiter returns a Limiter for limit hits per window. A limit below 1 or a
// window of zero or less is a programming error and panics.
func NewLimiter(limit int, window time.Duration) *Limiter {
	if limit < 1 || window <= 0 {
		panic("auth: NewLimiter needs a limit of at least 1 and a positive window")
	}
	return &Limiter{
		limit:   limit,
		window:  window,
		maxKeys: LimiterMaxKeys,
		keys:    make(map[string]limiterEntry),
	}
}

// Allow reports whether a hit on key at now is within the limit, and counts it
// when it is.
//
// Memory is bounded: at most once per window the keys whose window has ended are
// dropped, and while the map still holds maxKeys keys a new key is denied (fail
// closed), so a flood of distinct keys cannot grow it or lift the limit of one
// that is known. A key created before a sweep survives it when its window has
// not ended yet, and can end before the next sweep (the same holds when the
// clock steps back), so the map can hold keys whose window has ended: the window
// check below gives such a key a new window, and the cap can count it for up to
// one window.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !now.Before(l.nextSweep) {
		for k, e := range l.keys {
			if !now.Before(e.start.Add(l.window)) {
				delete(l.keys, k)
			}
		}
		l.nextSweep = now.Add(l.window)
	}

	e, known := l.keys[key]
	switch {
	case known && now.Before(e.start.Add(l.window)):
		if e.hits >= l.limit {
			return false
		}
		e.hits++
		l.keys[key] = e
	case !known && len(l.keys) >= l.maxKeys:
		return false
	default: // a new key, or a known one whose window ended: a new window
		l.keys[key] = limiterEntry{start: now, hits: 1}
	}
	return true
}
