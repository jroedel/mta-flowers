# The one verify command is `make check`. Everything else here is a convenience
# or a deploy-time tool.

GO      ?= go
BINARY  ?= mta-flowers
PKG     ?= ./...

.DEFAULT_GOAL := help

## check: gofmt, vet, staticcheck, build and test. Run after editing any .go file.
.PHONY: check
check: fmt vet staticcheck build test

.PHONY: fmt
fmt:
	gofmt -s -w .

## lint: the read-only half of check -- fails rather than fixing. What CI runs.
.PHONY: lint
lint: vet staticcheck
	@out=$$(gofmt -s -l .); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi
	@echo "gofmt clean"

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: staticcheck
staticcheck:
	@command -v staticcheck >/dev/null || { echo "staticcheck missing; run: make dev-tools"; exit 1; }
	staticcheck ./...

.PHONY: build
build:
	$(GO) build ./...

.PHONY: test
test:
	$(GO) test -race ./...

## release: the static linux binary the deploy workflow ships.
# CGO_ENABLED=0 is not optional. modernc.org/sqlite is pure Go so nothing needs
# cgo, but an ordinary `go build` still links against the builder's glibc, and
# the result dies with "GLIBC_2.xx not found" on a machine with a different one.
# That failure does not appear until the binary is already on the server.
.PHONY: release
release:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build \
		-trimpath -ldflags "-s -w" -o $(BINARY)-$(GOOS)-$(GOARCH) .

## run: the server, against a local database, for development.
.PHONY: run
run:
	$(GO) run . 

## sym: find a Go declaration by name. Faster and more exact than grep.
.PHONY: sym
sym:
	@test -n "$(NAME)" || { echo "usage: make sym NAME=Commitment"; exit 1; }
	@gopls definition $$(gopls workspace_symbol $(NAME) | head -1 | cut -d' ' -f1) 2>/dev/null \
		|| gopls workspace_symbol $(NAME)

## outline: a map of one file -- every declaration with its position.
.PHONY: outline
outline:
	@test -n "$(FILE)" || { echo "usage: make outline FILE=internal/store/store.go"; exit 1; }
	@gopls symbols $(FILE)

## dev-tools: install the pinned tools the checks need.
.PHONY: dev-tools
dev-tools:
	$(GO) install honnef.co/go/tools/cmd/staticcheck@2025.1.1
	$(GO) install golang.org/x/tools/gopls@latest

.PHONY: help
help:
	@echo "mta-flowers -- Feast of Our Lady of Schoenstatt, 17 October 2026"
	@echo
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## deploy-ready: everything that must be true before pushing to main. main deploys.
.PHONY: deploy-ready
deploy-ready:
	@# Exit 2 is "nothing broken, work still to do", which is the normal state
	@# of a deployment in progress and must not fail the target. A real
	@# failure (exit 1) still does.
	@scripts/deploy-ready || [ $$? -eq 2 ]
