.PHONY: help setup test test-integration vet build run docker-build smoke sqlc migrate-up migrate-down tidy

IMAGE ?= comms-openkit-channels:dev
# In sandboxed shells with no module proxy, prepend:
#   make ... GO="GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod go"
GO ?= go

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

setup: ## Interactive first-run setup (tenant + account + channel + link)
	@bash ../../deploy/setup/setup.sh

test: ## Run unit tests
	$(GO) test ./... -count=1

# The integration tests read FCC_TEST_DATABASE_URL — NOT DATABASE_URL — and
# t.Skip() when it is unset. Setting the wrong one skips every test and still
# prints "ok", which looks exactly like a passing run.
#
# Note the database name: openkit_TEST, not openkit. This suite is destructive —
# TestMigrationsDownIsClean runs `migrate down` and drops every table — so it
# must never point at a database holding data you want to keep. Compose creates
# openkit_test on first boot (deploy/postgres-init/).
FCC_TEST_DATABASE_URL ?= postgres://openkit:openkit@localhost:5434/openkit_test?sslmode=disable

test-integration: ## Run integration tests (needs Postgres + the migrate CLI)
	@command -v migrate >/dev/null || { \
		echo "ERROR: golang-migrate CLI not found in PATH."; \
		echo "       The schema tests skip silently without it, so this target"; \
		echo "       would report success while testing nothing."; \
		echo "       Install: brew install golang-migrate"; \
		exit 1; }
	@case "$(FCC_TEST_DATABASE_URL)" in \
		*/openkit\?*|*/openkit) \
			echo "REFUSING: FCC_TEST_DATABASE_URL points at the 'openkit' database."; \
			echo "          This suite drops every table. Use openkit_test, or pass"; \
			echo "          an explicit throwaway DSN if you really mean it."; \
			exit 1;; \
	esac
	@echo "==> FCC_TEST_DATABASE_URL=$(FCC_TEST_DATABASE_URL)"
	@echo "==> this suite DROPS EVERY TABLE in that database"
	@echo "==> running verbosely on purpose: these tests t.Skip() when the DB is"
	@echo "    unreachable, and a silent skip is indistinguishable from a pass."
	FCC_TEST_DATABASE_URL="$(FCC_TEST_DATABASE_URL)" \
		$(GO) test ./tests/integration/... ./internal/db/... \
		-count=1 -tags=integration -p 1 -v

vet: ## go vet
	$(GO) vet ./...

build: ## Build the server binary into ./bin/server
	@mkdir -p bin
	$(GO) build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

run: ## Run the server locally (PORT defaults to 9090)
	$(GO) run ./cmd/server

sqlc: ## Regenerate type-safe query code
	sqlc generate

migrate-up: ## Apply migrations (DATABASE_URL required)
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down: ## Roll back one migration (DATABASE_URL required)
	migrate -path migrations -database "$(DATABASE_URL)" down 1

tidy: ## go mod tidy
	$(GO) mod tidy

docker-build: ## Build the deploy image (tag $(IMAGE))
	docker build -f deploy/Dockerfile -t $(IMAGE) .

smoke: docker-build ## Build image, boot container, curl /healthz
	@CID=$$(docker run -d --rm -p 19090:9090 \
		-e PORT=9090 \
		-e DATABASE_URL=postgres://u:p@localhost:5432/openkit \
		-e INTERNAL_API_TOKEN=dev-token \
		-e CREDENTIALS_ENCRYPTION=localaes \
		-e LOCAL_AES_KEY=$$(printf '0%.0s' {1..32} | base64) \
		$(IMAGE)) && \
		sleep 1 && \
		curl -fsS http://localhost:19090/healthz && echo && \
		docker stop $$CID
