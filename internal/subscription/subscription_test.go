package subscription

import (
	"strings"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)


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
