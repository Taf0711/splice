# Splice build/test/lint targets.
.DEFAULT_GOAL := build
# Recursive "=" so git runs only when the dev target expands it.
DEV_VERSION = dev-$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)$(shell git diff --quiet HEAD 2>/dev/null || echo -dirty)
.PHONY: build dev build-all test test-race test-cli-tui test-memd e2e check vet fmt fmt-check lint tidy clean help install-memd

# Remove the old binary before each build. A failed build then leaves no stale binary that a person can run by accident.
build:
	rm -f splice
	go build -o splice ./cmd/splice

# Build ./splice-dev from the working tree. Its version names the commit, plus -dirty for uncommitted changes.
dev:
	rm -f splice-dev
	go build -ldflags "-X github.com/Taf0711/splice/internal/version.Version=$(DEV_VERSION)" -o splice-dev ./cmd/splice
	@./splice-dev version

# Build every command in cmd/.
build-all:
	go build ./...

# Run the full test suite with the race detector (matches CI expectations).
test:
	go test ./... -race -count=1 -timeout 30m

# Faster, no race detector.
test-quick:
	go test -timeout 30m ./...

# Test the CLI and TUI seams with the race detector.
test-cli-tui:
	go test -race -count=1 -timeout 30m ./internal/cli ./internal/tui

# Run the binary e2e suite (tests/e2e) against a scripted fake provider.
e2e:
	SPLICE_E2E=1 go test -count=1 -parallel 4 -timeout 15m ./tests/e2e/...

# Test the memd sidecar (separate Go module; root go test does not see it).
test-memd:
	cd memd && go test ./...

# Full pre-push gate: fmt + vet + build + test in one command. Skips two
# known-local-failures that CI runs clean: TestRealMemdSidecarMemoryRetrieval
# (zombie-memd-socket hang on dev machines) and the WSL2 sandbox backend
# selection test (false-positive under WSL2; passes on real Linux). Use
# `make test` for the unskipped race suite that matches CI.
check: fmt-check vet build-all
	go test -count=1 -timeout 30m -skip 'TestRealMemdSidecarMemoryRetrieval|TestSelectBackendChoosesPlatformAdapterWithFallback' ./...

vet:
	go vet ./...

fmt:
	gofmt -w $(shell git ls-files '*.go')

# Fail if any tracked Go file is not gofmt-clean.
fmt-check:
	@out="$$(gofmt -l $$(git ls-files '*.go'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# Lint = formatting check + vet (no extra tooling required).
lint: fmt-check vet

tidy:
	go mod tidy

# Install the splice-memd memory sidecar binary onto PATH.
install-memd:
	cd memd && go install

clean:
	rm -f splice splice-dev
	go clean ./...

help:
	@echo "Targets: build (default), dev, build-all, check, test, test-quick, test-cli-tui, test-memd, e2e, vet, fmt, fmt-check, lint, tidy, install-memd, clean"
