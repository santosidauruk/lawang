GO ?= go
TOOLS_DIR := $(CURDIR)/bin
SQLC := $(TOOLS_DIR)/sqlc
GOOSE := $(TOOLS_DIR)/goose
STATICCHECK := $(TOOLS_DIR)/staticcheck

SQLC_VERSION := v1.30.0
GOOSE_VERSION := v3.27.1
STATICCHECK_VERSION := 2026.1

ifneq (,$(wildcard ./.env))
include .env
export
endif

IMAGE_TAG ?= local
IMAGE_REGISTRY ?= local

API_IMAGE := $(IMAGE_REGISTRY)/lawang-api:$(IMAGE_TAG)
FAKE_PROVIDER_IMAGE := $(IMAGE_REGISTRY)/lawang-fake-provider:$(IMAGE_TAG)
WORKER_IMAGE := $(IMAGE_REGISTRY)/lawang-worker:$(IMAGE_TAG)

.PHONY: tools deps-up deps-down migrate sql proof proof-events run run-fake-provider run-worker fmt fmt-check vet staticcheck test sqlc-generate sqlc-diff migration-validate compose-validate quality docker-build docker-build-api docker-build-fake-provider docker-build-worker

tools:
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	GOBIN=$(TOOLS_DIR) $(GO) install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	GOBIN=$(TOOLS_DIR) $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)

deps-up:
	docker compose up -d --wait postgres minio redis

deps-down:
	docker compose down

migrate:
	$(GOOSE) -dir sql/migrations postgres "$(DATABASE_URL)" up

sql:
	psql "$(DATABASE_URL)" -f sql/exercises/001_insert_and_select_verification_session.sql

proof:
	psql "$(DATABASE_URL)" -f sql/proofs/001_verification_session_defaults.sql

proof-events:
	psql "$(DATABASE_URL)" -f sql/proofs/003_atomic_session_events.sql

run:
	$(GO) run ./cmd/api

run-fake-provider:
	$(GO) run ./cmd/fake-provider

run-worker:
	$(GO) run ./cmd/worker

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	$(GO) vet ./...

staticcheck:
	$(STATICCHECK) ./...

test:
	$(GO) test -race -timeout=20m ./...

sqlc-generate:
	$(SQLC) generate

sqlc-diff:
	@tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$tmp"' EXIT; \
	cp -R internal/adapter/postgres/sqlc "$$tmp/sqlc"; \
	$(SQLC) generate; \
	diff -ru "$$tmp/sqlc" internal/adapter/postgres/sqlc

migration-validate:
	$(GOOSE) -dir sql/migrations validate

compose-validate:
	docker compose config --quiet

quality: fmt-check vet staticcheck test sqlc-diff migration-validate compose-validate

docker-build-api:
	docker build \
		--target api \
		--tag $(API_IMAGE) \
		.

docker-build-fake-provider:
	docker build \
		--target fake-provider \
		--tag $(FAKE_PROVIDER_IMAGE) \
		.

docker-build-worker:
	docker build \
        --target worker \
        --tag $(WORKER_IMAGE) \
        .

docker-build: docker-build-api docker-build-fake-provider docker-build-worker
