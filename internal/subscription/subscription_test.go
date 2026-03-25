package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)

func TestVMessLink(t *testing.T) {
	ib := inbounds.Inbound{Protocol: "vmess", Port: 443}
	host := inbounds.Host{Address: "example.com", Port: 443}
	acc := users.UserInbound{UUID: "11111111-1111-1111-1111-111111111111"}
	u := users.User{Username: "alice"}

	link := Link(ib, host, acc, u)
	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("expected vmess:// prefix, got %s", link)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("decode base64 failed: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		t.Fatalf("unmarshal vmess json failed: %v", err)
	}
	if obj["id"] != acc.UUID {
		t.Errorf("uuid mismatch: got %v", obj["id"])
	}
	if obj["add"] != host.Address {
		t.Errorf("address mismatch: got %v", obj["add"])
	}
	if obj["ps"] != u.Username {
		t.Errorf("remark mismatch: got %v", obj["ps"])
	}
}

func TestVlessLink(t *testing.T) {
	ib := inbounds.Inbound{Protocol: "vless", Port: 8443}
	host := inbounds.Host{Address: "example.com", Port: 8443}
	acc := users.UserInbound{UUID: "22222222-2222-2222-2222-222222222222"}
	u := users.User{Username: "bob"}

	link := Link(ib, host, acc, u)
	if !strings.HasPrefix(link, "vless://") {
		t.Fatalf("expected vless:// prefix, got %s", link)
	}
	if !strings.Contains(link, acc.UUID) {
		t.Errorf("uuid not found in link: %s", link)
	}
}

func TestTrojanLink(t *testing.T) {
	ib := inbounds.Inbound{Protocol: "trojan", Port: 443}
	host := inbounds.Host{Address: "example.com", Port: 443}
	acc := users.UserInbound{Secret: "trojan-pass"}
	u := users.User{Username: "carol"}

	link := Link(ib, host, acc, u)
	if !strings.HasPrefix(link, "trojan://") {
		t.Fatalf("expected trojan:// prefix, got %s", link)
	}
	if !strings.Contains(link, acc.Secret) {
		t.Errorf("secret not found in link: %s", link)
	}
}

func TestVlessRealityLink(t *testing.T) {
	ib := inbounds.Inbound{
		Protocol:          "vless",
		Port:              443,
		Security:          "reality",
		RealityPublicKey:  "abc123publickey",
		RealityShortID:    "deadbeef",
	}
	host := inbounds.Host{
		Address:  "1.2.3.4",
		Port:     443,
		Security: "reality",
		SNI:      "www.google.com",
		Fingerprint: "chrome",
	}
	acc := users.UserInbound{UUID: "33333333-3333-3333-3333-333333333333"}
	u := users.User{Username: "eve"}

	link := Link(ib, host, acc, u)
	if !strings.HasPrefix(link, "vless://") {
		t.Fatalf("expected vless:// prefix, got %s", link)
	}
	for _, want := range []string{"security=reality", "pbk=abc123publickey", "sid=deadbeef", "sni=www.google.com", "fp=chrome", "flow=xtls-rprx-vision"} {
		if !strings.Contains(link, want) {
			t.Errorf("expected %q in link: %s", want, link)
		}
	}
}

func TestShadowsocksLink(t *testing.T) {
	ib := inbounds.Inbound{
		Protocol: "shadowsocks",
		Port:     8388,
		Method:   "aes-128-gcm",
	}
	host := inbounds.Host{Address: "example.com", Port: 8388}
	acc := users.UserInbound{Secret: "ss-pass"}
	u := users.User{Username: "dave"}

	link := Link(ib, host, acc, u)
	if !strings.HasPrefix(link, "ss://") {
		t.Fatalf("expected ss:// prefix, got %s", link)
	}
}
