BINARY := gossm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
	-X github.com/bovinemagnet/gossm/cmd.Version=$(VERSION) \
	-X github.com/bovinemagnet/gossm/cmd.Commit=$(COMMIT) \
	-X github.com/bovinemagnet/gossm/cmd.Date=$(DATE)

.PHONY: build clean test vet tidy

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) .

test:
	go test -race ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
