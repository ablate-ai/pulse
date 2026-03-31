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

// TestUsage_EvictedWithoutScan 验证：连接在两次 Usage() 调用间关闭且被驱逐出环形缓冲，
// 从未出现在 ClosedConnections() 中时，通过活跃连接追踪机制回收其字节数。
func TestUsage_EvictedWithoutScan(t *testing.T) {
	mgr := NewManager()
	fake := &fakeTrafficManager{}
	mgr.traffic = fake

	// 第一轮：连接 A 活跃，200/100 字节
	connA := makeConn("alice", 200, 100)
	fake.active = []*trafficontrol.TrackerMetadata{connA}
	fake.closed = nil

	stats1 := mgr.Usage()
	var alice1 UserUsage
	for _, u := range stats1.Users {
		if u.User == "alice" {
			alice1 = u
		}
	}
	if alice1.UploadTotal != 200 || alice1.DownloadTotal != 100 {
		t.Fatalf("第一轮: upload=%d download=%d, 期望 200/100", alice1.UploadTotal, alice1.DownloadTotal)
	}

	// 第二轮：A 关闭且被驱逐（不出现在 active 和 closed 中）
	// 模拟：两次调用间 A 关闭，且 1000+ 其他连接也关闭把 A 挤出缓冲
	fake.active = nil
	fake.closed = nil // A 从未出现在 ClosedConnections 中

	stats2 := mgr.Usage()
	var alice2 UserUsage
	for _, u := range stats2.Users {
		if u.User == "alice" {
			alice2 = u
		}
	}
	// 修复前：alice 的 200/100 字节完全丢失，UploadTotal=0
	// 修复后：通过 lastActiveConns 检测到 A 消失，补入上次已知的 200/100 字节
	if alice2.UploadTotal != 200 {
		t.Fatalf("第二轮 upload=%d, 期望 200（消失连接应通过活跃追踪回收）", alice2.UploadTotal)
	}
	if alice2.DownloadTotal != 100 {
		t.Fatalf("第二轮 download=%d, 期望 100", alice2.DownloadTotal)
	}

	// 第三轮：新连接 B 活跃
	connB := makeConn("alice", 50, 30)
	fake.active = []*trafficontrol.TrackerMetadata{connB}

	stats3 := mgr.Usage()
	var alice3 UserUsage
	for _, u := range stats3.Users {
		if u.User == "alice" {
			alice3 = u
		}
	}
	// A 的 200/100 在累积器中 + B 的 50/30 活跃 = 250/130
	if alice3.UploadTotal != 250 {
		t.Fatalf("第三轮 upload=%d, 期望 250 (200+50)", alice3.UploadTotal)
	}
	if alice3.DownloadTotal != 130 {
		t.Fatalf("第三轮 download=%d, 期望 130 (100+30)", alice3.DownloadTotal)
	}
}

// TestUsage_EvictedMultiUser 验证多用户场景下消失连接的回收。
func TestUsage_EvictedMultiUser(t *testing.T) {
	mgr := NewManager()
	fake := &fakeTrafficManager{}
	mgr.traffic = fake

	// 第一轮：两个用户各有活跃连接
	connA := makeConn("alice", 100, 50)
	connB := makeConn("bob", 200, 80)
	fake.active = []*trafficontrol.TrackerMetadata{connA, connB}
	fake.closed = nil

	mgr.Usage()

	// 第二轮：alice 的连接消失（驱逐），bob 的连接仍活跃
	connB.Upload.Store(300) // bob 继续传输
	connB.Download.Store(120)
	fake.active = []*trafficontrol.TrackerMetadata{connB}
	fake.closed = nil

	stats2 := mgr.Usage()
	var alice2, bob2 UserUsage
	for _, u := range stats2.Users {
		switch u.User {
		case "alice":
			alice2 = u
		case "bob":
			bob2 = u
		}
	}
	if alice2.UploadTotal != 100 {
		t.Errorf("alice upload=%d, want 100 (recovered from lastActive)", alice2.UploadTotal)
	}
	if bob2.UploadTotal != 300 {
		t.Errorf("bob upload=%d, want 300 (still active)", bob2.UploadTotal)
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
