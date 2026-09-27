MODULES := . adapters/postgres adapters/couchbase
LINT    := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

.PHONY: help tidy fmt vet lint test test-short test-race cover vuln scaffold-demo

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "%-14s %s\n", $$1, $$2}'

tidy: ## go mod tidy in every module
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

fmt: ## Format code
	gofmt -s -w .

vet: ## go vet in every module
	@for m in $(MODULES); do (cd $$m && go vet ./...) || exit 1; done

lint: ## golangci-lint in every module
	@for m in $(MODULES); do (cd $$m && $(LINT) run ./...) || exit 1; done

test: ## Tests in every module (includes the scaffold end-to-end test)
	@for m in $(MODULES); do (cd $$m && go test ./...) || exit 1; done

test-short: ## Fast tests (skips the scaffold end-to-end test)
	@for m in $(MODULES); do (cd $$m && go test -short ./...) || exit 1; done

test-race: ## Tests with race detector (needs cgo)
	@for m in $(MODULES); do (cd $$m && go test -race ./...) || exit 1; done

cover: ## Coverage of the core module
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

vuln: ## govulncheck
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

scaffold-demo: ## Generate ./tmp/demo-service wired to this checkout
	go run ./cmd/gocore new github.com/example/demo-service -dir tmp/demo-service -core-replace . -force
