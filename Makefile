.PHONY: build run test clean install

BINARY_NAME=cronplus
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_FLAGS=-ldflags "-s -w -X main.version=$(VERSION)"
GO_CACHE_DIR ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE_DIR ?= $(CURDIR)/.cache/go-mod
GO_ENV=GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)"

build:
	$(GO_ENV) go build $(BUILD_FLAGS) -o $(BINARY_NAME) .

run: build
	./$(BINARY_NAME)

test:
	$(GO_ENV) go test ./... -v

clean:
	rm -f $(BINARY_NAME)
	$(GO_ENV) go clean

install: build
	cp $(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@echo "Installed $(BINARY_NAME) to /usr/local/bin/"

uninstall:
	rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "Removed $(BINARY_NAME) from /usr/local/bin/"

lint:
	$(GO_ENV) go vet ./...

fmt:
	gofmt -s -w .

# Development: build and run with live reload (requires entr)
dev:
	find . -name '*.go' -o -name '*.html' -o -name '*.css' -o -name '*.js' | entr -r make run
