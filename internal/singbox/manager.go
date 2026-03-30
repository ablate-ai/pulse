package singbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid/v5"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	_ "github.com/sagernet/sing-box/experimental/clashapi"
	"github.com/sagernet/sing-box/experimental/clashapi/trafficontrol"
	"github.com/sagernet/sing-box/include"
	sbLog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	sbJSON "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

const maxLogs = 200

var ErrNotRunning = errors.New("sing-box is not running")

type Manager struct {
	mu          sync.Mutex
	instance    *box.Box
	starting    bool
	startedAt   time.Time
	logs        []string
	traffic     trafficManager
	lastConfig  string
	configFile  string // 持久化路径，非空时 Start/Stop 会读写该文件
	subscribers map[int64]chan string
	nextSubID   int64

	// 每用户流量累积器：汇总已关闭连接的字节数。
	// sing-box 的 ClosedConnections() 是容量 1000 的环形缓冲，超限后旧连接被驱逐，
	// 驱逐时字节数丢失会导致游标判断误判为节点重启，造成流量重复计算。
	// 通过追踪已见过的连接 ID，在驱逐发生前捕获字节，保证累积值单调递增。
	closedUserUpload   map[string]int64
	closedUserDownload map[string]int64
	seenClosedConnIDs  map[uuid.UUID]struct{}
}

type trafficManager interface {
	Total() (up int64, down int64)
	ConnectionsLen() int
	Connections() []*trafficontrol.TrackerMetadata
	ClosedConnections() []*trafficontrol.TrackerMetadata
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
}

func NewManager() *Manager {
	return &Manager{
		logs:               make([]string, 0, maxLogs),
		subscribers:        make(map[int64]chan string),
		closedUserUpload:   make(map[string]int64),
		closedUserDownload: make(map[string]int64),
		seenClosedConnIDs:  make(map[uuid.UUID]struct{}),
	}
}

// NewManagerWithPersistence 创建带持久化的 Manager。
// configFile 为保存最近一次配置的文件路径；启动成功后写入，显式 Stop 后删除。
func NewManagerWithPersistence(configFile string) *Manager {
	return &Manager{
		logs:               make([]string, 0, maxLogs),
		configFile:         configFile,
		subscribers:        make(map[int64]chan string),
		closedUserUpload:   make(map[string]int64),
		closedUserDownload: make(map[string]int64),
		seenClosedConnIDs:  make(map[uuid.UUID]struct{}),
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
	m.mu.Unlock()

	if err := instance.Close(); err != nil {
		return fmt.Errorf("close sing-box: %w", err)
	}

	m.mu.Lock()
	configFile := m.configFile
	if m.instance == instance {
		m.instance = nil
		m.startedAt = time.Time{}
		m.traffic = nil
		// sing-box 重启后连接计数从零开始，清空累积器避免游标错位
		m.closedUserUpload = make(map[string]int64)
		m.closedUserDownload = make(map[string]int64)
		m.seenClosedConnIDs = make(map[uuid.UUID]struct{})
		m.appendLogLocked("sing-box stopped")
	}
	m.mu.Unlock()

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

func (m *Manager) Usage() UsageStats {
	m.mu.Lock()
	running := m.instance != nil
	startedAt := m.startedAt
	traffic := m.traffic
	m.mu.Unlock()

	stats := UsageStats{
		Available: traffic != nil,
		Running:   running,
		StartedAt: startedAt,
		Users:     make([]UserUsage, 0),
	}
	if traffic == nil {
		return stats
	}

	stats.UploadTotal, stats.DownloadTotal = traffic.Total()
	stats.Connections = traffic.ConnectionsLen()

	m.mu.Lock()

	// 将新出现的已关闭连接字节数累积到 per-user 计数器中。
	// ClosedConnections() 是容量 1000 的环形缓冲，超限时旧连接被驱逐导致总量下降。
	// 通过 seenClosedConnIDs 追踪已处理的连接，确保每条连接只计一次，
	// 驱逐后字节数已在 closedUserUpload/Download 中保存，不会丢失。
	closedConns := traffic.ClosedConnections()
	newSeenIDs := make(map[uuid.UUID]struct{}, len(closedConns))
	for _, meta := range closedConns {
		newSeenIDs[meta.ID] = struct{}{}
		if _, already := m.seenClosedConnIDs[meta.ID]; !already {
			user := strings.TrimSpace(meta.Metadata.User)
			if user == "" {
				user = "anonymous"
			}
			if meta.Upload != nil {
				m.closedUserUpload[user] += meta.Upload.Load()
			}
			if meta.Download != nil {
				m.closedUserDownload[user] += meta.Download.Load()
			}
		}
	}
	// 只保留仍在缓冲区中的 ID，防止 map 无限增长
	m.seenClosedConnIDs = newSeenIDs

	// 快照累积值，供后续计算使用
	closedUploadSnap := make(map[string]int64, len(m.closedUserUpload))
	for k, v := range m.closedUserUpload {
		closedUploadSnap[k] = v
	}
	closedDownloadSnap := make(map[string]int64, len(m.closedUserDownload))
	for k, v := range m.closedUserDownload {
		closedDownloadSnap[k] = v
	}

	m.mu.Unlock()

	// 构建 per-user 统计：已关闭连接累积值 + 当前活跃连接实时字节数
	userIndex := make(map[string]*UserUsage)
	ensureUser := func(user string) *UserUsage {
		item, ok := userIndex[user]
		if !ok {
			stats.Users = append(stats.Users, UserUsage{
				User:          user,
				UploadTotal:   closedUploadSnap[user],
				DownloadTotal: closedDownloadSnap[user],
			})
			item = &stats.Users[len(stats.Users)-1]
			userIndex[user] = item
		}
		return item
	}

	// 预先为有已关闭流量的用户创建条目
	for user := range closedUploadSnap {
		ensureUser(user)
	}
	for user := range closedDownloadSnap {
		ensureUser(user)
	}

	// 叠加当前活跃连接的字节数
	for _, meta := range traffic.Connections() {
		if meta == nil {
			continue
		}
		user := strings.TrimSpace(meta.Metadata.User)
		if user == "" {
			user = "anonymous"
		}
		item := ensureUser(user)
		if meta.Upload != nil {
			item.UploadTotal += meta.Upload.Load()
		}
		if meta.Download != nil {
			item.DownloadTotal += meta.Download.Load()
		}
		item.Connections++
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

type platformWriter struct {
	manager *Manager
}

func (w platformWriter) WriteMessage(level sbLog.Level, message string) {
	w.manager.mu.Lock()
	defer w.manager.mu.Unlock()
	w.manager.appendLogLocked(sbLog.FormatLevel(level) + ": " + message)
}
