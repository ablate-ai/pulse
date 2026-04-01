package users

import (
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.RWMutex
	users    map[string]User
	inbounds map[string]UserInbound
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:    make(map[string]User),
		inbounds: make(map[string]UserInbound),
	}
}

// ─── User CRUD ────────────────────────────────────────────────────────────────

func (s *MemoryStore) UpsertUser(user User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	if user.Status == "" {
		user.Status = StatusActive
	}
	if user.DataLimitResetStrategy == "" {
		user.DataLimitResetStrategy = ResetStrategyNoReset
	}
	user.UsedBytes = user.UploadBytes + user.DownloadBytes
	s.users[user.ID] = user
	return user, nil
}

func (s *MemoryStore) GetUser(id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *MemoryStore) GetUserBySubToken(token string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.SubToken == token {
			return u, nil
		}
	}
	return User{}, ErrUserNotFound
}

func (s *MemoryStore) ListUsers() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortUsers(usersMapToSlice(s.users)), nil
}

func (s *MemoryStore) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return ErrUserNotFound
	}
	delete(s.users, id)
	return nil
}

func (s *MemoryStore) GetUsersByIDs(ids []string) (map[string]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]User, len(ids))
	for _, id := range ids {
		if u, ok := s.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

// ─── UserInbound CRUD ─────────────────────────────────────────────────────────

func (s *MemoryStore) UpsertUserInbound(acc UserInbound) (UserInbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}
	s.inbounds[acc.ID] = acc
	return acc, nil
}

func (s *MemoryStore) GetUserInbound(id string) (UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	acc, ok := s.inbounds[id]
	if !ok {
		return UserInbound{}, ErrUserInboundNotFound
	}
	return acc, nil
}

func (s *MemoryStore) ListUserInboundsByUser(userID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if acc.UserID == userID {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) ListUserInboundsByNode(nodeID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if nodeID == "" || acc.NodeID == nodeID {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) ListUserInboundsByInbound(inboundID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, acc := range s.inbounds {
		if inboundID == "" || acc.InboundID == inboundID {
			out = append(out, acc)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) DeleteUserInbound(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inbounds[id]; !ok {
		return ErrUserInboundNotFound
	}
	delete(s.inbounds, id)
	return nil
}

func (s *MemoryStore) DeleteUserInboundsByUser(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, acc := range s.inbounds {
		if acc.UserID == userID {
			delete(s.inbounds, id)
		}
	}
	return nil
}

// ─── 订阅访问日志（内存 stub，仅测试用） ──────────────────────────────────────

func (s *MemoryStore) LogSubAccess(userID, ip, userAgent string) error {
	return nil
}

func (s *MemoryStore) ListSubAccessLogs(userID string, limit int) ([]SubAccessLog, error) {
	return nil, nil
}

func (s *MemoryStore) AddUserNodeTraffic(userID, nodeID, date string, upload, download int64) error {
	return nil
}

func (s *MemoryStore) ListUserNodeUsage(userID string) ([]UserNodeUsage, error) {
	return nil, nil
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

func usersMapToSlice(items map[string]User) []User {
	out := make([]User, 0, len(items))
	for _, user := range items {
		out = append(out, user)
	}
	return out
}

func sortUsers(out []User) []User {
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func inboundsMapToSlice(items map[string]UserInbound) []UserInbound {
	out := make([]UserInbound, 0, len(items))
	for _, acc := range items {
		out = append(out, acc)
	}
	return out
}

func sortInbounds(out []UserInbound) []UserInbound {
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}
