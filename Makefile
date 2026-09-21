.PHONY: build run demo test check install
build:
	go build -o bin/ntty .
run:
	go run .
demo:
	go run . --demo
test:
	go test -race ./...
check:
	go vet ./...
install: build
	mkdir -p $(HOME)/.local/bin
	install -m 755 bin/ntty $(HOME)/.local/bin/ntty
