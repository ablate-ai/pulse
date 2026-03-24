package singbox

import (
	"context"
	"testing"
)

func TestRuntimeInfoAndVersion(t *testing.T) {
	manager := NewManager()

	info := manager.RuntimeInfo(context.Background())
	if !info.Available {
		t.Fatalf("expected sing-box runtime available")
	}
	if info.Module == "" {
		t.Fatalf("expected module path")
	}

	version, err := manager.Version(context.Background())
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version == "" {
		t.Fatalf("unexpected version: %q", version)
	}
}
