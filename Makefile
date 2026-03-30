VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/smallest-inc/velocity-cli/cmd.version=$(VERSION)
BIN     := vctl

.PHONY: build install clean test lint

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install:
	go build -ldflags "$(LDFLAGS)" -o /usr/local/bin/$(BIN) .
	@echo "Installed $(BIN) $(VERSION) to /usr/local/bin/$(BIN)"
	@SHELL_NAME=$$(basename $$SHELL); \
	if [ "$$SHELL_NAME" = "zsh" ] && ! grep -q 'source <(vctl completion zsh)' ~/.zshrc 2>/dev/null; then \
		echo 'source <(vctl completion zsh)' >> ~/.zshrc; \
		echo "Shell completion added to ~/.zshrc (restart shell or run: source ~/.zshrc)"; \
	elif [ "$$SHELL_NAME" = "bash" ] && ! grep -q 'source <(vctl completion bash)' ~/.bashrc 2>/dev/null; then \
		echo 'source <(vctl completion bash)' >> ~/.bashrc; \
		echo "Shell completion added to ~/.bashrc (restart shell or run: source ~/.bashrc)"; \
	fi

clean:
	rm -f $(BIN)

test:
	go test ./...

lint:
	go vet ./...
