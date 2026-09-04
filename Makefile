.PHONY: build build-linux build-linux-arm64 test test-db clean ui ui-check

BINARY ?= samo-server
DIST_DIR ?= dist
GOOS ?= linux
GOARCH ?= amd64
GOFLAGS ?=

# Samo Server is built for Ubuntu Linux (amd64 by default, arm64 optional).
LINUX_PLATFORM = linux-$(GOARCH)

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

# A plain binary, for running the server outside a container. It needs ffmpeg
# and ffprobe on PATH (or SAMO_FFMPEG_PATH / SAMO_FFPROBE_PATH); the published
# image installs them itself.
build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(GOFLAGS) -ldflags "-s -w" -o $(DIST_DIR)/$(BINARY) ./cmd/samo-server

build-linux:
	@$(MAKE) GOOS=linux GOARCH=amd64 build

build-linux-arm64:
	@$(MAKE) GOOS=linux GOARCH=arm64 build

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

clean:
	rm -rf $(DIST_DIR)
