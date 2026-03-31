package singbox

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/experimental/clashapi/trafficontrol"
	"github.com/sagernet/sing-box/experimental/v2rayapi"
)

// fakeTrafficManager 模拟 trafficontrol.Manager 的行为，用于测试连接/设备统计。
type fakeTrafficManager struct {
	active []*trafficontrol.TrackerMetadata
}

func (f *fakeTrafficManager) Total() (int64, int64)                         { return 0, 0 }
func (f *fakeTrafficManager) ConnectionsLen() int                           { return len(f.active) }
func (f *fakeTrafficManager) Connections() []*trafficontrol.TrackerMetadata { return f.active }

// fakeV2RayStats 模拟 V2Ray StatsService 的 QueryStats 行为。
type fakeV2RayStats struct {
	mu       sync.Mutex
	counters map[string]int64
}

func newFakeV2RayStats() *fakeV2RayStats {
	return &fakeV2RayStats{counters: make(map[string]int64)}
}

func (f *fakeV2RayStats) QueryStats(_ context.Context, req *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var stats []*v2rayapi.Stat
	for name, value := range f.counters {
		matched := false
		for _, pattern := range req.Patterns {
			if strings.Contains(name, pattern) {
				matched = true
				break
			}
		}
		if len(req.Patterns) == 0 || matched {
			stats = append(stats, &v2rayapi.Stat{Name: name, Value: value})
			if req.Reset_ {
				f.counters[name] = 0
			}
		}
	}
	return &v2rayapi.QueryStatsResponse{Stat: stats}, nil
}

func (f *fakeV2RayStats) addTraffic(user string, upload, download int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters["user>>>"+user+">>>traffic>>>uplink"] += upload
	f.counters["user>>>"+user+">>>traffic>>>downlink"] += download
}

func makeConn(user string) *trafficontrol.TrackerMetadata {
	meta := &trafficontrol.TrackerMetadata{}
	meta.Metadata.User = user
	return meta
}

// TestUsage_V2RayStats_ResetDelta verifies that read-and-reset returns correct deltas
// and subsequent reads return zero.
func TestUsage_V2RayStats_ResetDelta(t *testing.T) {
	mgr := NewManager()
	v2ray := newFakeV2RayStats()
	mgr.v2rayStats = v2ray

	// Add traffic for alice
	v2ray.addTraffic("alice", 100, 50)
	v2ray.addTraffic("bob", 200, 80)

	// Read with reset=true: should get the full values
	stats1 := mgr.Usage(true)
	if !stats1.Available {
		t.Fatal("expected available=true")
	}
	aliceFound, bobFound := false, false
	for _, u := range stats1.Users {
		switch u.User {
		case "alice":
			aliceFound = true
			if u.UploadTotal != 100 || u.DownloadTotal != 50 {
				t.Errorf("alice: want 100/50, got %d/%d", u.UploadTotal, u.DownloadTotal)
			}
		case "bob":
			bobFound = true
			if u.UploadTotal != 200 || u.DownloadTotal != 80 {
				t.Errorf("bob: want 200/80, got %d/%d", u.UploadTotal, u.DownloadTotal)
			}
		}
	}
	if !aliceFound || !bobFound {
		t.Fatalf("expected both alice and bob in stats, got %+v", stats1.Users)
	}
	if stats1.UploadTotal != 300 || stats1.DownloadTotal != 130 {
		t.Errorf("totals: want 300/130, got %d/%d", stats1.UploadTotal, stats1.DownloadTotal)
	}

	// Read again with reset=true: counters were cleared, should get zero
	stats2 := mgr.Usage(true)
	for _, u := range stats2.Users {
		if u.UploadTotal != 0 || u.DownloadTotal != 0 {
			t.Errorf("%s: expected 0/0 after reset, got %d/%d", u.User, u.UploadTotal, u.DownloadTotal)
		}
	}

	// Add more traffic, read without reset
	v2ray.addTraffic("alice", 30, 10)
	stats3 := mgr.Usage(false)
	for _, u := range stats3.Users {
		if u.User == "alice" {
			if u.UploadTotal != 30 || u.DownloadTotal != 10 {
				t.Errorf("alice after new traffic: want 30/10, got %d/%d", u.UploadTotal, u.DownloadTotal)
			}
		}
	}
	// Read again without reset: values should be the same
	stats4 := mgr.Usage(false)
	for _, u := range stats4.Users {
		if u.User == "alice" {
			if u.UploadTotal != 30 || u.DownloadTotal != 10 {
				t.Errorf("alice (no reset): want 30/10, got %d/%d", u.UploadTotal, u.DownloadTotal)
			}
		}
	}
}

// TestUsage_V2RayStats_WithConnections verifies connection/device info from Clash API
// is correctly merged with V2Ray traffic stats.
func TestUsage_V2RayStats_WithConnections(t *testing.T) {
	mgr := NewManager()
	v2ray := newFakeV2RayStats()
	fake := &fakeTrafficManager{}
	mgr.v2rayStats = v2ray
	mgr.traffic = fake

	// Set up V2Ray traffic
	v2ray.addTraffic("alice", 100, 50)

	// Set up Clash API connections for alice
	conn1 := makeConn("alice")
	conn2 := makeConn("alice")
	fake.active = []*trafficontrol.TrackerMetadata{conn1, conn2}

	stats := mgr.Usage(false)

	var alice UserUsage
	for _, u := range stats.Users {
		if u.User == "alice" {
			alice = u
			break
		}
	}
	if alice.UploadTotal != 100 || alice.DownloadTotal != 50 {
		t.Errorf("traffic: want 100/50, got %d/%d", alice.UploadTotal, alice.DownloadTotal)
	}
	if alice.Connections != 2 {
		t.Errorf("connections: want 2, got %d", alice.Connections)
	}
	if stats.Connections != 2 {
		t.Errorf("total connections: want 2, got %d", stats.Connections)
	}
}

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
