# ---------------------------------------------------------------------------
# Vayalavan — developer entrypoint.
#
#   make doctor       check tooling, ports and .env before anything else
#   make dev          infra + migrations + seed + all 10 app processes
#   make dev-api      infra + gateway and the 3 Go APIs (no UIs)
#   make dev-client   infra + gateway, catalog, orders, customer UI
#   make help         list every target
#
# Run from anywhere: `make -C infra dev`.
# ---------------------------------------------------------------------------

INFRA_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
ROOT_DIR  := $(abspath $(INFRA_DIR)/..)

# ONE env file for the whole platform, at the repository root.
ENV_FILE     := $(ROOT_DIR)/.env
ENV_EXAMPLE  := $(ROOT_DIR)/.env.example
COMPOSE_FILE := $(INFRA_DIR)/docker-compose.yml
COMPOSE      := docker compose --env-file $(ENV_FILE) -f $(COMPOSE_FILE)

# Migration order is a dependency order, not alphabetical: profile owns
# identity, catalog references suppliers, orders references both.
GO_SERVICES := vm-profile-api vm-catalog-api vm-orders-api
# Migrated after the three above: it owns the analytics schema, which nothing
# else references. Kept out of GO_SERVICES because migrate-down's warning and
# order are about the domain schemas.
CONSUMER_SERVICE := vm-events-consumer-api

SCRIPTS   := $(INFRA_DIR)/scripts
TOOLS_BIN := $(ROOT_DIR)/.tools/bin

.DEFAULT_GOAL := help

.PHONY: help doctor env deps tools up wait down stop status logs ps \
        dev dev-api dev-client \
        migrate-up migrate-down migrate-status cdc-setup seed verify-storage \
        mobile mobile-client mobile-deps mobile-supplier-deps mobile-client-deps mobile-ip \
        mobile-tunnel mobile-client-tunnel mobile-android mobile-ios \
        mobile-client-android mobile-client-ios mobile-doctor mobile-test mobile-lint \
        sync-brand test test-go test-py test-js test-e2e test-e2e-auth test-e2e-catalog test-e2e-availability test-e2e-cart test-e2e-close test-e2e-settlement test-e2e-customer test-e2e-rates test-e2e-refresh test-e2e-theft test-e2e-range test-e2e-processor test-e2e-commission test-e2e-markup smoke reset-orders loadcheck lint lint-go lint-js fmt reset clean

help: ## Show this help
	@echo "Vayalavan — make targets"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  gateway 8080 | profile 8081 | catalog 8082 | orders 8083"
	@echo "  events-consumer 8084 | analytics-api 8085"
	@echo "  client 5173  | admin 5174   | supplier 5175 | analytics 5176"
	@echo "  postgres 5432 | minio 9000/9001 | mailhog 1025/8025"
	@echo "  mobile apps (Expo, alongside make dev):"
	@echo "    make mobile         supplier  — Vayalavan Supplier"
	@echo "    make mobile-client  customer  — Vayalavan"

doctor: ## Check tooling, ports and .env completeness
	@$(SCRIPTS)/doctor.sh

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------

env: ## Create .env from .env.example if missing
	@if [ ! -f $(ENV_FILE) ]; then \
		cp $(ENV_EXAMPLE) $(ENV_FILE); \
		echo "  created .env from .env.example"; \
	fi

tools: ## Install air and goose into .tools/bin (pinned, not global)
	@mkdir -p $(TOOLS_BIN)
	@if [ ! -x $(TOOLS_BIN)/air ]; then \
		echo "  installing air"; \
		GOBIN=$(TOOLS_BIN) go install github.com/air-verse/air@latest; \
	fi
	@if [ ! -x $(TOOLS_BIN)/goose ]; then \
		echo "  installing goose (for authoring migrations)"; \
		GOBIN=$(TOOLS_BIN) go install github.com/pressly/goose/v3/cmd/goose@latest; \
	fi
	@echo "  tools ready in .tools/bin"

sync-brand: ## Copy the master logo from vm-ui-kit into each app's public/
	@$(SCRIPTS)/sync-brand.sh

deps: tools ## Install all dependencies and build the UI kit
	@echo "==> npm workspaces"
	@cd $(ROOT_DIR) && npm install --no-audit --no-fund --silent
	@echo "==> go workspace"
	@cd $(ROOT_DIR) && go work sync
	@echo "==> vm-analytics-api (uv)"
	@cd $(ROOT_DIR)/vm-analytics-api && uv sync --quiet
	@echo "==> building @vayal/ui-kit (the three UIs import it)"
	@cd $(ROOT_DIR) && npm run build --workspace @vayal/ui-kit --silent

# ---------------------------------------------------------------------------
# Stack lifecycle
# ---------------------------------------------------------------------------

up: env ## Start backing services (postgres, minio, mailhog)
	@$(COMPOSE) up -d
	@$(MAKE) --no-print-directory wait

wait: ## Block until postgres and minio report healthy
	@$(SCRIPTS)/wait-for-stack.sh

## The three dev targets share one preparation path and differ only in which
## processes they run. Nobody should need ten processes to work on one thing.
dev: env deps up migrate-up seed ## Everything: infra + 10 app processes
	@$(SCRIPTS)/run.sh all

dev-api: env deps up migrate-up seed ## Backend only: gateway + 3 Go APIs
	@$(SCRIPTS)/run.sh api

dev-client: env deps up migrate-up seed ## Storefront slice: gateway, catalog, orders, client UI
	@$(SCRIPTS)/run.sh client

down: ## Stop containers (app processes stop with Ctrl-C)
	@$(COMPOSE) down

stop: down ## Alias for down

status: ## Show health of containers and app processes
	@$(SCRIPTS)/status.sh

logs: ## Tail container logs
	@$(COMPOSE) logs -f --tail=100

ps: ## List running containers
	@$(COMPOSE) ps

# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

## Each service migrates its OWN schema with its OWN role, in dependency
## order. There is no central migration runner: a single privileged migrator
## would quietly undo the per-schema isolation the init script sets up.
migrate-up: ## Apply pending migrations (profile -> catalog -> orders -> analytics) + CDC setup
	@printf '  %-34s' 'provisioning analytics roles'; \
		$(SCRIPTS)/analytics-db.sh roles >/dev/null && echo "ok" || { echo "FAILED"; exit 1; }
	@for svc in $(GO_SERVICES) $(CONSUMER_SERVICE); do \
		printf '  migrating %-24s' "$$svc"; \
		(cd $(ROOT_DIR)/$$svc && go run ./cmd/api -migrate up >/dev/null) \
			&& echo "ok" || { echo "FAILED"; exit 1; }; \
	done
	@$(MAKE) --no-print-directory cdc-setup

cdc-setup: ## Create the analytics publication and replication slot (idempotent)
	@printf '  %-34s' 'publishing outbox tables'; \
		$(SCRIPTS)/analytics-db.sh cdc >/dev/null && echo "ok" || { echo "FAILED"; exit 1; }

migrate-down: ## Roll back one migration per service (DESTRUCTIVE — see warning)
	@echo "  This rolls back the last migration in EVERY service, not just one."
	@echo "  Rolling back vm-profile-api's identity migration DROPS users and"
	@echo "  suppliers. Catalog references suppliers by id with no cross-schema"
	@echo "  FK (CLAUDE.md §3), so products silently orphan and the storefront"
	@echo "  goes empty. To roll back ONE service, run goose in that directory:"
	@echo "      cd vm-orders-api && go run ./cmd/api -migrate down"
	@echo ""
	@printf "  Type ROLLBACK to continue: "; read reply; \
		[ "$$reply" = "ROLLBACK" ] || { echo "  Cancelled."; exit 1; }
	@for svc in vm-orders-api vm-catalog-api vm-profile-api; do \
		printf '  rolling back %-16s' "$$svc"; \
		(cd $(ROOT_DIR)/$$svc && go run ./cmd/api -migrate down >/dev/null) \
			&& echo "ok" || { echo "FAILED"; exit 1; }; \
	done

migrate-status: ## Show applied and pending migrations per service
	@for svc in $(GO_SERVICES) $(CONSUMER_SERVICE); do \
		echo "==> $$svc"; \
		(cd $(ROOT_DIR)/$$svc && go run ./cmd/api -migrate status) || exit 1; \
	done

seed: ## Load development seed data
	@$(SCRIPTS)/seed.sh

verify-storage: ## Prove presigned MinIO upload/download works end to end
	@cd $(ROOT_DIR)/vm-catalog-api && go run ./cmd/api -verify-storage

# ---------------------------------------------------------------------------
# The mobile apps (React Native / Expo)
#
#   vm-supplier-mobile-ui  "Vayalavan Supplier"     — `make mobile`
#   vm-client-mobile-ui    "Vayalavan"  — `make mobile-client`
#
# Kept out of `make dev` on purpose. The other seven processes are servers that
# either start or fail; a mobile app needs a simulator or a paired handset, and
# blocking `make dev` on that would stop the whole platform booting for anyone
# who has neither installed. Start one alongside `make dev` when you want it.
#
# Both share MOBILE_API_BASE_URL — one gateway, one address — and `mobile-ip`
# sets it for both. Everything else is per app, including the Metro port, so
# the two dev servers can run at the same time.
#
# `mobile` stays the supplier app for muscle memory; the customer app is always
# spelled out. Anything that applies to both (deps, test, lint) does both.
# ---------------------------------------------------------------------------

MOBILE_DIR        := $(ROOT_DIR)/vm-supplier-mobile-ui
MOBILE_CLIENT_DIR := $(ROOT_DIR)/vm-client-mobile-ui

# Metro's own default is 8081, which is vm-profile-api's port. With the stack up
# Expo would fail to bind, or slide to another port and leave the QR code
# pointing at nothing. Read from .env so the port map stays in one place.
MOBILE_METRO_PORT := $(shell sed -n 's/^MOBILE_METRO_PORT=//p' $(ENV_FILE) 2>/dev/null)
MOBILE_PORT := $(if $(MOBILE_METRO_PORT),$(MOBILE_METRO_PORT),8090)

MOBILE_CLIENT_METRO_PORT := $(shell sed -n 's/^MOBILE_CLIENT_METRO_PORT=//p' $(ENV_FILE) 2>/dev/null)
MOBILE_CLIENT_PORT := $(if $(MOBILE_CLIENT_METRO_PORT),$(MOBILE_CLIENT_METRO_PORT),8091)

mobile-ip: env ## Point MOBILE_API_BASE_URL + S3_ENDPOINT at this machine's LAN address (both apps)
	@$(SCRIPTS)/mobile-api-url.sh

mobile-deps: mobile-supplier-deps mobile-client-deps ## Install both mobile apps' dependencies

mobile-supplier-deps: ## Install the supplier app's dependencies (its own node_modules)
	@echo "==> vm-supplier-mobile-ui (separate from the npm workspaces — see its README)"
	@cd $(MOBILE_DIR) && npm install --no-audit --no-fund --silent

mobile-client-deps: ## Install the customer app's dependencies (its own node_modules)
	@echo "==> vm-client-mobile-ui (separate from the npm workspaces — see its README)"
	@cd $(MOBILE_CLIENT_DIR) && npm install --no-audit --no-fund --silent

mobile: env mobile-supplier-deps ## Start the SUPPLIER app's Expo dev server (needs `make dev`)
	@echo "==> Vayalavan Supplier — Expo on :$(MOBILE_PORT). Scan the QR code, or press a/i."
	@echo "    A handset cannot reach http://localhost — run 'make -C infra mobile-ip'"
	@echo "    to point MOBILE_API_BASE_URL and S3_ENDPOINT (product images are"
	@echo "    presigned against it) at this machine's LAN address first."
	@cd $(MOBILE_DIR) && npx expo start --port $(MOBILE_PORT)

mobile-client: env mobile-client-deps ## Start the CUSTOMER app's Expo dev server (needs `make dev`)
	@echo "==> Vayalavan — Expo on :$(MOBILE_CLIENT_PORT). Scan the QR code, or press a/i."
	@echo "    A handset cannot reach http://localhost — run 'make -C infra mobile-ip'"
	@echo "    to point MOBILE_API_BASE_URL and S3_ENDPOINT (product images are"
	@echo "    presigned against it) at this machine's LAN address first."
	@cd $(MOBILE_CLIENT_DIR) && npx expo start --port $(MOBILE_CLIENT_PORT)

## Escape hatch for a network that will not carry LAN traffic between the
## laptop and the phone — AP/client isolation, a guest SSID, a corporate VPN.
## Routes Metro through a public tunnel so the handset never needs to reach
## this machine directly.
##
## It only fixes METRO. The app itself still calls MOBILE_API_BASE_URL, which
## is a LAN address, so the bundle will load and every request will then fail.
## To use it end to end, expose the gateway too and point MOBILE_API_BASE_URL
## at that public URL.
mobile-tunnel: env mobile-supplier-deps ## Supplier app over a public tunnel (see the caveat above)
	@echo "==> Expo over a tunnel. Metro only — the API is still on the LAN."
	@cd $(MOBILE_DIR) && npx expo start --port $(MOBILE_PORT) --tunnel

mobile-client-tunnel: env mobile-client-deps ## Customer app over a public tunnel (same caveat)
	@echo "==> Expo over a tunnel. Metro only — the API is still on the LAN."
	@cd $(MOBILE_CLIENT_DIR) && npx expo start --port $(MOBILE_CLIENT_PORT) --tunnel

mobile-android: env mobile-supplier-deps ## Run the supplier app on a connected Android device or emulator
	@cd $(MOBILE_DIR) && npx expo run:android --port $(MOBILE_PORT)

mobile-ios: env mobile-supplier-deps ## Run the supplier app on an iOS simulator or device
	@cd $(MOBILE_DIR) && npx expo run:ios --port $(MOBILE_PORT)

mobile-client-android: env mobile-client-deps ## Run the customer app on a connected Android device or emulator
	@cd $(MOBILE_CLIENT_DIR) && npx expo run:android --port $(MOBILE_CLIENT_PORT)

mobile-client-ios: env mobile-client-deps ## Run the customer app on an iOS simulator or device
	@cd $(MOBILE_CLIENT_DIR) && npx expo run:ios --port $(MOBILE_CLIENT_PORT)

mobile-doctor: ## Check both mobile projects against their Expo SDK
	@cd $(MOBILE_DIR) && npx expo-doctor
	@cd $(MOBILE_CLIENT_DIR) && npx expo-doctor

mobile-test: ## Run both mobile apps' unit tests
	@cd $(MOBILE_DIR) && npm test --silent
	@cd $(MOBILE_CLIENT_DIR) && npm test --silent

mobile-lint: ## Typecheck both mobile apps
	@cd $(MOBILE_DIR) && npm run lint --silent
	@cd $(MOBILE_CLIENT_DIR) && npm run lint --silent

# ---------------------------------------------------------------------------
# Quality
# ---------------------------------------------------------------------------

test: test-go test-py test-js ## Run all tests

test-go: ## Run Go tests
	@cd $(ROOT_DIR)/packages/vm-go-common && go test ./...
	@for svc in $(GO_SERVICES) $(CONSUMER_SERVICE); do \
		(cd $(ROOT_DIR)/$$svc && go test ./...) || exit 1; \
	done

test-py: ## Run vm-analytics-api's tests
	@cd $(ROOT_DIR)/vm-analytics-api && uv run pytest -q

test-e2e: test-e2e-auth test-e2e-catalog test-e2e-availability test-e2e-cart test-e2e-close test-e2e-settlement test-e2e-customer test-e2e-rates test-e2e-refresh test-e2e-theft test-e2e-range test-e2e-processor test-e2e-commission test-e2e-markup ## All end-to-end checks against a running, seeded stack

test-e2e-auth: ## End-to-end auth checks against a running, seeded stack
	@python3 $(SCRIPTS)/e2e-auth.py

test-e2e-catalog: ## End-to-end catalog checks (products, images, CSV, admin)
	@python3 $(SCRIPTS)/e2e-catalog.py

test-e2e-availability: ## End-to-end daily availability and storefront checks
	@python3 $(SCRIPTS)/e2e-availability.py

test-e2e-cart: ## End-to-end cart and checkout, driven through the shared UI client
	@node $(SCRIPTS)/e2e-cart.mjs

test-e2e-close: ## End-to-end availability close/reopen (must not silently resell)
	@node $(SCRIPTS)/e2e-availability-close.mjs

test-e2e-settlement: ## End-to-end offline payment, payouts and supplier sales view
	@node $(SCRIPTS)/e2e-settlement.mjs

test-e2e-customer: ## End-to-end product detail, order history and profile
	@node $(SCRIPTS)/e2e-customer.mjs

test-e2e-rates: ## End-to-end commission, delivery margin and earnings breakdown
	@node $(SCRIPTS)/e2e-rates.mjs

test-e2e-refresh: ## End-to-end session survival across reloads
	@node $(SCRIPTS)/e2e-refresh.mjs

test-e2e-theft: ## End-to-end refresh-token theft detection
	@node $(SCRIPTS)/e2e-theft.mjs

test-e2e-range: ## End-to-end dashboard reporting periods
	@node $(SCRIPTS)/e2e-dashboard-range.mjs

test-e2e-processor: ## End-to-end 4pm auto-processing job
	@node $(SCRIPTS)/e2e-processor.mjs

test-e2e-commission: ## End-to-end per-supplier commission rates
	@node $(SCRIPTS)/e2e-commission.mjs

test-e2e-markup: ## End-to-end admin markup: pricing, payouts and earnings
	@node $(SCRIPTS)/e2e-markup.mjs

reset-orders: ## Delete all trading data, keep catalogue and suppliers
	@bash $(SCRIPTS)/reset-orders.sh

smoke: ## Read-only post-deploy checks. URL=https://api.example.com
	@bash $(SCRIPTS)/smoke.sh $${URL:-http://localhost:$${GATEWAY_PORT:-8080}}

loadcheck: ## Concurrent read check against the catalogue
	@node $(SCRIPTS)/loadcheck.mjs $${CONCURRENCY:-20} $${SECONDS:-10}

test-js: ## Run JS/TS tests
	@cd $(ROOT_DIR) && npm test --workspaces --if-present
	@$(MAKE) --no-print-directory mobile-test

lint: lint-go lint-js ## Lint everything

lint-go: ## gofmt check and go vet
	@echo "==> gofmt"
	@unformatted=$$(gofmt -l $(ROOT_DIR)/packages/vm-go-common $(addprefix $(ROOT_DIR)/,$(GO_SERVICES))); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "==> go vet"
	@cd $(ROOT_DIR)/packages/vm-go-common && go vet ./...
	@for svc in $(GO_SERVICES); do \
		(cd $(ROOT_DIR)/$$svc && go vet ./...) || exit 1; \
	done

lint-js: ## Typecheck every TypeScript package
	@cd $(ROOT_DIR) && npm run lint --workspaces --if-present
	@$(MAKE) --no-print-directory mobile-lint

fmt: ## Format Go code in place
	@gofmt -w $(ROOT_DIR)/packages/vm-go-common $(addprefix $(ROOT_DIR)/,$(GO_SERVICES))

# ---------------------------------------------------------------------------
# Teardown
# ---------------------------------------------------------------------------

## Destroys volumes and rebuilds. Deliberately does NOT run `deps` — the
## point is a fast database rebuild, not a full reinstall.
reset: ## Destroy volumes and rebuild the database from scratch
	@echo "==> tearing down (this deletes the database and all objects)"
	@$(COMPOSE) down -v --remove-orphans
	@$(MAKE) --no-print-directory up
	@$(MAKE) --no-print-directory migrate-up
	@$(MAKE) --no-print-directory seed
	@echo "==> reset complete"

clean: ## Remove build output and local tooling
	@rm -rf $(ROOT_DIR)/.tools $(ROOT_DIR)/packages/vm-ui-kit/dist
	@for d in vm-gateway-api vm-client-ui vm-admin-ui vm-analytics-ui vm-supplier-ui; do \
		rm -rf $(ROOT_DIR)/$$d/dist; done
	@rm -rf $(ROOT_DIR)/vm-analytics-api/.venv $(ROOT_DIR)/vm-events-consumer-api/tmp
	@# Metro's cache and the generated native projects. node_modules is left
	@# alone here, as it is for every other package.
	@rm -rf $(MOBILE_DIR)/.expo $(MOBILE_DIR)/android $(MOBILE_DIR)/ios
	@rm -rf $(MOBILE_CLIENT_DIR)/.expo $(MOBILE_CLIENT_DIR)/android $(MOBILE_CLIENT_DIR)/ios
	@for d in $(GO_SERVICES); do rm -rf $(ROOT_DIR)/$$d/tmp; done
	@echo "clean"
