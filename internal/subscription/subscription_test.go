package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/users"
)

func TestVMessLink(t *testing.T) {
	ib := users.UserInbound{
		Protocol: "vmess",
		UUID:     "11111111-1111-1111-1111-111111111111",
		Domain:   "example.com",
		Port:     443,
	}
	u := users.User{Username: "alice"}
	link := Link(ib, u)
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
	if obj["id"] != ib.UUID {
		t.Errorf("uuid mismatch: got %v", obj["id"])
	}
	if obj["add"] != ib.Domain {
		t.Errorf("domain mismatch: got %v", obj["add"])
	}
	if obj["ps"] != u.Username {
		t.Errorf("remark mismatch: got %v", obj["ps"])
	}
}

func TestVlessLink(t *testing.T) {
	ib := users.UserInbound{
		Protocol: "vless",
		UUID:     "22222222-2222-2222-2222-222222222222",
		Domain:   "example.com",
		Port:     8443,
	}
	u := users.User{Username: "bob"}
	link := Link(ib, u)
	if !strings.HasPrefix(link, "vless://") {
		t.Fatalf("expected vless:// prefix, got %s", link)
	}
	if !strings.Contains(link, ib.UUID) {
		t.Errorf("uuid not found in link: %s", link)
	}
}

func TestTrojanLink(t *testing.T) {
	ib := users.UserInbound{
		Protocol: "trojan",
		Secret:   "trojan-pass",
		Domain:   "example.com",
		Port:     443,
	}
	u := users.User{Username: "carol"}
	link := Link(ib, u)
	if !strings.HasPrefix(link, "trojan://") {
		t.Fatalf("expected trojan:// prefix, got %s", link)
	}
	if !strings.Contains(link, ib.Secret) {
		t.Errorf("secret not found in link: %s", link)
	}
}

func TestVlessRealityLink(t *testing.T) {
	ib := users.UserInbound{
		Protocol:         "vless",
		UUID:             "33333333-3333-3333-3333-333333333333",
		Domain:           "1.2.3.4",
		Port:             443,
		Security:         "reality",
		Flow:             "xtls-rprx-vision",
		SNI:              "www.google.com",
		Fingerprint:      "chrome",
		RealityPublicKey: "abc123publickey",
		RealityShortID:   "deadbeef",
	}
	u := users.User{Username: "eve"}
	link := Link(ib, u)
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
	ib := users.UserInbound{
		Protocol: "shadowsocks",
		Secret:   "ss-pass",
		Method:   "aes-128-gcm",
		Domain:   "example.com",
		Port:     8388,
	}
	u := users.User{Username: "dave"}
	link := Link(ib, u)
	if !strings.HasPrefix(link, "ss://") {
		t.Fatalf("expected ss:// prefix, got %s", link)
	}
}
