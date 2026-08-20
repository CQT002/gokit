# gokit — multi-module. Mọi target chạy vòng qua từng module vì mỗi module
# có go.mod riêng: `go test ./...` ở gốc repo KHÔNG chạm tới module con.

SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

MODULES := core obs httpx db cache kafka testx examples
GOLANGCI_VERSION := v2.13.0
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null)
ifeq ($(GOLANGCI),)
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
endif

.PHONY: help
help: ## Liệt kê target
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: test
test: ## Unit test + race, mọi module (không cần Docker)
	@set -e; for m in $(MODULES); do \
		echo "==> test $$m"; (cd $$m && go test -race -count=1 ./...); \
	done

.PHONY: test-integration
test-integration: ## Test integration (cần Docker) — build tag `integration`
	@set -e; for m in $(MODULES); do \
		echo "==> integration $$m"; (cd $$m && go test -race -count=1 -tags=integration ./...); \
	done

.PHONY: cover
cover: ## Coverage từng module, in tổng %
	@set -e; for m in $(MODULES); do \
		echo "==> cover $$m"; \
		(cd $$m && go test -count=1 -covermode=atomic -coverprofile=coverage.out ./... \
			&& go tool cover -func=coverage.out | tail -1); \
	done

.PHONY: lint
lint: ## golangci-lint mọi module
	@set -e; for m in $(MODULES); do \
		echo "==> lint $$m"; (cd $$m && $(GOLANGCI) run --config=../.golangci.yml ./...); \
	done

.PHONY: fmt
fmt: ## gofmt + tổ chức lại import
	@set -e; for m in $(MODULES); do \
		(cd $$m && $(GOLANGCI) fmt --config=../.golangci.yml ./...); \
	done

.PHONY: vet
vet: ## go vet mọi module
	@set -e; for m in $(MODULES); do \
		echo "==> vet $$m"; (cd $$m && go vet ./...); \
	done

.PHONY: tidy
tidy: ## go mod tidy mọi module
	@set -e; for m in $(MODULES); do \
		echo "==> tidy $$m"; (cd $$m && go mod tidy); \
	done

.PHONY: tidy-check
tidy-check: ## Fail nếu go.mod/go.sum chưa tidy (dùng trong CI)
	@set -e; for m in $(MODULES); do \
		(cd $$m && cp go.mod go.mod.bak && { [ -f go.sum ] && cp go.sum go.sum.bak || true; } \
			&& go mod tidy \
			&& { diff -q go.mod go.mod.bak >/dev/null || { echo "$$m: go.mod chưa tidy"; exit 1; }; } \
			&& { [ ! -f go.sum.bak ] || diff -q go.sum go.sum.bak >/dev/null || { echo "$$m: go.sum chưa tidy"; exit 1; }; } \
			&& mv go.mod.bak go.mod && { [ -f go.sum.bak ] && mv go.sum.bak go.sum || true; }); \
	done
	@echo "OK: mọi module đã tidy"

.PHONY: build-nowork
build-nowork: ## Build với GOWORK=off — bắt lỗi require/version mà go.work che mất
	@set -e; for m in $(MODULES); do \
		echo "==> build (GOWORK=off) $$m"; (cd $$m && GOWORK=off go build -o /dev/null ./...); \
	done

.PHONY: check-module-path
check-module-path: ## Chặn module path viết hoa (CQT002)
	@./scripts/check-module-path.sh

.PHONY: check
check: check-module-path vet lint test ## Toàn bộ kiểm tra không cần Docker

.PHONY: clean
clean: ## Dọn output test
	@rm -rf bin; find . -name coverage.out -delete
