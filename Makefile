BINARY_NAME := 9router-go
# Central version — single source: VERSION file, fallback to version.json, then git
VERSION ?= $(shell cat VERSION 2>/dev/null || (cat version.json 2>/dev/null | grep -o '"latestVersion": *"[^"]*"' | cut -d'"' -f4) || git describe --tags --always 2>/dev/null || echo "1.0.0")
PORT ?= 20130
DATA_DIR ?= $(HOME)/.9router
RTK ?=
CAVEMAN ?=
PONYTAIL ?=
AUTO_UPDATE ?= false

LDFLAGS := -s -w -X '9router/proxy/internal/updater.CurrentVersion=$(VERSION)'

.PHONY: build run dev version update test test-short vet bench bench-go cross mitm-enable mitm-disable mitm-status docker docker-build clean help web-build web-dev

## web-build — build frontend static assets (Svelte/Vite) into web/dist
web-build:
	@if [ ! -f web/dist/index.html ] || [ "$$FORCE" = "1" ]; then \
		echo "Building web SPA assets..."; \
		cd web && bun install --frozen-lockfile && bun run build; \
	fi

## build — compile binary with version embedding
build: web-build
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/9router-go/

## run — start proxy (PORT=20130)
run: build
	PORT=$(PORT) DATA_DIR=$(DATA_DIR) ./$(BINARY_NAME) $(if $(RTK),--rtk=$(RTK)) $(if $(CAVEMAN),--caveman=$(CAVEMAN)) $(if $(PONYTAIL),--ponytail=$(PONYTAIL)) --auto-update=$(AUTO_UPDATE)

## dev — start with go run (auto-rebuild)
dev:
	PORT=$(PORT) DATA_DIR=$(DATA_DIR) go run -ldflags="$(LDFLAGS)" ./cmd/9router-go/ $(if $(RTK),--rtk=$(RTK)) $(if $(CAVEMAN),--caveman=$(CAVEMAN)) $(if $(PONYTAIL),--ponytail=$(PONYTAIL)) --auto-update=$(AUTO_UPDATE)

## web-dev — Vite dev server (HMR) on :5173, API proxied to Go :20130. FE changes hot-reload without rebuilding the binary.
web-dev:
	cd web && bun run dev

## version — display binary version info
version: build
	./$(BINARY_NAME) version

## update — check and install binary self-update
update: build
	./$(BINARY_NAME) update

## test — run all unit tests
test:
	go test ./... -v

## test-short — run tests (quiet)
test-short:
	go test ./...

## vet — run go vet static analysis
vet:
	go vet ./...

## bench — run bash comparison benchmark
bench: build
	bash benchmark/run_comparison.sh

## bench-go — run native Go high-throughput benchmark
bench-go:
	go run ./benchmark/runner.go

## cross — cross-compile Linux/macOS/Windows release binaries
cross: web-build
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME)-linux-amd64 ./cmd/9router-go/
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME)-linux-arm64 ./cmd/9router-go/
	GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME)-darwin-amd64 ./cmd/9router-go/
	GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME)-darwin-arm64 ./cmd/9router-go/
	GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME)-windows-amd64.exe ./cmd/9router-go/
	@ls -lh $(BINARY_NAME)-*
	@(sha256sum $(BINARY_NAME)-linux-amd64 $(BINARY_NAME)-linux-arm64 $(BINARY_NAME)-darwin-amd64 $(BINARY_NAME)-darwin-arm64 $(BINARY_NAME)-windows-amd64.exe 2>/dev/null || shasum -a 256 $(BINARY_NAME)-linux-amd64 $(BINARY_NAME)-linux-arm64 $(BINARY_NAME)-darwin-amd64 $(BINARY_NAME)-darwin-arm64 $(BINARY_NAME)-windows-amd64.exe) > SHA256SUMS.txt
	@cat SHA256SUMS.txt

## mitm-enable — start MITM proxy
mitm-enable: build
	./$(BINARY_NAME) mitm enable

## mitm-disable — stop MITM proxy
mitm-disable: build
	./$(BINARY_NAME) mitm disable

## mitm-status — check MITM proxy status
mitm-status: build
	./$(BINARY_NAME) mitm status

## docker — docker compose up
docker:
	docker compose up -d

## docker-build — build Docker image only
docker-build:
	docker build -t $(BINARY_NAME) .

## clean — remove build artifacts
clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME)-*
	rm -rf web/dist

## help — show targets
help:
	@echo "9router-go — Makefile targets:"
	@grep -E '^## ' Makefile | sed 's/## /  make /' | sed 's/ — /  /'
	@echo ""
	@echo "Options:"
	@echo "  make run PORT=3000 VERSION=1.1.0"
	@echo "  make run DATA_DIR=/path/to/data"
	@echo "  make run CAVEMAN=true PONYTAIL=true AUTO_UPDATE=true"
	@echo "  make run RTK=false"
