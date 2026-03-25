package certmgr

import (
	"context"
	"crypto/tls"
	"path/filepath"
	"strings"

	"github.com/caddyserver/certmagic"
)

// Manager 封装 certmagic，负责 Trojan TLS 证书的申请与续期。
// HTTP-01 challenge 由 certmagic 自动在 :80 端口处理。
type Manager struct {
	cfg     *certmagic.Config
	certDir string
}

// New 创建 Manager。certDir 为证书存储目录；email 可为空，不影响证书申请。
func New(certDir, email string) *Manager {
	certmagic.Default.Storage = &certmagic.FileStorage{Path: certDir}
	certmagic.DefaultACME.Agreed = true
	if email != "" {
		certmagic.DefaultACME.Email = email
	}

	cfg := certmagic.NewDefault()
	return &Manager{cfg: cfg, certDir: certDir}
}

// Ensure 确保 domain 的证书存在且有效；不存在则同步申请。
// 申请时 certmagic 会自动在 :80 端口完成 HTTP-01 challenge。
func (m *Manager) Ensure(ctx context.Context, domain string) error {
	return m.cfg.ManageSync(ctx, []string{domain})
}

// CertPath 返回 certmagic FileStorage 存储该域名证书的绝对路径。
func (m *Manager) CertPath(domain string) string {
	return m.storagePath(certmagic.StorageKeys.SiteCert(issuerKey(), domain))
}

// KeyPath 返回 certmagic FileStorage 存储该域名私钥的绝对路径。
func (m *Manager) KeyPath(domain string) string {
	return m.storagePath(certmagic.StorageKeys.SitePrivateKey(issuerKey(), domain))
}

// TLSConfig 返回带 certmagic 自动证书管理的 tls.Config。
// 使用 TLS-ALPN-01 challenge，无需额外开放 :80 端口。
func (m *Manager) TLSConfig() *tls.Config {
	return m.cfg.TLSConfig()
}

// storagePath 将 certmagic 的存储键转为本地文件系统绝对路径。
func (m *Manager) storagePath(key string) string {
	// certmagic 的存储键使用 "/" 分隔，FileStorage.Filename 会转为 OS 路径
	return filepath.Join(m.certDir, filepath.FromSlash(key))
}

// issuerKey 返回 Let's Encrypt 生产环境的 issuer key。
func issuerKey() string {
	// certmagic 用 CA URL 的 host+path（去掉协议头，"/" → "-"）作为 key
	ca := certmagic.LetsEncryptProductionCA
	key := strings.TrimPrefix(ca, "https://")
	key = strings.TrimPrefix(key, "http://")
	key = strings.ReplaceAll(key, "/", "-")
	return key
}
