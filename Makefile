.PHONY: test build helm-lint

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -o bin/litellm-config-adapter ./cmd/litellm-config-adapter

helm-lint:
	helm lint charts/litellm-config-adapter
