.PHONY: help dev build build-image web tidy test test-race lint fmt clean

IMAGE ?= etcd-ui:dev

help:
	@echo "Targets:"
	@echo "  make dev          run all services + web dev server"
	@echo "  make build        build all Go binaries into ./bin"
	@echo "  make web          build the SPA into web/dist"
	@echo "  make build-image  docker build -t $(IMAGE) ."
	@echo "  make tidy         go mod tidy"
	@echo "  make test         go test ./... && (cd web && npm test --silent)"
	@echo "  make test-race    go test -race ./... (slower; catches data races)"
	@echo "  make lint         golangci-lint run ./..."

dev:
	@bash -c 'set -m; \
	  go run ./cmd/audit & \
	  go run ./cmd/cluster & \
	  go run ./cmd/kv & \
	  go run ./cmd/ops & \
	  go run ./cmd/gateway & \
	  (cd web && npm run dev) ; \
	  wait'

build:
	@mkdir -p bin
	@for svc in supervisor gateway cluster kv ops audit; do \
	  echo ">> building $$svc"; \
	  CGO_ENABLED=0 go build -ldflags='-s -w' -o bin/$$svc ./cmd/$$svc ; \
	done

web:
	cd web && npm install --legacy-peer-deps && npm run build

build-image:
	docker build -t $(IMAGE) .

tidy:
	go mod tidy

test:
	go test ./...
	cd web && npm test --silent

# Race detector: required for CI on every PR. CGO_ENABLED=1 is mandatory.
test-race:
	CGO_ENABLED=1 go test -race -count=1 -timeout 120s ./...

# Static analysis. Install once: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin web/dist web/node_modules
