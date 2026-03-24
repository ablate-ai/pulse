VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
TARGET_OS ?= $(shell go env GOOS)
TARGET_ARCH ?= $(shell go env GOARCH)
DIST_DIR ?= dist
LDFLAGS = -s -w -X pulse/internal/buildinfo.Version=$(VERSION) -X pulse/internal/buildinfo.Commit=$(COMMIT) -X pulse/internal/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: build build-server build-node build-cli wasm test package-server package-node checksums clean

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
