.PHONY: all build run test test-short lint vet generate migration db-up db-down openapi web-client web web-dev docker helm-lint clean deps

APP := infra-monitor
GOCMD := go
DEV_DB_URL := postgres://postgres:postgres@localhost:55432/postgres?sslmode=disable
CONFIG := $(if $(wildcard config/config.yaml),config/config.yaml,config/config.example.yaml)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/xonoko/infra-monitor/internal/buildinfo.Version=$(VERSION) \
	-X github.com/xonoko/infra-monitor/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/xonoko/infra-monitor/internal/buildinfo.Date=$(DATE)
IMAGE ?= ghcr.io/xonoko/infra-monitor:dev

GREEN := $(shell printf '\033[32m')
RED   := $(shell printf '\033[31m')
RESET := $(shell printf '\033[0m')

all: generate web build test lint helm-lint

build:
	CGO_ENABLED=0 $(GOCMD) build -ldflags "$(LDFLAGS)" -o $(APP) ./cmd/infra-monitor

run: build
	./$(APP) serve --config $(CONFIG)

# Unit + integration tests (integration tests need `make db-up`).
test:
	@$(GOCMD) test -count=1 ./... | grep -v '\[no test files\]' | sed -e 's/^ok/$(GREEN)ok$(RESET)/' -e 's/^FAIL/$(RED)FAIL$(RESET)/'

test-short:
	$(GOCMD) test -short ./...

lint:
	golangci-lint run ./...

vet:
	$(GOCMD) vet ./...

generate:
	$(GOCMD) generate ./internal/db/ent

# Create a versioned migration from the ent schemas: make migration name=add_foo
migration:
	@test -n "$(name)" || (echo "usage: make migration name=<name>" && exit 1)
	$(GOCMD) run ./cmd/migrategen -name $(name) -dev-url '$(DEV_DB_URL)'
	atlas migrate hash --dir file://internal/db/migrations

db-up:
	docker compose up -d --wait

db-down:
	docker compose down

openapi:
	$(GOCMD) run ./cmd/infra-monitor openapi --config config/config.example.yaml > web/openapi.json

web-client: openapi
	cd web && npm run gen:api

web:
	cd web && npm ci && npm run build

web-dev:
	cd web && npm run dev

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE) .

helm-lint:
	helm lint deploy/helm/infra-monitor
	@for f in deploy/helm/infra-monitor/ci/*.yaml; do \
		echo "helm template $$f"; \
		helm template im deploy/helm/infra-monitor -f $$f > /dev/null || exit 1; \
	done

deps:
	$(GOCMD) mod tidy

clean:
	rm -f $(APP)
