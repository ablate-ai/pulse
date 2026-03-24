package config

import "os"

type Config struct {
	ServerAddr               string
	NodeAddr                 string
	DBPath                   string
	WebDir                   string
	AdminUsername            string
	AdminPassword            string
	ServerNodeClientCertFile string
	ServerNodeClientKeyFile  string
	NodeTLSCertFile          string
	NodeTLSKeyFile           string
	NodeTLSClientCertFile    string
}

func Load() Config {
	return Config{
		ServerAddr:               envOrDefault("PULSE_SERVER_ADDR", ":8080"),
		NodeAddr:                 envOrDefault("PULSE_NODE_ADDR", ":8081"),
		DBPath:                   envOrDefault("PULSE_DB_PATH", "./pulse.db"),
		WebDir:                   envOrDefault("PULSE_WEB_DIR", ""),
		AdminUsername:            envOrDefault("PULSE_ADMIN_USERNAME", "admin"),
		AdminPassword:            envOrDefault("PULSE_ADMIN_PASSWORD", "admin"),
		ServerNodeClientCertFile: envOrDefault("PULSE_SERVER_NODE_CLIENT_CERT_FILE", "./server_client_cert.pem"),
		ServerNodeClientKeyFile:  envOrDefault("PULSE_SERVER_NODE_CLIENT_KEY_FILE", "./server_client_key.pem"),
		NodeTLSCertFile:          envOrDefault("PULSE_NODE_TLS_CERT_FILE", "./node_cert.pem"),
		NodeTLSKeyFile:           envOrDefault("PULSE_NODE_TLS_KEY_FILE", "./node_key.pem"),
		NodeTLSClientCertFile:    envOrDefault("PULSE_NODE_TLS_CLIENT_CERT_FILE", ""),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
