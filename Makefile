.PHONY: build test lint

build:
	CGO_ENABLED=0 go build ./...

test:
	CGO_ENABLED=0 go test ./...

lint:
	CGO_ENABLED=0 go vet ./...
