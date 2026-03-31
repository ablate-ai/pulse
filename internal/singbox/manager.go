package singbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	_ "github.com/sagernet/sing-box/experimental/clashapi"
	"github.com/sagernet/sing-box/experimental/clashapi/trafficontrol"
	"github.com/sagernet/sing-box/experimental/v2rayapi"
	"github.com/sagernet/sing-box/include"
	sbLog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sbJSON "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

// v2rayQuerier abstracts the V2Ray StatsService for per-user traffic counters.
type v2rayQuerier interface {
	QueryStats(ctx context.Context, request *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error)
}

const maxLogs = 200

var ErrNotRunning = errors.New("sing-box is not running")

type Manager struct {
	mu          sync.Mutex
	resetMu     sync.Mutex // serializes Usage(reset=true) to prevent concurrent Swap(0) races
	instance    *box.Box
	starting    bool
	startedAt   time.Time
	logs        []string
	traffic     trafficManager // Clash API: connections/devices only
	v2rayStats  v2rayQuerier   // V2Ray Stats: per-user traffic (atomic read-and-reset)
	lastConfig  string
	configFile  string // 持久化路径，非空时 Start/Stop 会读写该文件
	subscribers map[int64]chan string
	nextSubID   int64
}

type trafficManager interface {
	Total() (up int64, down int64)
	ConnectionsLen() int
	Connections() []*trafficontrol.TrackerMetadata
}

type Status struct {
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

type RuntimeInfo struct {
	Available bool   `json:"available"`
	Module    string `json:"module"`
	Version   string `json:"version,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type UsageStats struct {
	Available     bool        `json:"available"`
	Running       bool        `json:"running"`
	StartedAt     time.Time   `json:"started_at,omitempty"`
	UploadTotal   int64       `json:"upload_total"`
	DownloadTotal int64       `json:"download_total"`
	Connections   int         `json:"connections"`
	Users         []UserUsage `json:"users"`
}

type UserUsage struct {
	User          string `json:"user"`
	UploadTotal   int64  `json:"upload_total"`
	DownloadTotal int64  `json:"download_total"`
	Connections   int    `json:"connections"`
	Devices       int    `json:"devices"`
}

func NewManager() *Manager {
	return &Manager{
		logs:        make([]string, 0, maxLogs),
		subscribers: make(map[int64]chan string),
	}
}

// NewManagerWithPersistence 创建带持久化的 Manager。
// configFile 为保存最近一次配置的文件路径；启动成功后写入，显式 Stop 后删除。
func NewManagerWithPersistence(configFile string) *Manager {
	return &Manager{
		logs:        make([]string, 0, maxLogs),
		configFile:  configFile,
		subscribers: make(map[int64]chan string),
	}
}

// SavedConfig 读取磁盘上持久化的配置（进程重启后自动恢复用）。
// 文件不存在时返回空字符串，不报错。
func (m *Manager) SavedConfig() string {
	if m.configFile == "" {
		return ""
	}
	data, err := os.ReadFile(m.configFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (m *Manager) Start(config string) error {
	m.mu.Lock()
	if m.instance != nil {
		m.mu.Unlock()
		return errors.New("sing-box is already running")
	}
	if m.starting {
		m.mu.Unlock()
		return errors.New("sing-box is already starting")
	}
	m.starting = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.starting = false
		m.mu.Unlock()
	}()

	ctx := include.Context(context.Background())
	var options option.Options
	if err := sbJSON.UnmarshalContext(ctx, []byte(config), &options); err != nil {
		return fmt.Errorf("parse sing-box config: %w", err)
	}

	instance, err := box.New(box.Options{
		Context:           ctx,
		Options:           options,
		PlatformLogWriter: platformWriter{manager: m},
	})
	if err != nil {
		return fmt.Errorf("create sing-box instance: %w", err)
	}

	if err := instance.Start(); err != nil {
		return fmt.Errorf("start sing-box: %w", err)
	}

	m.mu.Lock()
	m.instance = instance
	m.startedAt = time.Now().UTC()
	m.lastConfig = config
	m.traffic = extractTrafficManager(ctx)
	m.v2rayStats = extractV2RayStats(ctx)
	m.appendLogLocked("sing-box started")
	configFile := m.configFile
	m.mu.Unlock()

	// 持久化配置，进程重启后可自动恢复
	if configFile != "" {
		_ = os.WriteFile(configFile, []byte(config), 0600)
	}
	return nil
}

// Config 返回最近一次成功启动时使用的配置（JSON 字符串）。
func (m *Manager) Config() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastConfig
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	instance := m.instance
	if instance == nil {
		m.mu.Unlock()
		return ErrNotRunning
	}
	// 立即清除引用，防止并发 Stop/Restart 对同一 instance 重复 Close
	m.instance = nil
	m.startedAt = time.Time{}
	m.traffic = nil
	m.v2rayStats = nil
	configFile := m.configFile
	m.appendLogLocked("sing-box stopped")
	m.mu.Unlock()

	if err := instance.Close(); err != nil && !isClosedConnError(err) {
		return fmt.Errorf("close sing-box: %w", err)
	}

	// 显式停止时清除持久化配置，避免下次进程启动时自动恢复
	if configFile != "" {
		_ = os.Remove(configFile)
	}
	return nil
}

func (m *Manager) Restart(config string) error {
	if err := m.Stop(); err != nil && !errors.Is(err, ErrNotRunning) {
		return err
	}
	return m.Start(config)
}

func (m *Manager) Version(context.Context) (string, error) {
	info := buildInfo()
	if info.Version == "" {
		return "embedded", nil
	}
	return info.Version, nil
}

func (m *Manager) RuntimeInfo(context.Context) RuntimeInfo {
	return buildInfo()
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Status{
		Running:   m.instance != nil,
		StartedAt: m.startedAt,
	}
}

func (m *Manager) Logs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, len(m.logs))
	copy(out, m.logs)
	return out
}

func (m *Manager) Usage(reset bool) UsageStats {
	m.mu.Lock()
	running := m.instance != nil
	startedAt := m.startedAt
	traffic := m.traffic
	v2ray := m.v2rayStats
	m.mu.Unlock()

	stats := UsageStats{
		Available: v2ray != nil || traffic != nil,
		Running:   running,
		StartedAt: startedAt,
		Users:     make([]UserUsage, 0),
	}

	// V2Ray Stats: per-user traffic (precise, no ring buffer)
	if v2ray != nil {
		if reset {
			m.resetMu.Lock()
			defer m.resetMu.Unlock()
		}
		resp, err := v2ray.QueryStats(context.Background(), &v2rayapi.QueryStatsRequest{
			Patterns: []string{"user>>>"},
			Reset_:   reset,
		})
		if err == nil && resp != nil {
			userTraffic := make(map[string]*UserUsage)
			for _, stat := range resp.Stat {
				// stat.Name format: "user>>>alice>>>traffic>>>uplink"
				parts := strings.SplitN(stat.Name, ">>>", 4)
				if len(parts) != 4 || parts[0] != "user" {
					continue
				}
				username := parts[1]
				direction := parts[3] // "uplink" or "downlink"
				uu, ok := userTraffic[username]
				if !ok {
					uu = &UserUsage{User: username}
					userTraffic[username] = uu
				}
				switch direction {
				case "uplink":
					uu.UploadTotal = stat.Value
				case "downlink":
					uu.DownloadTotal = stat.Value
				}
			}
			for _, uu := range userTraffic {
				stats.Users = append(stats.Users, *uu)
				stats.UploadTotal += uu.UploadTotal
				stats.DownloadTotal += uu.DownloadTotal
			}
		}
	}

	// Clash API: connections, devices, connection count
	if traffic != nil {
		stats.Connections = traffic.ConnectionsLen()
		userIPs := make(map[string]map[string]struct{})
		userConns := make(map[string]int)
		for _, meta := range traffic.Connections() {
			if meta == nil {
				continue
			}
			user := strings.TrimSpace(meta.Metadata.User)
			if user == "" {
				user = "anonymous"
			}
			userConns[user]++
			ip := meta.Metadata.Source.Addr.String()
			if ip != "" {
				if userIPs[user] == nil {
					userIPs[user] = make(map[string]struct{})
				}
				userIPs[user][ip] = struct{}{}
			}
		}
		// Merge connection/device info into user entries
		userIndex := make(map[string]int, len(stats.Users))
		for i, u := range stats.Users {
			userIndex[u.User] = i
		}
		for user, conns := range userConns {
			if idx, ok := userIndex[user]; ok {
				stats.Users[idx].Connections = conns
				stats.Users[idx].Devices = len(userIPs[user])
			}
		}
	}

	sort.Slice(stats.Users, func(i, j int) bool {
		left := stats.Users[i].UploadTotal + stats.Users[i].DownloadTotal
		right := stats.Users[j].UploadTotal + stats.Users[j].DownloadTotal
		if left == right {
			return stats.Users[i].User < stats.Users[j].User
		}
		return left > right
	})

	return stats
}

// Subscribe 注册一个日志订阅者，返回订阅 ID 和只读 channel。
// 调用方负责在完成时调用 Unsubscribe(id) 释放资源。
func (m *Manager) Subscribe() (id int64, ch <-chan string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSubID++
	id = m.nextSubID
	c := make(chan string, 64)
	m.subscribers[id] = c
	return id, c
}

// Unsubscribe 注销订阅者并关闭其 channel。
func (m *Manager) Unsubscribe(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.subscribers[id]; ok {
		close(c)
		delete(m.subscribers, id)
	}
}

func (m *Manager) appendLogLocked(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	if len(m.logs) == maxLogs {
		copy(m.logs, m.logs[1:])
		m.logs = m.logs[:maxLogs-1]
	}
	m.logs = append(m.logs, line)

	// 广播给所有订阅者，慢消费者直接丢弃
	for _, c := range m.subscribers {
		select {
		case c <- line:
		default:
		}
	}
}

func buildInfo() RuntimeInfo {
	info := RuntimeInfo{
		Available: true,
		Module:    "github.com/sagernet/sing-box",
	}

	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		info.LastError = "build info unavailable"
		return info
	}

	for _, dep := range buildInfo.Deps {
		if dep.Path == info.Module {
			info.Version = dep.Version
			return info
		}
	}

	info.Version = "embedded"
	info.LastError = "sing-box module not found in build info"
	return info
}

func extractTrafficManager(ctx context.Context) trafficManager {
	clashServer := service.FromContext[adapter.ClashServer](ctx)
	if clashServer == nil {
		return nil
	}
	provider, ok := clashServer.(interface {
		TrafficManager() *trafficontrol.Manager
	})
	if !ok {
		return nil
	}
	return provider.TrafficManager()
}

func extractV2RayStats(ctx context.Context) v2rayQuerier {
	v2ray := service.FromContext[adapter.V2RayServer](ctx)
	if v2ray == nil {
		return nil
	}
	tracker := v2ray.StatsService()
	if tracker == nil {
		return nil
	}
	if querier, ok := tracker.(v2rayQuerier); ok {
		return querier
	}
	return nil
}

type platformWriter struct {
	manager *Manager
}

func (w platformWriter) WriteMessage(level sbLog.Level, message string) {
	w.manager.mu.Lock()
	defer w.manager.mu.Unlock()
	w.manager.appendLogLocked(sbLog.FormatLevel(level) + ": " + message)
}

// isClosedConnError 检查错误链中是否包含 "use of closed network connection"。
// sing-box Close() 时内部组件（如 V2Ray Stats TCP listener）可能已被先行关闭，
// 再关一次产生此错误；资源实际已释放，可安全忽略。
func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return strings.Contains(netErr.Err.Error(), "use of closed network connection")
	}
	return strings.Contains(err.Error(), "use of closed network connection")
}
