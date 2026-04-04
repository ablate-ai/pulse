package nodes

import (
	"errors"
	"time"
)

var ErrNodeNotFound = errors.New("node not found")

type Node struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	BaseURL          string  `json:"base_url"`
	TrafficRate      float64 `json:"traffic_rate"` // 流量倍率，默认 1.0，影响用户计费流量
	UploadBytes      int64   `json:"upload_bytes"`
	DownloadBytes    int64   `json:"download_bytes"`
	CaddyACMEEmail    string  `json:"caddy_acme_email"`
	CaddyPanelDomain  string  `json:"caddy_panel_domain"`
	CaddyExtraProxies string  `json:"caddy_extra_proxies"` // 额外反代规则，每行一条 "domain:port"
	CaddyEnabled      bool    `json:"caddy_enabled"`
}

// CheckResult 节点解锁检测结果，按 (node_id, service, check_type) 唯一存储。
type CheckResult struct {
	Service   string    `json:"service"`
	CheckType string    `json:"check_type"` // "direct" 或 "proxied"
	Unlocked  bool      `json:"unlocked"`
	Region    string    `json:"region"`
	Note      string    `json:"note"`
	CheckedAt time.Time `json:"checked_at"`
}

// SpeedTestResult 节点测速结果，按 node_id 唯一存储。
type SpeedTestResult struct {
	DownBps  int64     `json:"down_bps"`
	UpBps    int64     `json:"up_bps"`
	TestedAt time.Time `json:"tested_at"`
}

// NodeDailyUsage 某节点某日的流量快照。
type NodeDailyUsage struct {
	NodeID        string
	Date          string // YYYY-MM-DD
	UploadBytes   int64
	DownloadBytes int64
}

type Store interface {
	Upsert(node Node) (Node, error)
	Delete(id string) error
	Get(id string) (Node, error)
	List() ([]Node, error)
	// AddTraffic 原子性地将 upload/download 字节数累加到节点流量计数器。
	AddTraffic(nodeID string, upload, download int64) error
	UpdateCaddyConfig(nodeID, acmeEmail, panelDomain, extraProxies string, caddyEnabled bool) error
	// AddNodeDailyUsage 将 delta 流量累加到当日统计桶（幂等 upsert）。
	AddNodeDailyUsage(nodeID, date string, upload, download int64) error
	// ListNodeDailyUsage 返回最近 days 天内所有节点的日流量记录。
	ListNodeDailyUsage(days int) ([]NodeDailyUsage, error)
	// CleanupOldDailyUsage 删除超过 retainDays 天的历史日流量记录。
	CleanupOldDailyUsage(retainDays int) error
	// UpsertNodeCheckResults 批量写入节点解锁检测结果（按 node_id+service 唯一）。
	UpsertNodeCheckResults(nodeID string, results []CheckResult) error
	// ListAllNodeCheckResults 返回所有节点的解锁检测结果，按 nodeID 分组。
	ListAllNodeCheckResults() (map[string][]CheckResult, error)
	// UpsertNodeSpeedTest 写入节点测速结果（按 node_id 唯一）。
	UpsertNodeSpeedTest(nodeID string, result SpeedTestResult) error
	// ListAllNodeSpeedTests 返回所有节点的最新测速结果。
	ListAllNodeSpeedTests() (map[string]SpeedTestResult, error)
}
