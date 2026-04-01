// Package alert 提供推送告警功能，目前支持 Bark。
package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const dedupTTL = 30 * time.Minute

// SettingsReader 从持久化存储读取配置项的最小接口。
type SettingsReader interface {
	GetSetting(key string) (string, bool)
}

// BarkSender 通过 Bark 发送推送通知。
// Bark URL 每次发送时从 settings 读取，修改配置后立即生效。
// 相同标题+正文在 30 分钟内只发一次，避免告警风暴。
type BarkSender struct {
	settings SettingsReader
	mu       sync.Mutex
	lastSent map[string]time.Time
}

// NewBarkSender 创建从 settings 中读取配置的 BarkSender。
func NewBarkSender(s SettingsReader) *BarkSender {
	return &BarkSender{settings: s, lastSent: make(map[string]time.Time)}
}

// Send 发送推送通知。未配置 Bark URL 时静默返回 nil。
func (b *BarkSender) Send(ctx context.Context, title, body string) error {
	base, ok := b.settings.GetSetting("alert_bark_url")
	if !ok || strings.TrimSpace(base) == "" {
		return nil
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")

	// 30 分钟内相同内容不重复发送
	key := title + "\x00" + body
	b.mu.Lock()
	if t, seen := b.lastSent[key]; seen && time.Since(t) < dedupTTL {
		b.mu.Unlock()
		return nil
	}
	b.lastSent[key] = time.Now()
	b.mu.Unlock()

	pushURL := fmt.Sprintf("%s/%s/%s?group=Pulse&sound=default",
		base,
		url.PathEscape(title),
		url.PathEscape(body),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pushURL, nil)
	if err != nil {
		return fmt.Errorf("bark: build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bark: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("bark: server returned %d", resp.StatusCode)
	}
	return nil
}
