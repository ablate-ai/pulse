package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// memSessionStore 内存实现，仅用于测试。
type memSessionStore struct {
	mu   sync.RWMutex
	data map[string]string
}

func newMemStore() *memSessionStore {
	return &memSessionStore{data: make(map[string]string)}
}

func (s *memSessionStore) Create(token, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[token] = username
	return nil
}

func (s *memSessionStore) GetUsername(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.data[token]
	return u, ok
}

func (s *memSessionStore) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, token)
	return nil
}

func TestLoginMeLogout(t *testing.T) {
	manager := NewManager("admin", "secret", newMemStore())

	loginBody := []byte(`{"username":"admin","password":"secret"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(loginBody))
	rec := httptest.NewRecorder()
	manager.HandleLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatalf("expected token")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	manager.Middleware(http.HandlerFunc(manager.HandleMe)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	manager.Middleware(http.HandlerFunc(manager.HandleLogout)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec = httptest.NewRecorder()
	manager.Middleware(http.HandlerFunc(manager.HandleMe)).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized after logout, got %d", rec.Code)
	}
}
