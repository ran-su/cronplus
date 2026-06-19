.PHONY: build run test clean install

BINARY_NAME=cronplus
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_FLAGS=-ldflags "-s -w -X main.version=$(VERSION)"
GO_CACHE_DIR ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE_DIR ?= $(CURDIR)/.cache/go-mod
GO_ENV=GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)"
INSTALL_BINDIR ?= $(shell if [ -d /opt/homebrew/bin ]; then echo /opt/homebrew/bin; else echo /usr/local/bin; fi)

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
	install -d "$(INSTALL_BINDIR)"
	install -m 0755 "$(BINARY_NAME)" "$(INSTALL_BINDIR)/$(BINARY_NAME)"
	@echo "Installed $(BINARY_NAME) to $(INSTALL_BINDIR)/"

uninstall:
	rm -f "$(INSTALL_BINDIR)/$(BINARY_NAME)"
	@echo "Removed $(BINARY_NAME) from $(INSTALL_BINDIR)/"

lint:
	$(GO_ENV) go vet ./...

fmt:
	gofmt -s -w .

# Development: build and run with live reload (requires entr)
dev:
	find . -name '*.go' -o -name '*.html' -o -name '*.css' -o -name '*.js' | entr -r make run
