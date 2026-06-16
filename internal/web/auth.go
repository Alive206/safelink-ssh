package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/example/sshtunneld/internal/config"
)

// sessionTTL is how long a /api/login cookie remains valid.
const sessionTTL = 12 * time.Hour

// authenticator owns user credentials, the static bearer token and the
// in-memory session table.
type authenticator struct {
	users    map[string][]byte // username → bcrypt hash
	apiToken string

	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
}

func newAuthenticator(cfg config.AuthCfg) *authenticator {
	a := &authenticator{
		users:    make(map[string][]byte, len(cfg.Users)),
		apiToken: cfg.APIToken,
		sessions: make(map[string]time.Time),
	}
	for _, u := range cfg.Users {
		if u.Username == "" || u.PasswordHash == "" {
			continue
		}
		a.users[u.Username] = []byte(u.PasswordHash)
	}
	return a
}

// authEnabled reports whether any auth method is configured.  When false the
// server runs fully open — useful for local dev only.
func (a *authenticator) authEnabled() bool {
	return len(a.users) > 0 || a.apiToken != ""
}

// hasUsers reports whether at least one user account is configured.
func (a *authenticator) hasUsers() bool { return len(a.users) > 0 }

// login validates the credentials and, on success, returns a fresh session
// token.  An invalid pair returns an empty string.
func (a *authenticator) login(username, password string) string {
	hash, ok := a.users[username]
	if !ok {
		// Run bcrypt against a known-good hash anyway to keep timing roughly
		// uniform between unknown-user and wrong-password failures.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$K1U8r8jw1m6Y4/0a8mTtxe7oCQH3w5lLPMqlnP6n3X4n.ge1y6n6m"), []byte(password))
		return ""
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return ""
	}
	tok := newToken()
	a.mu.Lock()
	a.sessions[tok] = time.Now().Add(sessionTTL)
	a.mu.Unlock()
	return tok
}

// logout revokes the session associated with the cookie token.
func (a *authenticator) logout(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// validate runs a single auth check on r and returns true when it passes.
// When auth is fully disabled it always succeeds.
func (a *authenticator) validate(r *http.Request) bool {
	if !a.authEnabled() {
		return true
	}
	if a.checkBearer(r) {
		return true
	}
	if a.checkSession(r) {
		return true
	}
	return false
}

func (a *authenticator) checkBearer(r *http.Request) bool {
	if a.apiToken == "" {
		return false
	}
	got := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(got, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(a.apiToken)) == 1
}

func (a *authenticator) checkSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, c.Value)
		return false
	}
	return true
}

// pruneSessions periodically removes expired tokens from memory.  Should be
// called from a long-lived goroutine.
func (a *authenticator) pruneSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for tok, exp := range a.sessions {
		if now.After(exp) {
			delete(a.sessions, tok)
		}
	}
}

const sessionCookieName = "sshtunneld_session"

func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
