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
	mu         sync.Mutex
	instance   *box.Box
	starting   bool
	startedAt  time.Time
	logs       []string
	traffic    trafficManager
	lastConfig string
	configFile string // 持久化路径，非空时 Start/Stop 会读写该文件
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
		logs: make([]string, 0, maxLogs),
	}
}

// NewManagerWithPersistence 创建带持久化的 Manager。
// configFile 为保存最近一次配置的文件路径；启动成功后写入，显式 Stop 后删除。
func NewManagerWithPersistence(configFile string) *Manager {
	return &Manager{
		logs:       make([]string, 0, maxLogs),
		configFile: configFile,
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

	userIndex := make(map[string]*UserUsage)
	add := func(metadata *trafficontrol.TrackerMetadata, active bool) {
		if metadata == nil {
			return
		}
		user := strings.TrimSpace(metadata.Metadata.User)
		if user == "" {
			user = "anonymous"
		}
		item, ok := userIndex[user]
		if !ok {
			stats.Users = append(stats.Users, UserUsage{User: user})
			item = &stats.Users[len(stats.Users)-1]
			userIndex[user] = item
		}
		if metadata.Upload != nil {
			item.UploadTotal += metadata.Upload.Load()
		}
		if metadata.Download != nil {
			item.DownloadTotal += metadata.Download.Load()
		}
		if active {
			item.Connections++
		}
	}

	for _, metadata := range traffic.Connections() {
		add(metadata, true)
	}
	for _, metadata := range traffic.ClosedConnections() {
		add(metadata, false)
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
