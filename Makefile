# gao build entrypoints. One Go module, one static binary, so the targets here
# are deliberately thin.

GO      ?= go
GOFLAGS ?=
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# CGO is off by default: the binary is fully static and cross-compilable.
export CGO_ENABLED ?= 0

LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build install test race cover bench vet lint fmt tidy golden takedown clean

all: build

## build: compile the gao binary into ./bin
build:
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o bin/gao ./cmd/gao

## install: install gao into GOBIN
install:
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/gao

## test: run the unit, property, and golden-file tests
test:
	$(GO) test ./...

## race: run the suite under the race detector (CGO is forced on for -race)
race:
	CGO_ENABLED=1 $(GO) test -race ./...

## cover: produce a coverage profile and print the total
cover:
	$(GO) test -coverprofile=cover.out ./...
	$(GO) tool cover -func=cover.out | tail -1

## bench: run the microbenchmarks
bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

## vet: go vet across the module
vet:
	$(GO) vet ./...

## lint: golangci-lint (config in .golangci.yml)
lint:
	golangci-lint run ./...

## fmt: gofmt the tree
fmt:
	gofmt -s -w .

## tidy: prune and verify go.mod / go.sum
tidy:
	$(GO) mod tidy

## golden: regenerate the golden files, then review the diff by hand
golden:
	$(GO) test ./... -update

## takedown: check the takedown register and fail on anything past the promise
takedown:
	$(GO) run ./cmd/gao xoa check
	$(GO) run ./cmd/gao xoa status

## clean: remove build and coverage output
clean:
	rm -rf bin dist cover.out
