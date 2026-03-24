package inbounds

import (
	"testing"
)

func TestMemoryStore_InboundCRUD(t *testing.T) {
	s := NewMemoryStore()

	inbound := Inbound{
		ID:       "ib-1",
		NodeID:   "node-1",
		Protocol: "vless",
		Tag:      "pulse-vless-1",
		Port:     443,
	}

	// Upsert
	got, err := s.UpsertInbound(inbound)
	if err != nil {
		t.Fatalf("UpsertInbound: %v", err)
	}
	if got.ID != inbound.ID {
		t.Fatalf("id mismatch: %s", got.ID)
	}

	// Get
	got, err = s.GetInbound("ib-1")
	if err != nil {
		t.Fatalf("GetInbound: %v", err)
	}
	if got.Port != 443 {
		t.Fatalf("port mismatch: %d", got.Port)
	}

	// ListByNode
	items, err := s.ListInboundsByNode("node-1")
	if err != nil || len(items) != 1 {
		t.Fatalf("ListInboundsByNode: %v, len=%d", err, len(items))
	}

	// Delete
	if err := s.DeleteInbound("ib-1"); err != nil {
		t.Fatalf("DeleteInbound: %v", err)
	}
	if _, err := s.GetInbound("ib-1"); !IsInboundNotFound(err) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestMemoryStore_HostCRUD(t *testing.T) {
	s := NewMemoryStore()

	_, _ = s.UpsertInbound(Inbound{ID: "ib-1", NodeID: "n1", Protocol: "vless", Tag: "tag", Port: 443})

	host := Host{
		ID:        "h-1",
		InboundID: "ib-1",
		Remark:    "主节点",
		Address:   "example.com",
		Security:  "tls",
	}

	got, err := s.UpsertHost(host)
	if err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	if got.Address != "example.com" {
		t.Fatalf("address mismatch: %s", got.Address)
	}

	hosts, err := s.ListHostsByInbound("ib-1")
	if err != nil || len(hosts) != 1 {
		t.Fatalf("ListHostsByInbound: %v, len=%d", err, len(hosts))
	}

	if err := s.DeleteHost("h-1"); err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}
}

func IsInboundNotFound(err error) bool {
	return err == ErrInboundNotFound
}
