.PHONY: build run tidy test

build:
	go build -o bin/tributary ./cmd/tributary

run: build
	./bin/tributary

tidy:
	go mod tidy

test:
	go test ./...
