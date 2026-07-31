.PHONY: setup bundle bundle-linux bundle-chromaprint bundle-chromaprint-all build build-linux build-bundled test test-db install-dist clean release release-amd64 release-arm64 ui ui-check

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

# fpcalc is a separate, optional bundle target: only the explo folder
# feature needs it, so it isn't part of the default bundle-linux/build path.
# Run this once (or --all for both arches) before `make build`/`make release`
# to have fpcalc automatically swept into dist/ and the release tarball.
bundle-chromaprint:
	./scripts/bundle-chromaprint.sh --platform $(LINUX_PLATFORM)

bundle-chromaprint-all:
	./scripts/bundle-chromaprint.sh --all

# The web UI is built by Vite from web/src into internal/api/web/build, which
# go:embed compiles into the binary. That output is COMMITTED: go:embed is a
# compile-time dependency, so a missing build directory is not a stale UI, it
# is a build failure — and `go build ./...` has to keep working for anyone with
# only Go installed. Run this after changing anything under web/src.
# Lint runs before the bundle, and the rule that earns its keep is no-undef.
# The UI was one 4,400-line IIFE where every function saw every other by
# sharing a scope; split into modules, a call to something you forgot to import
# is a reference to an undefined global. Rollup bundles that without complaint
# and it throws the first time the code path runs — on a tab nobody clicked
# during testing. no-undef turns that into a build failure.
ui:
	cd web && npm ci && npm run lint && npm run build

# Fails if the committed bundle is not what web/src currently produces. The
# risk with a committed build artifact is that it silently drifts from its
# source; this is the check that catches it in CI.
ui-check: ui
	@if ! git diff --quiet -- internal/api/web/build; then \
		echo "internal/api/web/build is stale — run 'make ui' and commit the result"; \
		git --no-pager diff --stat -- internal/api/web/build; \
		exit 1; \
	fi
	@echo "web bundle is up to date"

build: bundle-linux
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS) -ldflags "-s -w" -o $(DIST_DIR)/$(BINARY) ./cmd/samo-server
	@mkdir -p $(DIST_DIR)/bin
	@cp internal/toolchain/assets/$(LINUX_PLATFORM)/ffmpeg internal/toolchain/assets/$(LINUX_PLATFORM)/ffprobe $(DIST_DIR)/bin/
	@chmod 0755 $(DIST_DIR)/$(BINARY) $(DIST_DIR)/bin/ffmpeg $(DIST_DIR)/bin/ffprobe
	@if [ -f internal/toolchain/assets/$(LINUX_PLATFORM)/fpcalc ]; then \
		cp internal/toolchain/assets/$(LINUX_PLATFORM)/fpcalc $(DIST_DIR)/bin/; \
		chmod 0755 $(DIST_DIR)/bin/fpcalc; \
		echo "Included bin/fpcalc (explo folder feature)"; \
	fi
	@echo "Built Ubuntu bundle: $(DIST_DIR)/$(BINARY) + bin/ffmpeg + bin/ffprobe"

build-linux:
	@$(MAKE) GOOS=linux GOARCH=amd64 build

build-linux-arm64:
	@$(MAKE) GOOS=linux GOARCH=arm64 build

build-bundled: bundle-linux-all
	GOOS=linux GOARCH=amd64 go build -tags bundled $(GOFLAGS) -o $(DIST_DIR)/$(BINARY) ./cmd/samo-server
	@echo "Built linux/amd64 bundled binary (extracts tools into SAMO_DATA_DIR when bin/ is absent)"

# Tests run against a real PostgreSQL: every test clones its own database from
# a migrated template. test-db starts (or reuses) a disposable local container
# on port 55432; it is skipped when SAMO_TEST_PG_DSN points somewhere else.
test-db:
	@if [ -n "$$SAMO_TEST_PG_DSN" ]; then \
		echo "using SAMO_TEST_PG_DSN"; \
	elif [ "$$(docker inspect -f '{{.State.Running}}' samo-test-pg 2>/dev/null)" = "true" ]; then \
		echo "samo-test-pg already running"; \
	else \
		docker rm -f samo-test-pg >/dev/null 2>&1 || true; \
		docker run -d --name samo-test-pg \
			-e POSTGRES_USER=samo -e POSTGRES_PASSWORD=samo -e POSTGRES_DB=samo \
			-p 55432:5432 postgres:16 >/dev/null; \
		until docker exec samo-test-pg pg_isready -U samo -d samo >/dev/null 2>&1; do sleep 1; done; \
		echo "started samo-test-pg on :55432"; \
	fi

test: test-db
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
