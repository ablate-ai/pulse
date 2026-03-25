package proxycfg

import (
	"strings"
	"testing"

	"pulse/internal/users"
)

func TestBuildSingboxConfigOmitsUnsupportedFieldsForVLESS(t *testing.T) {
	ib := users.UserInbound{
		ID:       "ib1",
		UserID:   "u1",
		NodeID:   "node-1",
		Protocol: "vless",
		UUID:     "11111111-1111-1111-1111-111111111111",
		Domain:   "example.com",
		Port:     39001,
	}
	userMap := map[string]users.User{
		"u1": {ID: "u1", Username: "alice", Status: users.StatusActive},
	}
	config, err := BuildSingboxConfig([]users.UserInbound{ib}, userMap, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildSingboxConfig() error = %v", err)
	}

	if strings.Contains(config, `"transport"`) {
		t.Fatalf("unexpected transport in config: %s", config)
	}
	if strings.Contains(config, `"method"`) {
		t.Fatalf("unexpected method in config: %s", config)
	}
	if strings.Contains(config, `"password"`) {
		t.Fatalf("unexpected password in config: %s", config)
	}
}

func TestBuildSingboxConfigRealityTLS(t *testing.T) {
	ib := users.UserInbound{
		ID:                   "ib1",
		UserID:               "u1",
		NodeID:               "node-1",
		Protocol:             "vless",
		UUID:                 "11111111-1111-1111-1111-111111111111",
		Security:             "reality",
		RealityPrivateKey:    "myprivatekey",
		RealityShortID:       "deadbeef",
		RealityHandshakeAddr: "www.google.com:443",
		Domain:               "1.2.3.4",
		Port:                 443,
	}
	userMap := map[string]users.User{
		"u1": {ID: "u1", Username: "alice", Status: users.StatusActive},
	}
	config, err := BuildSingboxConfig([]users.UserInbound{ib}, userMap, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildSingboxConfig() error = %v", err)
	}
	for _, want := range []string{`"tls"`, `"reality"`, `"myprivatekey"`, `"deadbeef"`, `"www.google.com"`} {
		if !strings.Contains(config, want) {
			t.Errorf("expected %q in config: %s", want, config)
		}
	}
}

func TestBuildSingboxConfigKeepsShadowsocksMethod(t *testing.T) {
	ib := users.UserInbound{
		ID:       "ib1",
		UserID:   "u1",
		NodeID:   "node-1",
		Protocol: "shadowsocks",
		Secret:   "secret",
		Method:   "aes-256-gcm",
		Domain:   "example.com",
		Port:     39002,
	}
	userMap := map[string]users.User{
		"u1": {ID: "u1", Username: "alice", Status: users.StatusActive},
	}
	config, err := BuildSingboxConfig([]users.UserInbound{ib}, userMap, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildSingboxConfig() error = %v", err)
	}

	if !strings.Contains(config, `"method": "aes-256-gcm"`) {
		t.Fatalf("expected shadowsocks method in config: %s", config)
	}
}
