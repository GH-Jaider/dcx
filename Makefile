.PHONY: build run test fmt vet tidy check install

build:
	go build -o bin/dcx ./cmd/dcx

run: build
	./bin/dcx

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

tidy:
	go mod tidy

check: fmt vet test

install:
	go install ./cmd/dcx
