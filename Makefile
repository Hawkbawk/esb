.PHONY: default install clean lint

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
	staticcheck ./...
