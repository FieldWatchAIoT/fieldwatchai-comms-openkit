.PHONY: help test vet build run docker-build smoke

IMAGE ?= comms-webhook:dev

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

test: ## go test ./...
	go test ./... -count=1

vet: ## go vet ./...
	go vet ./...

build: ## go build the server binary into ./bin/server
	@mkdir -p bin
	go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

run: ## run the server locally (PORT defaults to 8080)
	go run ./cmd/server

docker-build: ## docker build the deploy image (tag $(IMAGE))
	docker build -f deploy/Dockerfile -t $(IMAGE) .

smoke: docker-build ## run the container and curl /healthz
	@CID=$$(docker run -d --rm -p 18080:8080 $(IMAGE)) && \
		sleep 1 && \
		curl -fsS http://localhost:18080/healthz && echo && \
		docker stop $$CID
