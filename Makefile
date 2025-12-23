.PHONY: build test clean run-redis run-server run-client lint fmt tidy docs demo

# Build all packages
build:
	go build ./...

# Run tests
test:
	go test ./...

# Start Redis via Docker Compose
run-redis:
	docker-compose up -d

# Stop Redis
stop-redis:
	docker-compose down

# Run example server
run-server:
	go run ./examples/server/main.go

# Run example client
run-client:
	go run ./examples/client/main.go

# Run Web UI
run-ui:
	go run ./ui

# Clean build artifacts
clean:
	go clean
	rm -f titanqueue

# Run linter
lint:
	golangci-lint run

# Format code
fmt:
	go fmt ./...

# Tidy dependencies
tidy:
	go mod tidy

# Generate documentation
docs:
	godoc -http=:6060

# Run demo (start redis, enqueue tasks, process them)
demo: run-redis
	@echo "Waiting for Redis..."
	@sleep 2
	go run ./examples/client/main.go
	go run ./examples/server/main.go
