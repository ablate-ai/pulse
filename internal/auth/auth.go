package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SessionStore 定义 session 持久化接口。
type SessionStore interface {
	Create(token, username string) error
	GetUsername(token string) (string, bool)
	Delete(token string) error
}

// loginAttempt 记录某 IP 的登录失败情况。
type loginAttempt struct {
	count     int
	windowEnd time.Time
}

const (
	maxLoginFailures  = 10              // 窗口内最大失败次数
	loginWindow       = 10 * time.Minute // 失败计数窗口
	loginLockDuration = 15 * time.Minute // 超限后锁定时长
)

type Manager struct {
	mu       sync.RWMutex
	username string
	password string
	sessions SessionStore

	failMu   sync.Mutex
	failures map[string]*loginAttempt // key: client IP
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
		failures: make(map[string]*loginAttempt),
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

	ip := clientIP(r)
	if m.isLocked(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many failed attempts, try later"})
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}
	if req.Username != m.username || req.Password != m.password {
		m.recordFailure(ip)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid credentials"})
		return
	}

	m.clearFailures(ip)
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

// Login 验证凭据并创建新的 session token。ip 用于暴力破解防护（可传空字符串跳过）。
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

// LoginFromRequest 供面板 handler 调用，携带 IP 用于暴力破解防护。
func (m *Manager) LoginFromRequest(r *http.Request, username, password string) (string, error) {
	ip := clientIP(r)
	if m.isLocked(ip) {
		return "", errors.New("too many failed attempts, try later")
	}
	m.mu.RLock()
	match := username == m.username && password == m.password
	m.mu.RUnlock()
	if !match {
		m.recordFailure(ip)
		return "", errors.New("invalid credentials")
	}
	m.clearFailures(ip)
	token := randomToken()
	if err := m.sessions.Create(token, username); err != nil {
		return "", err
	}
	return token, nil
}

// CreateSession 直接为指定用户名创建 session，供 Discourse SSO 等第三方认证使用。
func (m *Manager) CreateSession(username string) (string, error) {
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

// ─── 暴力破解防护 ──────────────────────────────────────────────────────────────

func (m *Manager) isLocked(ip string) bool {
	m.failMu.Lock()
	defer m.failMu.Unlock()
	a, ok := m.failures[ip]
	if !ok {
		return false
	}
	if time.Now().After(a.windowEnd) {
		delete(m.failures, ip)
		return false
	}
	return a.count >= maxLoginFailures
}

func (m *Manager) recordFailure(ip string) {
	m.failMu.Lock()
	defer m.failMu.Unlock()
	now := time.Now()
	a, ok := m.failures[ip]
	if !ok || now.After(a.windowEnd) {
		m.failures[ip] = &loginAttempt{count: 1, windowEnd: now.Add(loginWindow)}
		return
	}
	a.count++
	if a.count >= maxLoginFailures {
		// 超限后将窗口延长为锁定时长
		a.windowEnd = now.Add(loginLockDuration)
	}
}

func (m *Manager) clearFailures(ip string) {
	m.failMu.Lock()
	defer m.failMu.Unlock()
	delete(m.failures, ip)
}

// clientIP 从请求中提取客户端真实 IP（优先 X-Real-IP，否则 RemoteAddr）。
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
