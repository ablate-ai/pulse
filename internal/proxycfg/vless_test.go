package proxycfg

import (
	"strings"
	"testing"

	"pulse/internal/inbounds"
	"pulse/internal/users"
)

func vlessInbound(nodeID string, port int) inbounds.Inbound {
	return inbounds.Inbound{
		ID:       "ib1",
		NodeID:   nodeID,
		Protocol: "vless",
		Tag:      "pulse-vless-" + nodeID,
		Port:     port,
	}
}

func userAccess(userID string) users.UserInbound {
	return users.UserInbound{
		ID:     "acc1",
		UserID: userID,
		NodeID: "node-1",
		UUID:   "11111111-1111-1111-1111-111111111111",
		Secret: "test-secret",
	}
}

func TestBuildSingboxConfigOmitsUnsupportedFieldsForVLESS(t *testing.T) {
	ib := vlessInbound("node-1", 39001)
	acc := userAccess("u1")
	userMap := map[string]users.User{
		"u1": {ID: "u1", Username: "alice", Status: users.StatusActive},
	}
	config, err := BuildSingboxConfig([]inbounds.Inbound{ib}, []users.UserInbound{acc}, userMap, BuildOptions{})
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
	ib := inbounds.Inbound{
		ID:                   "ib1",
		NodeID:               "node-1",
		Protocol:             "vless",
		Tag:                  "pulse-vless-443",
		Port:                 443,
		Security:             "reality",
		RealityPrivateKey:    "myprivatekey",
		RealityShortID:       "deadbeef",
		RealityHandshakeAddr: "www.google.com:443",
	}
	acc := userAccess("u1")
	userMap := map[string]users.User{
		"u1": {ID: "u1", Username: "alice", Status: users.StatusActive},
	}
	config, err := BuildSingboxConfig([]inbounds.Inbound{ib}, []users.UserInbound{acc}, userMap, BuildOptions{})
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
	ib := inbounds.Inbound{
		ID:       "ib1",
		NodeID:   "node-1",
		Protocol: "shadowsocks",
		Tag:      "pulse-shadowsocks-39002",
		Port:     39002,
		Method:   "aes-256-gcm",
	}
	acc := users.UserInbound{
		ID:     "acc1",
		UserID: "u1",
		NodeID: "node-1",
		Secret: "secret",
	}
	userMap := map[string]users.User{
		"u1": {ID: "u1", Username: "alice", Status: users.StatusActive},
	}
	config, err := BuildSingboxConfig([]inbounds.Inbound{ib}, []users.UserInbound{acc}, userMap, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildSingboxConfig() error = %v", err)
	}

	if !strings.Contains(config, `"method": "aes-256-gcm"`) {
		t.Fatalf("expected shadowsocks method in config: %s", config)
	}
}
