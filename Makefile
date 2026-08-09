.PHONY: default install clean lint bump-vendor-hash

default:
	go generate ./...
	go build -o esb .

setup:
	go mod download
	go generate ./...

install: setup
	go install .

clean:
	go clean ./...
	go mod tidy

lint:
	gofmt -l -w .
	go vet ./...
	go fix ./...
	staticcheck ./...

bump-vendor-hash:
	nix-update --flake esb --version skip
