export CGO_ENABLED ?= 0

# Resolve a version to stamp into the binary; falls back to "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build
build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o outfit ./cmd/outfit

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: run
run:
	go run ./cmd/outfit $(filter-out $@,$(MAKECMDGOALS))

.PHONY: test
test:
	go test ./...

.PHONY: coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: coverage-html
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html
