//go:build js && wasm

package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// escape HTML 特殊字符，防止 XSS。
func escape(value string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(value)
}

// formatBytes 将字节数转换为人类可读格式。
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// formatBytesShort 精简版本，用于进度条标签。
func formatBytesShort(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	return formatBytes(b)
}

// formatLimit 格式化流量上限。
func formatLimit(b int64) string {
	if b <= 0 {
		return "∞"
	}
	return formatBytes(b)
}

const gbBytes = 1024 * 1024 * 1024

// randomPort 生成 10000-60000 范围内的随机端口。
func randomPort() string {
	return strconv.Itoa(10000 + rand.Intn(50000))
}


// gbToBytes 将 GB 字符串转换为字节数（支持小数）。
func gbToBytes(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 0
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int64(f * gbBytes)
}

// bytesToGBString 将字节数转换为 GB 字符串（用于回填输入框）。
func bytesToGBString(b int64) string {
	if b == 0 {
		return "0"
	}
	gb := float64(b) / gbBytes
	if gb == float64(int64(gb)) {
		return fmt.Sprintf("%d", int64(gb))
	}
	return strconv.FormatFloat(gb, 'f', 2, 64)
}

// parsePort 解析端口字符串，返回 1-65535 范围内的整数。
func parsePort(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port: %s", value)
	}
	return n, nil
}

// parseInt64 安全解析 int64。
func parseInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// displayTime 格式化时间字符串，空值返回 "—"。
func displayTime(value string) string {
	v := strings.TrimSpace(value)
	if v == "" || v == "0001-01-01T00:00:00Z" || v == "null" {
		return "—"
	}
	// 截短 RFC3339 到秒精度，去掉纳秒部分
	if len(v) > 19 {
		v = v[:19]
	}
	return strings.Replace(v, "T", " ", 1)
}

// statusBadge 返回带颜色 class 的状态 badge HTML。
func statusBadge(status string) string {
	var cls, label string
	switch status {
	case "active":
		cls, label = "badge-active", "Active"
	case "limited":
		cls, label = "badge-limited", "Limited"
	case "expired":
		cls, label = "badge-expired", "Expired"
	case "disabled":
		cls, label = "badge-disabled", "Disabled"
	case "on_hold":
		cls, label = "badge-on-hold", "On Hold"
	default:
		cls, label = "badge-unknown", escape(status)
	}
	return fmt.Sprintf(`<span class="badge %s">%s</span>`, cls, label)
}

// protoBadge 返回协议标签 HTML。
func protoBadge(proto string) string {
	return fmt.Sprintf(`<span class="proto-badge">%s</span>`, escape(strings.ToUpper(proto)))
}

// nodeBadge 返回节点运行状态 badge HTML。
func nodeBadge(running bool) string {
	if running {
		return `<span class="badge badge-running">Running</span>`
	}
	return `<span class="badge badge-stopped">Stopped</span>`
}

// trafficPercent 计算流量使用百分比，上限 100。
func trafficPercent(used, limit int64) int {
	if limit <= 0 {
		return 0
	}
	p := int(float64(used) / float64(limit) * 100)
	if p > 100 {
		return 100
	}
	return p
}

// trafficFillClass 根据使用率返回进度条颜色 class。
func trafficFillClass(pct int) string {
	if pct >= 90 {
		return "traffic-fill danger"
	}
	if pct >= 70 {
		return "traffic-fill warn"
	}
	return "traffic-fill"
}

// resetStrategyLabel 返回重置策略的中文标签。
func resetStrategyLabel(strategy string) string {
	switch strategy {
	case "day":
		return "每天重置"
	case "week":
		return "每周重置"
	case "month":
		return "每月重置"
	case "year":
		return "每年重置"
	default:
		return "不重置"
	}
}

// datetimeLocalValue 将 RFC3339 转为 datetime-local input 的格式 "YYYY-MM-DDTHH:MM"。
func datetimeLocalValue(value string) string {
	v := strings.TrimSpace(value)
	if v == "" || v == "null" || v == "0001-01-01T00:00:00Z" {
		return ""
	}
	if len(v) >= 16 {
		return v[:16]
	}
	return v
}

// datetimeToRFC3339 将 datetime-local "YYYY-MM-DDTHH:MM" 转为 RFC3339。
func datetimeToRFC3339(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "T") {
		return ""
	}
	return v + ":00Z"
}
