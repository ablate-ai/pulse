package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
)

// SessionStore 定义 session 持久化接口。
type SessionStore interface {
	Create(token, username string) error
	GetUsername(token string) (string, bool)
	Delete(token string) error
}

type Manager struct {
	mu       sync.RWMutex
	username string
	password string
	sessions SessionStore
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func NewManager(username, password string, sessions SessionStore) *Manager {
	return &Manager{
		username: username,
		password: password,
		sessions: sessions,
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
	if err := m.sessions.Create(token, req.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create session: " + err.Error()})
		return
	}

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
	_ = m.sessions.Delete(token)

	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (m *Manager) HandleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	token := bearerToken(r.Header.Get("Authorization"))
	username, _ := m.sessions.GetUsername(token)

	writeJSON(w, http.StatusOK, map[string]any{"username": username})
}

func (m *Manager) valid(token string) bool {
	_, ok := m.sessions.GetUsername(token)
	return ok
}

// ChangePassword 校验旧密码后更新密码（内存生效，重启后恢复为环境变量值）。
func (m *Manager) ChangePassword(current, newPw string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.password != current {
		return errors.New("当前密码不正确")
	}
	if len(newPw) < 6 {
		return errors.New("新密码至少 6 位")
	}
	m.password = newPw
	return nil
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

// Login 验证凭据并创建新的 session token。
func (m *Manager) Login(username, password string) (string, error) {
	m.mu.RLock()
	match := username == m.username && password == m.password
	m.mu.RUnlock()
	if !match {
		return "", errors.New("invalid credentials")
	}
	token := randomToken()
	if err := m.sessions.Create(token, username); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateToken 检查 token 是否有效。
func (m *Manager) ValidateToken(token string) bool {
	return m.valid(token)
}

// DeleteToken 使 token 失效。
func (m *Manager) DeleteToken(token string) error {
	return m.sessions.Delete(token)
}

// GetUsernameByToken 根据 token 返回用户名，token 无效时返回空字符串。
func (m *Manager) GetUsernameByToken(token string) string {
	username, _ := m.sessions.GetUsername(token)
	return username
}
