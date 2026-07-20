GO ?= go
GOCACHE ?= $(CURDIR)/.cache/go-build
GOMODCACHE ?= $(CURDIR)/.cache/go-mod
VERSION := 0.8.0
BIN_DIR := bin
BINARY := $(BIN_DIR)/lts-agent
LINUX_BINARY := $(BIN_DIR)/lts-agent-linux-amd64
DEB_ARCH := amd64
PACKAGE_BUILD_DIR := build/debian
PACKAGE_ROOT := $(PACKAGE_BUILD_DIR)/root
DEB_PACKAGE := $(BIN_DIR)/lts-agent_$(VERSION)_$(DEB_ARCH).deb

.PHONY: build build-linux-amd64 test test-race fmt vet package-stage package-deb package-verify release-linux-amd64 clean

build:
	@mkdir -p $(BIN_DIR)
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) build -o $(BINARY) ./cmd/lts-agent

build-linux-amd64:
	@mkdir -p $(BIN_DIR)
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '-s -w' -o $(LINUX_BINARY) ./cmd/lts-agent

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test ./...

test-race:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test -race ./...

fmt:
	gofmt -w $$(find cmd compat internal packaging -type f -name '*.go')

vet:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) vet ./...

package-stage: build-linux-amd64
	rm -rf $(PACKAGE_ROOT)
	sh packaging/stage-package.sh $(PACKAGE_ROOT) $(VERSION) $(DEB_ARCH) $(LINUX_BINARY)

package-deb: package-stage
	@command -v dpkg-deb >/dev/null 2>&1 || { echo 'dpkg-deb is required; run this target on Ubuntu or Debian' >&2; exit 1; }
	dpkg-deb --build --root-owner-group $(PACKAGE_ROOT) $(DEB_PACKAGE)

package-verify: package-deb
	dpkg-deb --info $(DEB_PACKAGE)
	dpkg-deb --contents $(DEB_PACKAGE)

release-linux-amd64: fmt vet test test-race build-linux-amd64 package-verify

clean:
	rm -rf $(BIN_DIR) .cache build
