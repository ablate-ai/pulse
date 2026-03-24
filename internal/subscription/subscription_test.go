package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"pulse/internal/users"
)

func TestVMessLink(t *testing.T) {
	u := users.User{
		Username: "alice",
		UUID:     "11111111-1111-1111-1111-111111111111",
		Protocol: "vmess",
		Domain:   "example.com",
		Port:     443,
	}
	link := Link(u)
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
	if obj["id"] != u.UUID {
		t.Errorf("uuid mismatch: got %v", obj["id"])
	}
	if obj["add"] != u.Domain {
		t.Errorf("domain mismatch: got %v", obj["add"])
	}
	if obj["ps"] != u.Username {
		t.Errorf("remark mismatch: got %v", obj["ps"])
	}
}

func TestVlessLink(t *testing.T) {
	u := users.User{
		Username: "bob",
		UUID:     "22222222-2222-2222-2222-222222222222",
		Protocol: "vless",
		Domain:   "example.com",
		Port:     8443,
	}
	link := Link(u)
	if !strings.HasPrefix(link, "vless://") {
		t.Fatalf("expected vless:// prefix, got %s", link)
	}
	if !strings.Contains(link, u.UUID) {
		t.Errorf("uuid not found in link: %s", link)
	}
}

func TestTrojanLink(t *testing.T) {
	u := users.User{
		Username: "carol",
		Secret:   "trojan-pass",
		Protocol: "trojan",
		Domain:   "example.com",
		Port:     443,
	}
	link := Link(u)
	if !strings.HasPrefix(link, "trojan://") {
		t.Fatalf("expected trojan:// prefix, got %s", link)
	}
	if !strings.Contains(link, u.Secret) {
		t.Errorf("secret not found in link: %s", link)
	}
}

func TestVlessRealityLink(t *testing.T) {
	u := users.User{
		Username:         "eve",
		UUID:             "33333333-3333-3333-3333-333333333333",
		Protocol:         "vless",
		Domain:           "1.2.3.4",
		Port:             443,
		Security:         "reality",
		Flow:             "xtls-rprx-vision",
		SNI:              "www.google.com",
		Fingerprint:      "chrome",
		RealityPublicKey: "abc123publickey",
		RealityShortID:   "deadbeef",
	}
	link := Link(u)
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
	u := users.User{
		Username: "dave",
		Secret:   "ss-pass",
		Method:   "aes-128-gcm",
		Protocol: "shadowsocks",
		Domain:   "example.com",
		Port:     8388,
	}
	link := Link(u)
	if !strings.HasPrefix(link, "ss://") {
		t.Fatalf("expected ss:// prefix, got %s", link)
	}
}
