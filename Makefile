export CGO_ENABLED ?= 0

.PHONY: build
build:
	go build -ldflags="-s -w" -o outfit

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: run
run:
	go run . $(filter-out $@,$(MAKECMDGOALS))

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
