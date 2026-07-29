.PHONY: all build clean test install

BINARY=deploy
BINDIR=bin
GOFLAGS=-ldflags="-s -w -X 'deploy/cmd.Version=0.2.0'"

all: test build

build:
	@mkdir -p $(BINDIR)
	go build $(GOFLAGS) -o $(BINDIR)/$(BINARY) .

clean:
	rm -rf $(BINDIR)/

test:
	go test ./... -v -count=1

test-short:
	go test ./... -count=1

install: build
	@echo "Installing $(BINARY) to /usr/local/bin/ (may need sudo)"
	install -m 0755 $(BINDIR)/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "Run 'deploy init' to set up the environment"

run-daemon: build
	./$(BINDIR)/$(BINARY) daemon
