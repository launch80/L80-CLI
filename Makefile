BINARY := bin/L80
VERSION ?= 1.1.0
LDFLAGS := -s -w -X github.com/launch80/L80-CLI/internal/cli.Version=$(VERSION)

.PHONY: build install test fmt vet clean hooks bump-tools check-manifest

# rm first: on a case-insensitive filesystem (default macOS) an existing
# bin/l80 keeps its old directory entry when overwritten, so the release
# tarball would ship a lowercase name that Linux then fails to find.
build:
	@rm -f $(BINARY) bin/l80
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/L80

install: build
	mkdir -p $(HOME)/.local/bin
	cp $(BINARY) $(HOME)/.local/bin/.L80.new && mv -f $(HOME)/.local/bin/.L80.new $(HOME)/.local/bin/L80
	@echo "installed to $(HOME)/.local/bin/L80"

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

clean:
	rm -rf bin

# Pin each Python tool to the commit checked out in its local working copy
# (paths in tools.local.json). Commit and push in those repos first.
#   make bump-tools            all tools with a local path
#   make bump-tools ONLY=pwdet one tool
bump-tools:
	go run ./cmd/bump-tools $(if $(ONLY),-only $(ONLY))

# Confirm every pin is fetchable (and, with SMOKE=1, that uvx can run it).
check-manifest:
	./scripts/check-manifest.sh $(if $(SMOKE),--smoke)

# Point git at the committed hooks, which refuse commits and pushes that are
# not authored by the shared launch80 identity (team@launch80.com).
hooks:
	git config core.hooksPath .githooks
	git config user.name launch80
	git config user.email team@launch80.com
	@echo "hooks enabled; commits and pushes must use team@launch80.com"
