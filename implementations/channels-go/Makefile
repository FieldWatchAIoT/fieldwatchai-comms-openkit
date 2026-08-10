.PHONY: help test test-integration vet build run docker-build smoke sqlc migrate-up migrate-down tidy

IMAGE ?= comms-openkit-channels:dev
# In sandboxed shells with no module proxy, prepend:
#   make ... GO="GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod go"
GO ?= go

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

test: ## Run unit tests
	$(GO) test ./... -count=1

test-integration: ## Run integration tests (needs Postgres; see docker-compose)
	$(GO) test ./... -count=1 -tags=integration -p 1

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
