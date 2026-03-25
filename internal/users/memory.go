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

func (s *MemoryStore) UpsertUserInbound(inbound UserInbound) (UserInbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inbound.CreatedAt.IsZero() {
		inbound.CreatedAt = time.Now().UTC()
	}
	if inbound.Protocol == "" {
		inbound.Protocol = "vless"
	}
	s.inbounds[inbound.ID] = inbound
	return inbound, nil
}

func (s *MemoryStore) GetUserInbound(id string) (UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ib, ok := s.inbounds[id]
	if !ok {
		return UserInbound{}, ErrUserInboundNotFound
	}
	return ib, nil
}

func (s *MemoryStore) ListUserInbounds() ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortInbounds(inboundsMapToSlice(s.inbounds)), nil
}

func (s *MemoryStore) ListUserInboundsByUser(userID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, ib := range s.inbounds {
		if ib.UserID == userID {
			out = append(out, ib)
		}
	}
	return sortInbounds(out), nil
}

func (s *MemoryStore) ListUserInboundsByNode(nodeID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UserInbound, 0)
	for _, ib := range s.inbounds {
		if nodeID == "" || ib.NodeID == nodeID {
			out = append(out, ib)
		}
	}
	return sortInbounds(out), nil
}

// ListCursorInboundsByNode 返回每个 (nodeID, userID) 组合中 ID 最小的 inbound。
func (s *MemoryStore) ListCursorInboundsByNode(nodeID string) ([]UserInbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 按 userID 分组，取每组中 ID 最小的 inbound
	type key struct{ userID string }
	cursors := make(map[key]UserInbound)

	for _, ib := range s.inbounds {
		if ib.NodeID != nodeID {
			continue
		}
		k := key{userID: ib.UserID}
		existing, ok := cursors[k]
		if !ok || ib.ID < existing.ID {
			cursors[k] = ib
		}
	}

	out := make([]UserInbound, 0, len(cursors))
	for _, ib := range cursors {
		out = append(out, ib)
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
	for id, ib := range s.inbounds {
		if ib.UserID == userID {
			delete(s.inbounds, id)
		}
	}
	return nil
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
	for _, ib := range items {
		out = append(out, ib)
	}
	return out
}

func sortInbounds(out []UserInbound) []UserInbound {
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}
