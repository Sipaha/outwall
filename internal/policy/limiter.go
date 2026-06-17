package policy

import (
	"sync"
	"time"
)

type window struct {
	minute int64
	count  int
}

// Limiter is an in-memory fixed-window-per-minute counter keyed by an arbitrary string.
type Limiter struct {
	mu sync.Mutex
	m  map[string]*window
}

// NewLimiter constructs an empty rate limiter.
func NewLimiter() *Limiter { return &Limiter{m: map[string]*window{}} }

// Allow records a hit for key and reports whether it is within limitPerMin for the current
// minute of now. limitPerMin <= 0 means unlimited.
func (l *Limiter) Allow(key string, limitPerMin int, now time.Time) bool {
	if limitPerMin <= 0 {
		return true
	}
	min := now.Unix() / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.m[key]
	if w == nil || w.minute != min {
		w = &window{minute: min}
		l.m[key] = w
	}
	if w.count >= limitPerMin {
		return false
	}
	w.count++
	return true
}
