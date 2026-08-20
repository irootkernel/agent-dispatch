# Single deterministic verification entrypoint (D-015).
# Every check the repository defines is reachable from `make verify`.

GO      ?= go
BINARY  := bin/jjukkumi
PKG     := github.com/rootkernel/jjukkumi
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
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/jjukkumi

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
	@git diff --quiet -- docs/docs/00-sot/traceability-matrix.md || \
	  (echo "traceability-matrix.md is stale; commit the regenerated file"; exit 1)

verify: build fmt-check vet staticcheck check-imports test test-race manifest-check schema-validation traceability
	@echo "verify: all checks passed"

clean:
	rm -rf bin
