.PHONY: build test lint fmt vet clean install

build:
	go build -o bin/anp-cli ./cmd/anp-cli

install:
	go install ./cmd/anp-cli

test:
	go test ./...

lint: fmt vet

fmt:
	gofmt -w $(shell find cmd internal -name '*.go')

vet:
	go vet ./...

clean:
	rm -rf bin
