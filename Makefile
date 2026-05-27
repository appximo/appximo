.PHONY: build test lint run

build:
	go build ./...

test:
	go test ./... -race

lint:
	golangci-lint run ./...

run:
	go run ./cmd/appitools/main.go
