package orders

import (
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu     sync.RWMutex
	orders map[string]Order
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{orders: make(map[string]Order)}
}

func (s *MemoryStore) UpsertOrder(order Order) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if order.CreatedAt.IsZero() {
		order.CreatedAt = time.Now().UTC()
	}
	if order.Status == "" {
		order.Status = StatusPending
	}
	if order.Currency == "" {
		order.Currency = "usd"
	}
	s.orders[order.ID] = order
	return order, nil
}

func (s *MemoryStore) GetOrder(id string) (Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	if !ok {
		return Order{}, ErrOrderNotFound
	}
	return o, nil
}

func (s *MemoryStore) GetOrderByStripeSession(sessionID string) (Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.orders {
		if o.StripeSessionID == sessionID {
			return o, nil
		}
	}
	return Order{}, ErrOrderNotFound
}

func (s *MemoryStore) GetOrderByStripeSubscription(subscriptionID string) (Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, o := range s.orders {
		if o.StripeSubscriptionID == subscriptionID {
			return o, nil
		}
	}
	return Order{}, ErrOrderNotFound
}

func (s *MemoryStore) ListOrders() ([]Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Order, 0, len(s.orders))
	for _, o := range s.orders {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ListOrdersByUser(userID string) ([]Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Order, 0)
	for _, o := range s.orders {
		if o.UserID == userID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) ListOrdersByEmail(email string) ([]Order, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Order, 0)
	for _, o := range s.orders {
		if o.Email == email {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) DeleteOrder(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orders[id]; !ok {
		return ErrOrderNotFound
	}
	delete(s.orders, id)
	return nil
}
