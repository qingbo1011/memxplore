.PHONY: build check fmt test vet

build:
	go build -o bin/memxplore ./cmd/memxplore

check: fmt vet test

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test -race ./...

vet:
	go vet ./...

