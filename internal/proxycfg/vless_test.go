package proxycfg

import (
	"strings"
	"testing"

	"pulse/internal/users"
)

func TestBuildSingboxConfigOmitsUnsupportedFieldsForVLESS(t *testing.T) {
	config, err := BuildSingboxConfig([]users.User{{
		ID:       "u1",
		Username: "alice",
		UUID:     "11111111-1111-1111-1111-111111111111",
		Protocol: "vless",
		Status:   users.StatusActive,
		NodeID:   "node-1",
		Domain:   "example.com",
		Port:     39001,
	}}, BuildOptions{})
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
	config, err := BuildSingboxConfig([]users.User{{
		ID:                   "u1",
		Username:             "alice",
		UUID:                 "11111111-1111-1111-1111-111111111111",
		Protocol:             "vless",
		Security:             "reality",
		RealityPrivateKey:    "myprivatekey",
		RealityShortID:       "deadbeef",
		RealityHandshakeAddr: "www.google.com:443",
		Status:               users.StatusActive,
		NodeID:               "node-1",
		Domain:               "1.2.3.4",
		Port:                 443,
	}}, BuildOptions{})
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
	config, err := BuildSingboxConfig([]users.User{{
		ID:       "u1",
		Username: "alice",
		Protocol: "shadowsocks",
		Secret:   "secret",
		Method:   "aes-256-gcm",
		Status:   users.StatusActive,
		NodeID:   "node-1",
		Domain:   "example.com",
		Port:     39002,
	}}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildSingboxConfig() error = %v", err)
	}

	if !strings.Contains(config, `"method": "aes-256-gcm"`) {
		t.Fatalf("expected shadowsocks method in config: %s", config)
	}
}
