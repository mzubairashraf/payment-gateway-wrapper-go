package httpapi

import (
	"crypto/sha256"
	"sync"
	"time"
)

// idempotencyStore remembers the response to each Idempotency-Key so that a
// client retry returns the original result instead of charging again.
//
// It is in-memory, which is correct for a single instance. Behind a load
// balancer the same interface would be backed by a shared store (Redis
// SET NX, or a unique-keyed table written in the same transaction as the
// payment record).
type idempotencyStore struct {
	mu      sync.Mutex
	entries map[string]*idempotencyEntry
	ttl     time.Duration
	now     func() time.Time
}

type idempotencyEntry struct {
	fingerprint [sha256.Size]byte
	done        bool
	status      int
	body        []byte
	expires     time.Time
}

type idempotencyState int

const (
	stateNew        idempotencyState = iota // caller owns the key and must call finish or release
	stateReplay                             // a finished response is returned
	stateInProgress                         // the first request is still running
	stateMismatch                           // the key was used with a different request
)

func newIdempotencyStore(ttl time.Duration, now func() time.Time) *idempotencyStore {
	return &idempotencyStore{entries: map[string]*idempotencyEntry{}, ttl: ttl, now: now}
}

// begin claims key for a request with the given fingerprint.
func (s *idempotencyStore) begin(key string, fingerprint [sha256.Size]byte) (idempotencyState, *idempotencyEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for k, e := range s.entries { // lazy expiry; the map stays small at demo scale
		if e.done && now.After(e.expires) {
			delete(s.entries, k)
		}
	}

	e, ok := s.entries[key]
	switch {
	case !ok:
		s.entries[key] = &idempotencyEntry{fingerprint: fingerprint}
		return stateNew, nil
	case e.fingerprint != fingerprint:
		return stateMismatch, nil
	case !e.done:
		return stateInProgress, nil
	default:
		return stateReplay, e
	}
}

// finish stores the response for future replays.
func (s *idempotencyStore) finish(key string, status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[key]; ok {
		e.done, e.status, e.body, e.expires = true, status, body, s.now().Add(s.ttl)
	}
}

// release forgets the key, so the same key may be used again. It is called
// only when the request provably had no effect.
func (s *idempotencyStore) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}
