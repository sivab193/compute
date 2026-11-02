# Shared Compute Pool — build + cross-compile targets.
#
# The agent is a single static binary with no runtime deps, so it cross-compiles
# to the whole heterogeneous fleet from one machine (the §2 design goal).

VERSION   := 0.1.0
BIN       := bin
DIST      := dist
LDFLAGS   := -s -w
GOFLAGS   := -trimpath

.PHONY: all server agent cli build test vet fmt dist clean run-server

all: build

build: server agent cli

server:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/compute-server ./cmd/compute-server

agent:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/pool-agent ./cmd/pool-agent

cli:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/pool ./cmd/pool

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# Cross-compile agent binaries for the fleet into dist/, which the control
# plane can serve at /dist for one-command node bootstrap (§5).
dist:
	mkdir -p $(DIST)
	GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/pool-agent-linux-amd64   ./cmd/pool-agent
	GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/pool-agent-linux-arm64   ./cmd/pool-agent
	GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/pool-agent-darwin-arm64  ./cmd/pool-agent
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/pool-agent-windows-amd64.exe ./cmd/pool-agent
	cp scripts/bootstrap-node.sh $(DIST)/bootstrap-node.sh
	@echo "fleet binaries in $(DIST)/"

run-server: server
	./$(BIN)/compute-server --addr 127.0.0.1:8080 --dist $(DIST)

clean:
	rm -rf $(BIN) $(DIST)
