.PHONY: setup bundle bundle-linux build build-linux build-bundled test install-dist clean release release-amd64 release-arm64

BINARY ?= samo-server
DIST_DIR ?= dist
GOOS ?= linux
GOARCH ?= amd64
GOFLAGS ?=

# Samo Server is built for Ubuntu Linux (amd64 by default, arm64 optional).
LINUX_PLATFORM = linux-$(GOARCH)

setup: bundle-linux

bundle:
	./scripts/bundle-ffmpeg.sh --platform $(LINUX_PLATFORM)

bundle-linux:
	./scripts/bundle-ffmpeg.sh --platform $(LINUX_PLATFORM)

bundle-linux-all:
	./scripts/bundle-ffmpeg.sh --all

build: bundle-linux
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS) -ldflags "-s -w" -o $(DIST_DIR)/$(BINARY) ./cmd/samo-server
	@mkdir -p $(DIST_DIR)/bin
	@cp internal/toolchain/assets/$(LINUX_PLATFORM)/ffmpeg internal/toolchain/assets/$(LINUX_PLATFORM)/ffprobe $(DIST_DIR)/bin/
	@chmod 0755 $(DIST_DIR)/$(BINARY) $(DIST_DIR)/bin/ffmpeg $(DIST_DIR)/bin/ffprobe
	@echo "Built Ubuntu bundle: $(DIST_DIR)/$(BINARY) + bin/ffmpeg + bin/ffprobe"

build-linux:
	@$(MAKE) GOOS=linux GOARCH=amd64 build

build-linux-arm64:
	@$(MAKE) GOOS=linux GOARCH=arm64 build

build-bundled: bundle-linux-all
	GOOS=linux GOARCH=amd64 go build -tags bundled $(GOFLAGS) -o $(DIST_DIR)/$(BINARY) ./cmd/samo-server
	@echo "Built linux/amd64 bundled binary (extracts tools into SAMO_DATA_DIR when bin/ is absent)"

test:
	go test ./...

install-dist: build-linux
	@echo "Ubuntu install layout:"
	@echo "  $(DIST_DIR)/$(BINARY)"
	@echo "  $(DIST_DIR)/bin/ffmpeg"
	@echo "  $(DIST_DIR)/bin/ffprobe"

clean:
	rm -rf $(DIST_DIR)

# Release builds: pure-Go static binary + native ffmpeg + install script,
# packaged into a single tarball per architecture. The install script next
# to the binary handles user creation, systemd setup, and start.
release-amd64:
	@$(MAKE) GOARCH=amd64 build
	@mv $(DIST_DIR)/$(BINARY) $(DIST_DIR)/$(BINARY)-linux-amd64
	@cp scripts/install.sh scripts/uninstall.sh scripts/samo-server.service $(DIST_DIR)/
	@chmod +x $(DIST_DIR)/install.sh $(DIST_DIR)/uninstall.sh
	@tar -C $(DIST_DIR) -czf $(DIST_DIR)/$(BINARY)-linux-amd64.tar.gz \
		$(BINARY)-linux-amd64 bin install.sh uninstall.sh samo-server.service
	@echo "Built $(DIST_DIR)/$(BINARY)-linux-amd64.tar.gz"

release-arm64:
	@$(MAKE) GOARCH=arm64 build
	@mv $(DIST_DIR)/$(BINARY) $(DIST_DIR)/$(BINARY)-linux-arm64
	@cp scripts/install.sh scripts/uninstall.sh scripts/samo-server.service $(DIST_DIR)/
	@chmod +x $(DIST_DIR)/install.sh $(DIST_DIR)/uninstall.sh
	@tar -C $(DIST_DIR) -czf $(DIST_DIR)/$(BINARY)-linux-arm64.tar.gz \
		$(BINARY)-linux-arm64 bin install.sh uninstall.sh samo-server.service
	@echo "Built $(DIST_DIR)/$(BINARY)-linux-arm64.tar.gz"

release: release-amd64
	@echo
	@echo "Release tarball ready: $(DIST_DIR)/$(BINARY)-linux-amd64.tar.gz"
	@echo "Install on Ubuntu:"
	@echo "  tar xzf $(BINARY)-linux-amd64.tar.gz"
	@echo "  cd <extracted>"
	@echo "  sudo ./install.sh"
