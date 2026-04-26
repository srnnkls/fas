.PHONY: build install test test-integration lint

build:
	CGO_ENABLED=0 go build ./...

# Installs the fas binary into $GOBIN (defaults to $GOPATH/bin), which mise
# puts on PATH. Integration tests invoke `fas` by name, so installation is
# the simplest way to make the binary discoverable without hardcoding paths.
install:
	CGO_ENABLED=0 go install ./cmd/fas

test:
	CGO_ENABLED=0 go test ./...

# End-to-end integration tests driven by scrut. Pipes JSON hook events into
# `fas eval` and snapshots stdout. Installs fas first so the binary is on
# PATH for the scrut subprocesses.
test-integration: install
	scrut test -w . tests/policies.md

lint:
	CGO_ENABLED=0 go vet ./...
