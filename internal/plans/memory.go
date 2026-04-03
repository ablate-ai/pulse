package plans

import (
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu    sync.RWMutex
	plans map[string]Plan
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{plans: make(map[string]Plan)}
}

func (s *MemoryStore) UpsertPlan(plan Plan) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	if plan.Currency == "" {
		plan.Currency = "usd"
	}
	s.plans[plan.ID] = plan
	return plan, nil
}

func (s *MemoryStore) GetPlan(id string) (Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[id]
	if !ok {
		return Plan{}, ErrPlanNotFound
	}
	return p, nil
}

func (s *MemoryStore) ListPlans() ([]Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}

func (s *MemoryStore) ListEnabledPlans() ([]Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Plan, 0)
	for _, p := range s.plans {
		if p.Enabled {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}

func (s *MemoryStore) DeletePlan(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[id]; !ok {
		return ErrPlanNotFound
	}
	delete(s.plans, id)
	return nil
}
