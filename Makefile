# Single deterministic verification entrypoint (D-015).
# Every check the repository defines is reachable from `make verify`.

GO      ?= go
BINARY  := bin/agent-dispatch
PKG     := github.com/irootkernel/agent-dispatch
VERSION ?= 0.1.0-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILDTIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION) \
           -X $(PKG)/internal/version.Commit=$(COMMIT) \
           -X $(PKG)/internal/version.BuildTime=$(BUILDTIME)

.PHONY: all build test test-race vet fmt-check staticcheck check-imports \
        manifest-check schema-validation traceability verify clean

all: build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/agent-dispatch

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@command -v gofmt >/dev/null 2>&1 || { echo "gofmt not found in PATH"; exit 1; }; \
	 unformatted=$$(gofmt -l .) || exit 1; \
	 if [ -n "$$unformatted" ]; then \
	   echo "gofmt required for:"; echo "$$unformatted"; exit 1; \
	 fi

# staticcheck is pinned as a go.mod tool dependency (SCP-005), so its
# integrity is covered by go.sum instead of a network fetch at verify time.
staticcheck:
	$(GO) tool staticcheck ./...

check-imports:
	$(GO) run ./internal/importlint

# Checksum verification of the SOT docs package; portable across macOS
# (shasum) and Linux (sha256sum).
manifest-check:
	cd docs && if command -v shasum >/dev/null 2>&1; then \
	  shasum -a 256 -c MANIFEST.sha256; \
	else \
	  sha256sum -c MANIFEST.sha256; \
	fi

# Draft 2020-12 schema and example validation (D-015; Python validator retired).
schema-validation:
	$(GO) run ./internal/tools/schemavalid -root docs

# Regenerate the traceability matrix and fail if it drifted from the
# roadmap and required-spec.
traceability:
	python3 docs/scripts/generate-traceability.py
	@if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then \
	  echo "traceability drift check requires a git repository"; exit 1; \
	fi
	@git diff --quiet -- docs/docs/00-sot/traceability-matrix.md || \
	  (echo "traceability-matrix.md is stale; commit the regenerated file"; exit 1)

verify: build fmt-check vet staticcheck check-imports test test-race manifest-check schema-validation traceability schedule-check
	@echo "verify: all checks passed"

# E6-T3 release process (SCP-008): reproducible cross-platform
# binaries plus a portable checksum list under dist/. The Go toolchain
# with -trimpath and the commit-pinned version, commit, and build time
# produces byte-identical binaries for one commit, so the checksums are
# generated over the binaries directly (archives would embed
# machine-specific metadata).
DIST_DIR := dist
RELEASE_OS_ARCH := darwin/arm64 linux/amd64
COMMIT_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo 1970-01-01T00:00:00Z)
RELEASE_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

release:
	@if [ "$(RELEASE_COMMIT)" = "unknown" ]; then echo "release: git metadata absent — the commit stamp will be 'unknown' and the build time the epoch" >&2; fi
	@rm -rf $(DIST_DIR) && mkdir -p $(DIST_DIR)
	@for os_arch in $(RELEASE_OS_ARCH); do \
	os=$${os_arch%/*}; arch=$${os_arch#*/}; \
	tag=$$(printf '%s' "$$os_arch" | tr / -); \
	CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath \
	  -ldflags "-X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(RELEASE_COMMIT) -X $(PKG)/internal/version.BuildTime=$(COMMIT_DATE)" \
	  -o $(DIST_DIR)/agent-dispatch-$(VERSION)-$$tag ./cmd/agent-dispatch || exit 1; \
	done
	@cd $(DIST_DIR) && if command -v shasum >/dev/null 2>&1; then \
	  shasum -a 256 agent-dispatch-$(VERSION)-* | LC_ALL=C sort > SHA256SUMS; \
	else \
	  sha256sum agent-dispatch-$(VERSION)-* | LC_ALL=C sort > SHA256SUMS; \
	fi
	@cat $(DIST_DIR)/SHA256SUMS
	@echo "release: artifacts in $(DIST_DIR) for $(VERSION)"

# E6-T3 scheduling-artifact validation: the launchd and systemd
# examples are verified with the platform tool where it exists
# (SCP-008 "where possible"); a missing tool is reported, not fatal,
# because the other platform validates its own artifact where it is deployed.
schedule-check:
	@miss=0; \
	if command -v plutil >/dev/null 2>&1; then \
	  plutil -lint docs/examples/scripts/agent-dispatch-reconcile.launchd.plist.example || exit 1; \
	else miss=1; fi; \
	if command -v systemd-analyze >/dev/null 2>&1; then \
	  systemd-analyze verify docs/examples/scripts/agent-dispatch-reconcile.service.example docs/examples/scripts/agent-dispatch-reconcile.timer.example || exit 1; \
	else miss=1; fi; \
	sh -n docs/examples/scripts/agent-dispatch-uninstall.sh.example || exit 1; \
	if [ $$miss -eq 1 ]; then echo "schedule-check: platform validator absent; validated the shell script only (run on the other platform to lint its artifact)"; fi; \
	echo "schedule-check: done"

clean:
	rm -rf bin dist
