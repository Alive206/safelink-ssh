package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/example/safelink/pkg/config"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "safelink_session"

type authService struct {
	cfg      config.AuthCfg
	mu       sync.Mutex
	sessions map[string]session
}

type session struct {
	Username  string
	ExpiresAt time.Time
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func newAuthService(cfg config.AuthCfg) *authService {
	return &authService{
		cfg:      cfg,
		sessions: make(map[string]session),
	}
}

func (a *authService) enabled() bool {
	return a != nil && (a.cfg.APIToken != "" || len(a.cfg.Users) > 0)
}

func (a *authService) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled() || a.authorized(r) {
			next(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func (a *authService) authorized(r *http.Request) bool {
	if a.cfg.APIToken != "" && r.Header.Get("Authorization") == "Bearer "+a.cfg.APIToken {
		return true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[cookie.Value]
	if !ok || time.Now().After(s.ExpiresAt) {
		delete(a.sessions, cookie.Value)
		return false
	}
	return true
}

func (a *authService) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if !a.checkPassword(req.Username, req.Password) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token, err := randomToken()
	if err != nil {
		http.Error(w, "create session", http.StatusInternalServerError)
		return
	}
	expires := time.Now().Add(24 * time.Hour)
	a.mu.Lock()
	a.sessions[token] = session{Username: req.Username, ExpiresAt: expires}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username})
}

func (a *authService) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *authService) handleMe(w http.ResponseWriter, r *http.Request) {
	if !a.enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": "anonymous"})
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		a.mu.Lock()
		s, ok := a.sessions[cookie.Value]
		a.mu.Unlock()
		if ok && time.Now().Before(s.ExpiresAt) {
			writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": s.Username})
			return
		}
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (a *authService) checkPassword(username, password string) bool {
	for _, user := range a.cfg.Users {
		if user.Username != username {
			continue
		}
		return bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) == nil
	}
	return false
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
