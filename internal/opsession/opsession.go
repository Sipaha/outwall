// Package opsession holds the operator session: a short-lived, master-password-gated window during
// which privileged operator mutations are permitted. It is deliberately SEPARATE from the vault's
// locked/unlocked state — the data plane keeps serving while the operator session is closed, and an
// idle-expiry / "Lock now" of the operator session does NOT lock the vault. The daemon holds exactly
// one *Session; a closed or idle-expired session makes every gated route return 403 until the
// operator re-enters the master password.
package opsession

import (
	"sync"
	"time"
)

// DefaultTTL is the default idle timeout of an operator session (sliding window).
const DefaultTTL = time.Hour

// Session is a single operator session guarded by an idle TTL. All methods are safe for concurrent
// use. The clock is injectable (now) so tests drive expiry deterministically without sleeping.
type Session struct {
	mu       sync.Mutex
	open     bool
	openedAt time.Time
	lastUsed time.Time
	ttl      time.Duration
	now      func() time.Time
}

// New returns a closed session with the given idle TTL. A non-positive ttl falls back to DefaultTTL.
func New(ttl time.Duration) *Session {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Session{ttl: ttl, now: time.Now}
}

// Open marks the session open, starting the idle window now.
func (s *Session) Open() {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.now()
	s.open = true
	s.openedAt = t
	s.lastUsed = t
}

// Lock closes the session immediately.
func (s *Session) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}

// Authorized reports whether the session is open AND within the idle TTL. On success it slides the
// idle window (refreshes lastUsed), so activity keeps the session alive; on expiry it closes the
// session and returns false.
func (s *Session) Authorized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false
	}
	t := s.now()
	if t.Sub(s.lastUsed) >= s.ttl {
		s.reset()
		return false
	}
	s.lastUsed = t
	return true
}

// Status returns whether the session is open and the idle time remaining (0 when closed/expired). It
// is a read-only peek: it does NOT slide the window, so polling status can never keep a session alive.
func (s *Session) Status() (open bool, idleRemaining time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false, 0
	}
	rem := s.ttl - s.now().Sub(s.lastUsed)
	if rem <= 0 {
		return false, 0
	}
	return true, rem
}

// reset clears session state. Caller must hold s.mu.
func (s *Session) reset() {
	s.open = false
	s.openedAt = time.Time{}
	s.lastUsed = time.Time{}
}
