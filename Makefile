VERSION = $(shell git rev-parse HEAD 2>/dev/null || echo dev)
GIT_TAG = $(shell git rev-list --tags --max-count=1 2>/dev/null)
VERSION_TAG = $(if $(GIT_TAG),$(shell git describe --tags $(GIT_TAG)),v0)
BUILD_VERSION = $(if $(GIT_TAG),$(VERSION_TAG),$(VERSION))
LDFLAGS = -X github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler.BillPiggyVersion=$(BUILD_VERSION)
BIN_DIR ?= bin
TARGET_NAME ?= billpiggy
HELM_IMAGE_REPOSITORY ?= example.invalid/billpiggy

.PHONY: help run test coverage fmt vet build check generate generate-openapi helm-lint helm-template clean

help:
	@echo "Targets: run test coverage fmt vet build check generate generate-openapi helm-lint helm-template clean"
	@echo "Build version: $(BUILD_VERSION)"

run:
	go run ./cmd/billpiggy

test:
	go test ./...

coverage:
	go test -race -coverprofile=coverage.out ./...

fmt:
	gofmt -w $$(rg --files -g '*.go')

vet:
	go vet ./...

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(TARGET_NAME) ./cmd/billpiggy

check: fmt vet test build helm-lint helm-template

generate: generate-openapi
	go generate ./...

generate-openapi:
	go tool swag fmt
	go tool swag init --generalInfo cmd/billpiggy/main.go --parseInternal --output api --outputTypes yaml
	mv api/swagger.yaml api/openapi.yaml

helm-lint:
	helm lint charts/billpiggy --set image.repository=$(HELM_IMAGE_REPOSITORY)

helm-template:
	helm template billpiggy charts/billpiggy --set image.repository=$(HELM_IMAGE_REPOSITORY)

clean:
	@rm -rf $(BIN_DIR) release coverage.out
