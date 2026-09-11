BINARY   := ift
VERSION  ?= dev
IMAGE    ?= ghcr.io/williamokano/insta-follower-tracker
LDFLAGS  := -s -w -X main.version=$(VERSION)

.PHONY: all build run test test-race lint fmt tidy docker docker-run clean

all: lint test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/ift

run:
	IFT_DATA_DIR=$(CURDIR)/.localdata go run ./cmd/ift

test:
	go test ./...

test-race:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...

lint:
	golangci-lint run

fmt:
	gofmt -w .

tidy:
	go mod tidy

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

docker-run: docker
	docker run --rm -p 8080:8080 -e PUID=$(shell id -u) -e PGID=$(shell id -g) \
		-v $(CURDIR)/.localdata:/data $(IMAGE):$(VERSION)

clean:
	rm -rf bin coverage.out .localdata
