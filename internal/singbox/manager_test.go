package singbox

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/gofrs/uuid/v5"
	"github.com/sagernet/sing-box/experimental/clashapi/trafficontrol"
)

// fakeTrafficManager 模拟 trafficontrol.Manager 的行为，用于测试。
type fakeTrafficManager struct {
	active []*trafficontrol.TrackerMetadata
	closed []*trafficontrol.TrackerMetadata
}

func (f *fakeTrafficManager) Total() (int64, int64)                           { return 0, 0 }
func (f *fakeTrafficManager) ConnectionsLen() int                             { return len(f.active) }
func (f *fakeTrafficManager) Connections() []*trafficontrol.TrackerMetadata   { return f.active }
func (f *fakeTrafficManager) ClosedConnections() []*trafficontrol.TrackerMetadata { return f.closed }

func makeConn(user string, upload, download int64) *trafficontrol.TrackerMetadata {
	id, _ := uuid.NewV4()
	up := new(atomic.Int64)
	up.Store(upload)
	down := new(atomic.Int64)
	down.Store(download)
	meta := &trafficontrol.TrackerMetadata{
		ID:       id,
		Upload:   up,
		Download: down,
	}
	meta.Metadata.User = user
	return meta
}

// TestUsage_ClosedBufferEviction 验证：当已关闭连接被驱逐出环形缓冲区时，
// 流量不会被重复计算（这是之前 double-count bug 的复现）。
func TestUsage_ClosedBufferEviction(t *testing.T) {
	mgr := NewManager()
	fake := &fakeTrafficManager{}
	mgr.traffic = fake

	// 第一轮：连接 A 活跃，100 字节
	connA := makeConn("alice", 100, 0)
	fake.active = []*trafficontrol.TrackerMetadata{connA}
	fake.closed = nil

	stats1 := mgr.Usage()
	alice1 := int64(0)
	for _, u := range stats1.Users {
		if u.User == "alice" {
			alice1 = u.UploadTotal
		}
	}
	if alice1 != 100 {
		t.Fatalf("第一轮 alice upload = %d, 期望 100", alice1)
	}

	// 第二轮：A 关闭（100 字节），进入 closed 缓冲
	fake.active = nil
	fake.closed = []*trafficontrol.TrackerMetadata{connA}

	stats2 := mgr.Usage()
	alice2 := int64(0)
	for _, u := range stats2.Users {
		if u.User == "alice" {
			alice2 = u.UploadTotal
		}
	}
	if alice2 != 100 {
		t.Fatalf("第二轮 alice upload = %d, 期望 100（连接关闭，总量不变）", alice2)
	}

	// 第三轮：模拟缓冲区驱逐（连接 A 消失）+ 新连接 B 也关闭（50 字节）
	connB := makeConn("alice", 50, 0)
	fake.closed = []*trafficontrol.TrackerMetadata{connB} // A 被驱逐

	stats3 := mgr.Usage()
	alice3 := int64(0)
	for _, u := range stats3.Users {
		if u.User == "alice" {
			alice3 = u.UploadTotal
		}
	}
	// 正确结果：100（A，已在第二轮累积）+ 50（B，本轮新增）= 150
	// Bug 行为：A 被驱逐后 total 变成 50 < cursor，错误地当成重启，返回 50，导致重复计算
	if alice3 != 150 {
		t.Fatalf("第三轮 alice upload = %d, 期望 150（缓冲驱逐后不应丢失 A 的 100 字节）", alice3)
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
