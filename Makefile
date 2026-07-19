# Splice build/test/lint targets.
.DEFAULT_GOAL := build
.PHONY: build build-all test test-race test-memd check vet fmt fmt-check lint tidy clean help install-memd

# Build the main CLI binary into ./splice.
build:
	go build -o splice ./cmd/splice

# Build every command in cmd/.
build-all:
	go build ./...

# Run the full test suite with the race detector (matches CI expectations).
test:
	go test ./... -race -count=1

# Faster, no race detector.
test-quick:
	go test ./...

# Test the memd sidecar (separate Go module; root go test does not see it).
test-memd:
	cd memd && go test ./...

# Full pre-push gate: fmt + vet + build + test in one command. Skips two
# known-local-failures that CI runs clean: TestRealMemdSidecarMemoryRetrieval
# (zombie-memd-socket hang on dev machines) and the WSL2 sandbox backend
# selection test (false-positive under WSL2; passes on real Linux). Use
# `make test` for the unskipped race suite that matches CI.
check: fmt-check vet build-all
	go test -count=1 -skip 'TestRealMemdSidecarMemoryRetrieval|TestSelectBackendChoosesPlatformAdapterWithFallback' ./...

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
	rm -f splice
	go clean ./...

help:
	@echo "Targets: build (default), build-all, check, test, test-quick, test-memd, vet, fmt, fmt-check, lint, tidy, install-memd, clean"
