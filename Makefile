.PHONY: all build test test-fast open_coverage clean publish

# ---------------------------------------------------------------------------
# Versioning
#
# If HEAD is the exact release tag (vX.Y.Z matching VERSION), use VERSION as-is.
# Otherwise append -dev+<commit>[.dirty] so non-release builds are obvious.
# ---------------------------------------------------------------------------
VERSION_FILE := $(shell cat VERSION 2>/dev/null || echo dev)
GIT_TAG      := $(shell git describe --exact-match --tags HEAD 2>/dev/null)
GIT_COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_DIRTY    := $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo .dirty)
ifeq ($(GIT_TAG),v$(VERSION_FILE))
VERSION := $(VERSION_FILE)
else
VERSION := $(VERSION_FILE)-dev+$(GIT_COMMIT)$(GIT_DIRTY)
endif

LDFLAGS := -s -w -X main.version=$(VERSION)

all: build

build: test
	go build ./...
	go build -ldflags '$(LDFLAGS)' -o bin/firpty ./cmd/firpty

# Run tests, gate on 100% coverage of non-ignored lines.
test:
	@tmpfile=$$(mktemp); \
	trap 'rm -f $$tmpfile' EXIT; \
	go test -race -shuffle=on -coverprofile=coverage.tmp.out ./... > $$tmpfile 2>&1; \
	rc=$$?; \
	cat $$tmpfile; \
	if [ $$rc -ne 0 ]; then exit $$rc; fi; \
	grep -v -E -f .covignore coverage.tmp.out > coverage.out; \
	if go tool cover -func=coverage.out | tail -1 | grep -q -v '100.0%'; then \
		echo "ERROR: coverage < 100% (excluded lines per .covignore)"; \
		go tool cover -func=coverage.out | grep -v '100.0%' || true; \
		exit 1; \
	fi; \
	echo "✓ Coverage 100% (excluding .covignore)"

test-fast:
	go test ./...

open_coverage: test
	go tool cover -html=coverage.out

clean:
	rm -f coverage.tmp.out coverage.out
	rm -rf bin

# ---------------------------------------------------------------------------
# Release publishing
#
# Bump VERSION + update CHANGELOG, commit, then `make publish` to tag and push.
# GoReleaser CI builds and uploads release assets on tag push.
# ---------------------------------------------------------------------------
RELEASE_TAG := v$(VERSION_FILE)

publish: build
	@git fetch --tags --quiet origin
	@if git rev-parse $(RELEASE_TAG) >/dev/null 2>&1; then \
		echo "Tag $(RELEASE_TAG) already exists locally"; exit 1; \
	fi
	@if git ls-remote --exit-code --tags origin refs/tags/$(RELEASE_TAG) >/dev/null 2>&1; then \
		echo "Tag $(RELEASE_TAG) already exists on origin"; exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Working tree dirty; commit first"; exit 1; \
	fi
	git tag -a $(RELEASE_TAG) -m "release: $(RELEASE_TAG)"
	git push origin main $(RELEASE_TAG)
	@echo "Pushed $(RELEASE_TAG). GoReleaser CI will build and upload assets."
