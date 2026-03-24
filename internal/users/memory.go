package users

import (
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu    sync.RWMutex
	users map[string]User
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users: make(map[string]User),
	}
}

func (s *MemoryStore) Upsert(user User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	if user.Protocol == "" {
		user.Protocol = "vless"
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

func (s *MemoryStore) Get(id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *MemoryStore) List() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortUsers(mapsToSlice(s.users)), nil
}

func (s *MemoryStore) ListByNode(nodeID string) ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]User, 0, len(s.users))
	for _, user := range s.users {
		if nodeID == "" || user.NodeID == nodeID {
			out = append(out, user)
		}
	}
	return sortUsers(out), nil
}

func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return ErrUserNotFound
	}
	delete(s.users, id)
	return nil
}

func mapsToSlice(items map[string]User) []User {
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
