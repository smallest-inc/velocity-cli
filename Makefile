VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/smallest-inc/velocity-cli/cmd.version=$(VERSION)
BIN     := vctl

.PHONY: build install clean test lint

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install:
	go build -ldflags "$(LDFLAGS)" -o /usr/local/bin/$(BIN) .
	@echo "Installed $(BIN) $(VERSION) to /usr/local/bin/$(BIN)"

clean:
	rm -f $(BIN)

test:
	go test ./...

lint:
	go vet ./...
