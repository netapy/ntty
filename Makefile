.PHONY: build run demo test check install
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
build:
	go build -ldflags "$(LDFLAGS)" -o bin/ntty .
run:
	go run -ldflags "$(LDFLAGS)" .
demo:
	go run -ldflags "$(LDFLAGS)" . --demo
test:
	go test -race ./...
check:
	go vet ./...
install: build
	mkdir -p $(HOME)/.local/bin
	install -m 755 bin/ntty $(HOME)/.local/bin/ntty
