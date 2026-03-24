VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
TARGET_OS ?= $(shell go env GOOS)
TARGET_ARCH ?= $(shell go env GOARCH)
DIST_DIR ?= dist
LDFLAGS = -s -w -X pulse/internal/buildinfo.Version=$(VERSION) -X pulse/internal/buildinfo.Commit=$(COMMIT) -X pulse/internal/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: build build-server build-node build-cli wasm test package-server package-node checksums clean clean-dev dev-certs run-server run-node

build: wasm build-cli build-server build-node

build-cli:
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/pulse ./cmd/pulse

build-server:
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/pulse-server ./cmd/pulse-server

build-node:
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/pulse-node ./cmd/pulse-node

wasm:
	GOOS=js GOARCH=wasm go build -ldflags "$(LDFLAGS)" -o web/mvp/app.wasm ./web/mvp

test:
	go test ./...

package-server: wasm build-server
	rm -rf $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)
	mkdir -p $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/bin
	mkdir -p $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/etc/pulse
	mkdir -p $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/lib/systemd/system
	mkdir -p $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/share/pulse/web
	cp $(DIST_DIR)/pulse-server $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/bin/pulse-server
	cp deploy/env/pulse-server.env.example $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/etc/pulse/pulse-server.env.example
	cp deploy/systemd/pulse-server.service $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/lib/systemd/system/pulse-server.service
	cp -R web/mvp $(DIST_DIR)/package/pulse-server-$(TARGET_OS)-$(TARGET_ARCH)/share/pulse/web/
	mkdir -p $(DIST_DIR)/release
	tar -C $(DIST_DIR)/package -czf $(DIST_DIR)/release/pulse-server-$(TARGET_OS)-$(TARGET_ARCH).tar.gz pulse-server-$(TARGET_OS)-$(TARGET_ARCH)

package-node: build-node
	rm -rf $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)
	mkdir -p $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)/bin
	mkdir -p $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)/etc/pulse
	mkdir -p $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)/lib/systemd/system
	cp $(DIST_DIR)/pulse-node $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)/bin/pulse-node
	cp deploy/env/pulse-node.env.example $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)/etc/pulse/pulse-node.env.example
	cp deploy/systemd/pulse-node.service $(DIST_DIR)/package/pulse-node-$(TARGET_OS)-$(TARGET_ARCH)/lib/systemd/system/pulse-node.service
	mkdir -p $(DIST_DIR)/release
	tar -C $(DIST_DIR)/package -czf $(DIST_DIR)/release/pulse-node-$(TARGET_OS)-$(TARGET_ARCH).tar.gz pulse-node-$(TARGET_OS)-$(TARGET_ARCH)

checksums:
	cd $(DIST_DIR)/release && shasum -a 256 *.tar.gz > checksums.txt

clean:
	rm -rf $(DIST_DIR)

clean-dev:
	rm -rf dev-data dev-certs

# 获取开发用证书（需要先启动 server）
dev-certs:
	@echo "Fetching development certificates from server..."
	@mkdir -p dev-certs
	@curl -s http://localhost:8080/v1/node/settings | grep -o '"certificate":"[^"]*' | cut -d'"' -f4 > dev-certs/server_client_cert.pem
	@if [ ! -s dev-certs/server_client_cert.pem ]; then \
		echo "Failed to fetch certificate. Make sure server is running on http://localhost:8080"; \
		rm -f dev-certs/server_client_cert.pem; \
		exit 1; \
	fi
	@echo "Certificates saved to dev-certs/server_client_cert.pem"

# 开发模式运行 server
run-server: build-server
	@echo "Starting development server..."
	@mkdir -p dev-data dev-certs
	@PULSE_SERVER_ADDR=:8080 \
	 PULSE_ADMIN_USERNAME=admin \
	 PULSE_ADMIN_PASSWORD=admin123 \
	 PULSE_DB_PATH=./dev-data/pulse.db \
	 PULSE_WEB_DIR=./web/mvp \
	 PULSE_SERVER_NODE_CLIENT_CERT_FILE=./dev-certs/server_client_cert.pem \
	 PULSE_SERVER_NODE_CLIENT_KEY_FILE=./dev-certs/server_client_key.pem \
	 ./dist/pulse-server

# 开发模式运行 node（需要先运行 make dev-certs）
run-node: build-node
	@echo "Starting development node..."
	@if [ ! -f dev-certs/server_client_cert.pem ]; then \
		echo "Error: dev-certs/server_client_cert.pem not found"; \
		echo "Please run 'make dev-certs' first (server must be running)"; \
		exit 1; \
	fi
	@mkdir -p dev-data
	@PULSE_NODE_ADDR=:8081 \
	 PULSE_NODE_SERVER_URL=http://localhost:8080 \
	 PULSE_NODE_NAME=dev-node \
	 PULSE_NODE_TLS_CERT_FILE=./dev-certs/node_cert.pem \
	 PULSE_NODE_TLS_KEY_FILE=./dev-certs/node_key.pem \
	 PULSE_NODE_TLS_CLIENT_CERT_FILE=./dev-certs/server_client_cert.pem \
	 ./dist/pulse-node
