.PHONY: build test test-fast cover open_coverage clean

build:
	go build ./...
	go build -o bin/firpty ./cmd/firpty

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

cover:
	go test -coverprofile=coverage.tmp.out ./...
	@grep -v -E -f .covignore coverage.tmp.out > coverage.out
	@go tool cover -func=coverage.out | tail

open_coverage: cover
	go tool cover -html=coverage.out

clean:
	rm -f coverage.tmp.out coverage.out
	rm -rf bin
