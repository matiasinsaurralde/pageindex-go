.PHONY: test build lint ci

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run ./...

ci: test build lint
