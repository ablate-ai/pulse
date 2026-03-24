package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type Manager struct {
	username string
	password string

	mu       sync.RWMutex
	sessions map[string]string
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func NewManager(username, password string) *Manager {
	return &Manager{
		username: username,
		password: password,
		sessions: make(map[string]string),
	}
}

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		if token == "" || !m.valid(token) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	if req.Username != m.username || req.Password != m.password {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}

	token := randomToken()

	m.mu.Lock()
	m.sessions[token] = req.Username
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"username": req.Username,
	})
}

func (m *Manager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	token := bearerToken(r.Header.Get("Authorization"))
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (m *Manager) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	token := bearerToken(r.Header.Get("Authorization"))

	m.mu.RLock()
	username := m.sessions[token]
	m.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{"username": username})
}

func (m *Manager) valid(token string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[token]
	return ok
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func randomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
