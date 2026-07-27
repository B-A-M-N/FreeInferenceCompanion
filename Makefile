.PHONY: build clean install test lint

BINARY=fi
BUILD_DIR=build
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/fi/

build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/fi/

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/fi/

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/fi/

build-all: build-linux-amd64 build-linux-arm64 build-darwin-arm64

install: build
	install -m 755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./... -v -count=1

lint:
	go vet ./...

tidy:
	go mod tidy

run:
	go run ./cmd/fi/ $(ARGS)

# Quick smoke test
smoke: build
	./$(BUILD_DIR)/$(BINARY) help
	./$(BUILD_DIR)/$(BINARY) models --refresh || echo "models may need API key"
	./$(BUILD_DIR)/$(BINARY) doctor || echo "doctor may need API key"
	@echo "Smoke test complete"