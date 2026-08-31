package transport

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Hub is the process-wide registry of live sessions. It maps player ids to the
// single active session for that player and remembers reconnect tokens so a
// dropped player can resume its seat.
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*Session // playerID -> active session
	tokens   map[string]string   // playerID -> reconnect token
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{
		sessions: make(map[string]*Session),
		tokens:   make(map[string]string),
	}
}

// NewToken returns a fresh 128-bit hex reconnect token.
func NewToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A deterministic fallback token would be shared by every player in
		// this failure mode, which collapses session security entirely. The
		// system CSPRNG being unavailable is unrecoverable: fail hard rather
		// than silently degrade.
		panic("transport: crypto/rand unavailable, refusing to issue session tokens: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// Register installs a session, replacing (and kicking) any previous session for
// the same player. It returns the previous session if one was displaced.
func (h *Hub) Register(s *Session) *Session {
	h.mu.Lock()
	prev := h.sessions[s.playerID]
	h.sessions[s.playerID] = s
	h.tokens[s.playerID] = s.token
	h.mu.Unlock()

	if prev != nil && prev != s {
		prev.Close("replaced_by_new_session")
	}
	return prev
}

// Unregister removes a session if it is still the active one for that player.
func (h *Hub) Unregister(s *Session) {
	h.mu.Lock()
	if cur, ok := h.sessions[s.playerID]; ok && cur == s {
		delete(h.sessions, s.playerID)
	}
	h.mu.Unlock()
}

// Session returns the active session for a player, if any.
func (h *Hub) Session(playerID string) (*Session, bool) {
	h.mu.RLock()
	s, ok := h.sessions[playerID]
	h.mu.RUnlock()
	return s, ok
}

// ValidToken reports whether token matches the last token issued to playerID.
func (h *Hub) ValidToken(playerID, token string) bool {
	if token == "" {
		return false
	}
	h.mu.RLock()
	want, ok := h.tokens[playerID]
	h.mu.RUnlock()
	return ok && want == token
}

// RememberToken stores a reconnect token for a player.
func (h *Hub) RememberToken(playerID, token string) {
	h.mu.Lock()
	h.tokens[playerID] = token
	h.mu.Unlock()
}

// ForgetToken drops a player's reconnect token (e.g. after a clean leave).
func (h *Hub) ForgetToken(playerID string) {
	h.mu.Lock()
	delete(h.tokens, playerID)
	h.mu.Unlock()
}

// Send delivers v to a player if they are currently connected.
func (h *Hub) Send(playerID string, v any) bool {
	s, ok := h.Session(playerID)
	if !ok || s.Closed() {
		return false
	}
	s.Send(v)
	return true
}

// Count returns the number of live sessions.
func (h *Hub) Count() int {
	h.mu.RLock()
	n := len(h.sessions)
	h.mu.RUnlock()
	return n
}

// CloseAll shuts every session down (used on graceful server shutdown).
func (h *Hub) CloseAll(reason string) {
	h.mu.Lock()
	all := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		all = append(all, s)
	}
	h.sessions = make(map[string]*Session)
	h.mu.Unlock()

	for _, s := range all {
		s.Close(reason)
	}
}
