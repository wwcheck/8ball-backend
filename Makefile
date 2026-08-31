.PHONY: help run build test clean tidy fmt

help:
	@echo "Sud8Ball Backend - Available Commands"
	@echo "======================================"
	@echo "  make run    - Run development server"
	@echo "  make build  - Build binary"
	@echo "  make test   - Run tests"
	@echo "  make tidy   - Tidy dependencies"
	@echo "  make fmt    - Format code"
	@echo "  make clean  - Clean build artifacts"

run:
	@echo "Starting Sud8Ball Backend Server..."
	go run cmd/server/main.go

build:
	@echo "Building Sud8Ball Backend..."
	CGO_ENABLED=0 go build -o bin/server.exe cmd/server/main.go
	@echo "Build complete: bin/server.exe"

test:
	@echo "Running tests..."
	go test -v -cover ./...

tidy:
	@echo "Tidying dependencies..."
	go mod tidy

fmt:
	@echo "Formatting code..."
	go fmt ./...

clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	go clean

dev:
	@make run

prod:
	@make build
