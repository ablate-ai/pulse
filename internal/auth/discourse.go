package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const nonceExpiry = 10 * time.Minute

// DiscourseConfig 包含与 Discourse SSO（DiscourseConnect）集成所需的配置。
// URL 和 Secret 同时非空时视为启用。
type DiscourseConfig struct {
	URL        string   // Discourse 实例地址，如 https://forum.example.com
	Secret     string   // Discourse 后台配置的 Connect Secret
	AdminUsers []string // 允许登录的用户名白名单；空则信任所有 Discourse 用户

	nonceMu sync.Mutex
	nonces  map[string]time.Time // nonce → 创建时间
}

// NewDiscourseConfig 创建已初始化内部字段的 DiscourseConfig。
func NewDiscourseConfig(discourseURL, secret, adminUsers string) *DiscourseConfig {
	cfg := &DiscourseConfig{
		URL:    strings.TrimRight(discourseURL, "/"),
		Secret: secret,
		nonces: make(map[string]time.Time),
	}
	if adminUsers != "" {
		for _, u := range strings.Split(adminUsers, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				cfg.AdminUsers = append(cfg.AdminUsers, u)
			}
		}
	}
	return cfg
}

// Enabled 返回 Discourse SSO 是否已配置。
func (d *DiscourseConfig) Enabled() bool {
	return d != nil && d.URL != "" && d.Secret != ""
}

// BuildRedirectURL 构造跳转到 Discourse 的 SSO 发起 URL。
// returnURL 是 Discourse 认证完成后的回调地址（即本服务的 callback 端点）。
func (d *DiscourseConfig) BuildRedirectURL(returnURL string) (string, error) {
	nonce, err := d.generateNonce()
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	payload := url.Values{
		"nonce":          {nonce},
		"return_sso_url": {returnURL},
	}.Encode()
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	sig := d.sign(encoded)
	target := d.URL + "/session/sso_provider"
	return fmt.Sprintf("%s?sso=%s&sig=%s", target, url.QueryEscape(encoded), sig), nil
}

// ParseCallback 验证并解析 Discourse 回调的 sso/sig 参数，返回 Discourse 用户名。
func (d *DiscourseConfig) ParseCallback(rawSSO, sig string) (string, error) {
	// 验证 HMAC 签名
	expected := d.sign(rawSSO)
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return "", errors.New("invalid signature")
	}
	// 解码 payload
	decoded, err := base64.StdEncoding.DecodeString(rawSSO)
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	params, err := url.ParseQuery(string(decoded))
	if err != nil {
		return "", fmt.Errorf("parse payload: %w", err)
	}
	// 验证 nonce（防重放攻击）
	if !d.consumeNonce(params.Get("nonce")) {
		return "", errors.New("invalid or expired nonce")
	}
	username := params.Get("username")
	if username == "" {
		return "", errors.New("missing username in payload")
	}
	return username, nil
}

// IsAllowed 检查 Discourse 用户名是否有权限登录。
// 白名单为空时信任所有 Discourse 用户。
func (d *DiscourseConfig) IsAllowed(username string) bool {
	if len(d.AdminUsers) == 0 {
		return true
	}
	for _, u := range d.AdminUsers {
		if strings.EqualFold(u, username) {
			return true
		}
	}
	return false
}

func (d *DiscourseConfig) generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(b)
	d.nonceMu.Lock()
	d.nonces[nonce] = time.Now()
	d.nonceMu.Unlock()
	return nonce, nil
}

// consumeNonce 验证并消费 nonce（一次性使用；过期或不存在返回 false）。
func (d *DiscourseConfig) consumeNonce(nonce string) bool {
	if nonce == "" {
		return false
	}
	d.nonceMu.Lock()
	defer d.nonceMu.Unlock()
	created, ok := d.nonces[nonce]
	if !ok {
		return false
	}
	delete(d.nonces, nonce)
	return time.Since(created) < nonceExpiry
}

func (d *DiscourseConfig) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(d.Secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
