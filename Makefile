.PHONY: build test vet install clean

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS = -X github.com/behnambm/gcli/internal/cmd.toolVersion=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o gcli .

test:
	go test ./...

vet:
	go vet ./...

install: build
	install -m 0755 gcli /usr/local/bin/gcli

clean:
	rm -f gcli
