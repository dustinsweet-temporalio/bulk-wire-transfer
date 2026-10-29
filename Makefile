# Bulk wire reference implementation: developer and presenter entry points.
# `make up` starts the whole stack; `make urls` prints where everything is.

SHELL := /bin/bash
.DEFAULT_GOAL := help

BACKEND ?= redis
COMPOSE := docker compose
LOG_SERVICES := worker-a worker-b bulkwire-service core-lakeshore core-prairie control-service
DOTNET_SDK_IMAGE := mcr.microsoft.com/dotnet/sdk:10.0.403
GO_IMAGE := golang:1.27.3-alpine3.24
# Go commands run in the pinned Go image (no local Go needed); module and build caches persist in named volumes.
GO_RUN := docker run --rm -v "$(CURDIR):/repo" -v bulkwire-gomod:/go/pkg/mod -v bulkwire-gocache:/root/.cache/go-build -w /repo/go $(GO_IMAGE)

.PHONY: help up down reset logs test test-go test-dotnet test-ui test-claimcheck e2e samples \
        capture-history replay replay-nde demo-reset versioning-demo urls config-check

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

up: export CLAIMCHECK_BACKEND = $(BACKEND)
up: export COMPOSE_PROFILES = local-temporal,claimcheck-$(BACKEND)
up: ## Build and start the stack (BACKEND=redis|s3|gcs)
	$(COMPOSE) up -d --build
	# Created, not started: the Control Center starts them during the versioning demo.
	$(COMPOSE) --profile versioning-demo create --build worker-v2 worker-v3
	./scripts/wait-for.sh http://localhost:3001/ 180
	$(COMPOSE) wait temporal-init
	@$(MAKE) --no-print-directory urls

down: ## Stop the stack (keeps data)
	$(COMPOSE) --profile '*' down

reset: ## Stop the stack and delete all volumes (Temporal history, Core databases, claim check)
	$(COMPOSE) --profile '*' down -v

logs: ## Follow logs of workers, bulkwire-service, Cores, control-service
	$(COMPOSE) logs -f $(LOG_SERVICES)

test: test-go test-dotnet test-ui ## Run all unit tests

test-go: ## Go unit + workflow tests (in the pinned Go container)
	$(GO_RUN) go test ./...

test-dotnet: ## .NET tests (in the pinned SDK container; no local .NET needed)
	docker run --rm -v "$(CURDIR):/repo" -v bulkwire-nuget:/root/.nuget/packages -w /repo/core $(DOTNET_SDK_IMAGE) \
	  sh -c 'dotnet build CoreApi.slnx -c Release -nologo -v q && dotnet test --solution CoreApi.slnx -c Release --no-build'

NODE_IMAGE := node:26.13.0-alpine3.24

test-ui: ## Portal and Control Center type-check, build, and unit tests (in the pinned Node LTS container)
	docker run --rm -v "$(CURDIR)/ui:/app" -v bulkwire-ui-modules:/app/node_modules -w /app $(NODE_IMAGE) \
	  sh -c 'npm ci --no-audit --no-fund >/dev/null && npm run build && npm test'
	docker run --rm -v "$(CURDIR):/repo" -v bulkwire-cc-modules:/repo/control-ui/node_modules -w /repo/control-ui $(NODE_IMAGE) \
	  sh -c 'npm ci --no-audit --no-fund >/dev/null && npm run build && npm test'

test-claimcheck: ## Claim-check contract tests against redis, s3 (RustFS), and gcs inside the compose network
	./scripts/test-claimcheck.sh

e2e: ## End-to-end checks against a running stack
	./scripts/e2e.sh

samples: ## Write the deterministic sample CSVs to samples/
	$(GO_RUN) go run ./cmd/samplegen -out ../samples

capture-history: ## Export a workflow history for replay tests (WF=bulkwire/<fi>/<batch> NAME=scenario-a)
	./scripts/capture-history.sh "$(WF)" "$(NAME)"

replay: ## Replay captured histories against the current workflow code
	$(GO_RUN) go test ./internal/workflow -run TestReplay -count=1 -v

replay-nde: ## Show a non-determinism error from a reordered workflow (catalog F39); fails on purpose
	$(GO_RUN) go test -tags ndedemo ./internal/workflow -run TestReplayNonDeterminism -count=1 -v

demo-reset: ## Reset the demo through the Control Center
	curl -fsS -X POST http://localhost:3100/api/control/reset | (command -v jq >/dev/null && jq . || cat)

versioning-demo: ## Rebuild worker-v2/worker-v3 images and recreate their stopped containers
	$(COMPOSE) --profile versioning-demo build worker-v2 worker-v3
	$(COMPOSE) --profile versioning-demo create --force-recreate worker-v2 worker-v3

config-check: ## Validate docker-compose.yml for every backend profile and versioning-demo
	@for p in redis s3 gcs; do \
	  CLAIMCHECK_BACKEND=$$p COMPOSE_PROFILES=local-temporal,claimcheck-$$p $(COMPOSE) config -q && echo "compose ok: $$p"; \
	done
	@COMPOSE_PROFILES=local-temporal,claimcheck-redis,versioning-demo $(COMPOSE) config -q && echo "compose ok: versioning-demo"

urls: ## Print every URL
	@echo "Lakeshore portal   http://localhost:3001"
	@echo "Prairie portal     http://localhost:3002"
	@echo "Control Center     http://localhost:3100"
	@echo "Temporal UI        http://localhost:8233/namespaces/bulkwire/workflows"
	@echo "Codec Server       http://localhost:8089"
	@echo "Worker metrics     http://localhost:9464/metrics  http://localhost:9465/metrics"
	@echo "RustFS console     http://localhost:9001  (BACKEND=s3 only)"
