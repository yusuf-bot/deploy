.PHONY: all build clean test test-short test-integration install run-daemon

BINARY=deploy
BINDIR=bin
GOFLAGS=-ldflags="-s -w -X 'deploy/cmd.Version=0.3.0'"

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

# Docker-backed integration tests. Each test uses a hermetic temp DEPLOY_DATA_DIR
# and never touches the production data dir or socket; tests are skipped when the
# Docker daemon is unreachable.
test-integration:
	go test -tags integration -count=1 ./internal/integration/...

install: build
	@echo "Installing $(BINARY) to /usr/local/bin/ (may need sudo)"
	install -m 0755 $(BINDIR)/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "Run 'deploy init' to set up the environment"

run-daemon: build
	./$(BINDIR)/$(BINARY) daemon
