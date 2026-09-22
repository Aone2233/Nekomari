package oauthutil

import (
	"sync"
	"time"
)

const stateTTL = 5 * time.Minute
const maxStates = 4096

// States bounds anonymous login attempts and consumes each nonce atomically.
// The zero value is ready for use; no cleanup goroutine is needed.
type States struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

func (s *States) Add(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, expires := range s.entries {
		if !now.Before(expires) {
			delete(s.entries, key)
		}
	}
	if state == "" || len(s.entries) >= maxStates {
		return false
	}
	if s.entries == nil {
		s.entries = make(map[string]time.Time)
	}
	s.entries[state] = now.Add(stateTTL)
	return true
}

func (s *States) Consume(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.entries[state]
	delete(s.entries, state)
	return ok && state != "" && time.Now().Before(expires)
}

func (s *States) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}
