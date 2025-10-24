# Makefile for iporg - IP Organization Lookup Tools

.PHONY: all build test clean install help

# Default target
all: build

# Build all binaries
build:
	@echo "Building iporg tools..."
	@go build -o bin/iporg-build ./cmd/iporg-build
	@go build -o bin/iporg-lookup ./cmd/iporg-lookup
	@go build -o bin/iporg-bulk ./cmd/iporg-bulk
	@go build -o bin/iptoasn-build ./cmd/iptoasn-build
	@go build -o bin/iptoasn-query ./cmd/iptoasn-query
	@go build -o bin/ripe-bulk-build ./cmd/ripe-bulk-build
	@go build -o bin/ripe-bulk-query ./cmd/ripe-bulk-query
	@echo "Build complete. Binaries in ./bin/"

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./pkg/...
	@echo "Tests complete."

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	@go test -v -coverprofile=coverage.out ./pkg/...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Install binaries to GOPATH/bin
install:
	@echo "Installing iporg tools..."
	@go install ./cmd/iporg-build
	@go install ./cmd/iporg-lookup
	@go install ./cmd/iporg-bulk
	@go install ./cmd/iptoasn-build
	@go install ./cmd/iptoasn-query
	@go install ./cmd/ripe-bulk-build
	@go install ./cmd/ripe-bulk-query
	@echo "Installation complete."

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf bin/
	@rm -rf data/
	@rm -rf cache/
	@rm -f coverage.out coverage.html
	@echo "Clean complete."

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Lint code (requires golangci-lint)
lint:
	@echo "Linting code..."
	@golangci-lint run ./...

# Tidy dependencies
tidy:
	@echo "Tidying dependencies..."
	@go mod tidy

# Help
help:
	@echo "Available targets:"
	@echo "  make build          - Build all binaries"
	@echo "  make test           - Run tests"
	@echo "  make test-coverage  - Run tests with coverage report"
	@echo "  make install        - Install binaries to GOPATH/bin"
	@echo "  make clean          - Remove build artifacts"
	@echo "  make fmt            - Format code"
	@echo "  make lint           - Lint code (requires golangci-lint)"
	@echo "  make tidy           - Tidy dependencies"
	@echo "  make help           - Show this help"
