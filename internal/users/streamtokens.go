package users

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// StreamTokenTTL is how long a minted stream token is valid. Short enough
// that leaks via URL/Referer/server-log have limited blast radius, long
// enough that a single token covers a normal listening session.
const StreamTokenTTL = 30 * time.Minute

type streamTokenEntry struct {
	userID    string
	expiresAt time.Time
}

// streamTokenStore is an in-memory ephemeral credential cache. Stream tokens
// authenticate the same user as the bearer that minted them but are designed
// to be safe-ish in URLs (HTML5 <audio src> can't send custom headers).
// Tokens are dropped on server restart — clients re-mint on next request.
type streamTokenStore struct {
	mu     sync.Mutex
	tokens map[string]streamTokenEntry
}

func newStreamTokenStore() *streamTokenStore {
	return &streamTokenStore{tokens: make(map[string]streamTokenEntry)}
}

func (s *streamTokenStore) issue(userID string) (string, time.Time, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	token := "smt_" + hex.EncodeToString(buf)
	expiresAt := time.Now().Add(StreamTokenTTL).UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(time.Now())
	s.tokens[token] = streamTokenEntry{userID: userID, expiresAt: expiresAt}
	return token, expiresAt, nil
}

func (s *streamTokenStore) validate(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[token]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(s.tokens, token)
		return "", false
	}
	return entry.userID, true
}

// gcLocked drops expired entries; cheap enough to call on every issue.
// The caller must hold s.mu.
func (s *streamTokenStore) gcLocked(now time.Time) {
	for k, v := range s.tokens {
		if now.After(v.expiresAt) {
			delete(s.tokens, k)
		}
	}
}
