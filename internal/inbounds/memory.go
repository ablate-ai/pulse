package inbounds

import (
	"sort"
	"sync"
)

type MemoryStore struct {
	mu       sync.RWMutex
	inbounds map[string]Inbound
	hosts    map[string]Host
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		inbounds: make(map[string]Inbound),
		hosts:    make(map[string]Host),
	}
}

func (s *MemoryStore) UpsertInbound(inbound Inbound) (Inbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbounds[inbound.ID] = inbound
	return inbound, nil
}

func (s *MemoryStore) GetInbound(id string) (Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inbound, ok := s.inbounds[id]
	if !ok {
		return Inbound{}, ErrInboundNotFound
	}
	return inbound, nil
}

func (s *MemoryStore) ListInbounds() ([]Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Inbound, 0, len(s.inbounds))
	for _, inbound := range s.inbounds {
		out = append(out, inbound)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) ListInboundsByNode(nodeID string) ([]Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Inbound, 0)
	for _, inbound := range s.inbounds {
		if inbound.NodeID == nodeID {
			out = append(out, inbound)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) DeleteInbound(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inbounds[id]; !ok {
		return ErrInboundNotFound
	}
	delete(s.inbounds, id)
	return nil
}

func (s *MemoryStore) UpsertHost(host Host) (Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[host.ID] = host
	return host, nil
}

func (s *MemoryStore) GetHost(id string) (Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	host, ok := s.hosts[id]
	if !ok {
		return Host{}, ErrHostNotFound
	}
	return host, nil
}

func (s *MemoryStore) ListHosts() ([]Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Host, 0, len(s.hosts))
	for _, host := range s.hosts {
		out = append(out, host)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) ListHostsByInbound(inboundID string) ([]Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Host, 0)
	for _, host := range s.hosts {
		if host.InboundID == inboundID {
			out = append(out, host)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) DeleteHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hosts[id]; !ok {
		return ErrHostNotFound
	}
	delete(s.hosts, id)
	return nil
}
