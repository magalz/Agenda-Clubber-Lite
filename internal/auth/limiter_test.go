package auth

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var limiterStart = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

func TestNewLimiterPanicsOnBadArguments(t *testing.T) {
	for _, c := range []struct {
		name   string
		limit  int
		window time.Duration
	}{
		{"zero limit", 0, time.Minute},
		{"negative limit", -1, time.Minute},
		{"zero window", 1, 0},
		{"negative window", 1, -time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("NewLimiter(%d, %v) did not panic", c.limit, c.window)
				}
			}()
			NewLimiter(c.limit, c.window)
		})
	}
}

func TestNewLimiterCapsTheKeys(t *testing.T) {
	if got := NewLimiter(1, time.Minute).maxKeys; got != LimiterMaxKeys {
		t.Errorf("maxKeys = %d, want LimiterMaxKeys (%d)", got, LimiterMaxKeys)
	}
	if LimiterMaxKeys != 100_000 {
		t.Errorf("LimiterMaxKeys = %d, want 100000", LimiterMaxKeys)
	}
}

func TestLimiterStopsAtTheLimit(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	for i := 1; i <= 3; i++ {
		if !l.Allow("login:1.2.3.4", limiterStart) {
			t.Fatalf("hit %d was denied, the limit is 3", i)
		}
	}
	for i := 4; i <= 6; i++ {
		if l.Allow("login:1.2.3.4", limiterStart) {
			t.Errorf("hit %d was allowed, the limit is 3", i)
		}
	}
}

func TestLimiterWindowStartsAtTheFirstHitAndDoesNotSlide(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	if !l.Allow("k", limiterStart) {
		t.Fatal("first hit denied")
	}
	if !l.Allow("k", limiterStart.Add(59*time.Second)) {
		t.Fatal("second hit inside the window denied")
	}
	if l.Allow("k", limiterStart.Add(59*time.Second+500*time.Millisecond)) {
		t.Error("third hit inside the window allowed")
	}
	if l.Allow("k", limiterStart.Add(time.Minute-time.Nanosecond)) {
		t.Error("hit one nanosecond before the window ends allowed")
	}
}

func TestLimiterStartsANewWindowWhenTheOldOneEnds(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	l.Allow("k", limiterStart)
	l.Allow("k", limiterStart)
	if l.Allow("k", limiterStart.Add(time.Minute-time.Nanosecond)) {
		t.Fatal("hit before the window ends allowed")
	}

	second := limiterStart.Add(time.Minute) // exactly start+window: a new window
	if !l.Allow("k", second) || !l.Allow("k", second.Add(time.Second)) {
		t.Fatal("the first two hits of the new window were denied")
	}
	if l.Allow("k", second.Add(time.Second)) {
		t.Error("third hit of the new window allowed")
	}
	// The new window began at its first hit, not at the old window's end.
	if l.Allow("k", second.Add(time.Minute-time.Nanosecond)) {
		t.Error("hit before the new window ends allowed")
	}
	if !l.Allow("k", second.Add(time.Minute)) {
		t.Error("hit when the new window ends denied")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	if !l.Allow("login:a", limiterStart) {
		t.Fatal("first key denied")
	}
	if l.Allow("login:a", limiterStart) {
		t.Fatal("first key allowed twice")
	}
	if !l.Allow("login:b", limiterStart) {
		t.Error("second key denied because of the first")
	}
	if !l.Allow("signup:a", limiterStart) {
		t.Error("same suffix under another prefix denied")
	}
}

// The rule of a key must not depend on when the sweep last ran. A clock that
// steps back leaves a key whose window has ended but that no sweep has dropped.
func TestLimiterWindowEndsEvenWhenNoSweepRuns(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	l.Allow("late", limiterStart.Add(5*time.Minute)) // the sweep ran: next one at +6m
	early := limiterStart.Add(2 * time.Minute)       // the clock stepped back
	if !l.Allow("k", early) {
		t.Fatal("first hit denied")
	}
	if l.Allow("k", early.Add(time.Minute-time.Nanosecond)) {
		t.Error("hit before the window ends allowed")
	}
	if !l.Allow("k", early.Add(time.Minute)) {
		t.Error("hit when the window ends denied")
	}
}

func TestLimiterConcurrentHitsOnOneKey(t *testing.T) {
	const goroutines, limit = 50, 10
	l := NewLimiter(limit, time.Minute)
	var allowed atomic.Int32
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(goroutines)
	done.Add(goroutines)
	for range goroutines {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			if l.Allow("login:shared", limiterStart) {
				allowed.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	if got := allowed.Load(); got != limit {
		t.Errorf("%d of %d concurrent hits were allowed, want exactly %d", got, goroutines, limit)
	}
}

// TestLimiterConcurrentManyKeys has no assertion of its own: it makes the race
// detector see the sweep and the cap check run next to ordinary hits.
func TestLimiterConcurrentManyKeys(t *testing.T) {
	l := NewLimiter(2, time.Second)
	l.maxKeys = 50
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 300 {
				now := limiterStart.Add(time.Duration(i) * 100 * time.Millisecond)
				l.Allow(fmt.Sprintf("g%d:k%d", g, i%40), now)
			}
		}()
	}
	wg.Wait()
}

func TestLimiterSweepsEndedWindows(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	for i := range 10 {
		if !l.Allow(fmt.Sprintf("k%d", i), limiterStart) {
			t.Fatalf("key k%d denied on its first hit", i)
		}
	}
	if got := len(l.keys); got != 10 {
		t.Fatalf("%d keys held after ten hits, want 10", got)
	}
	// Still inside the windows: nothing may be dropped.
	l.Allow("k0", limiterStart.Add(30*time.Second))
	if got := len(l.keys); got != 10 {
		t.Errorf("%d keys held inside the window, want 10", got)
	}
	// The windows have ended: the next hit sweeps them away and keeps its own key.
	if !l.Allow("late", limiterStart.Add(time.Minute)) {
		t.Fatal("hit after the windows ended denied")
	}
	if got := len(l.keys); got != 1 {
		t.Errorf("%d keys held after the sweep, want 1 (the late one)", got)
	}
	if _, ok := l.keys["late"]; !ok {
		t.Error("the sweep dropped the key of the hit that triggered it")
	}
}

func TestLimiterFailsClosedForNewKeysAtTheCap(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	l.maxKeys = 2

	if !l.Allow("a", limiterStart) || !l.Allow("b", limiterStart) {
		t.Fatal("the first two keys were denied")
	}
	if l.Allow("c", limiterStart) {
		t.Error("a new key was allowed with the map full")
	}
	if _, ok := l.keys["c"]; ok {
		t.Error("a denied new key was stored")
	}
	if got := len(l.keys); got != 2 {
		t.Errorf("%d keys held, want the cap (2)", got)
	}
	// Known keys keep following their own limit.
	for _, k := range []string{"a", "b"} {
		if !l.Allow(k, limiterStart) {
			t.Errorf("known key %s denied its second hit", k)
		}
		if l.Allow(k, limiterStart) {
			t.Errorf("known key %s allowed past its limit", k)
		}
	}
	// Once the windows end, the sweep frees the room.
	if !l.Allow("c", limiterStart.Add(time.Minute)) {
		t.Error("a new key was denied after the old windows ended")
	}
}
