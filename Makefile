VERSION = $(shell git rev-parse HEAD)
GIT_TAG = $(shell git rev-list --tags --max-count=1)
VERSION_TAG = $(if $(GIT_TAG),$(shell git describe --tags $(GIT_TAG)),v0)
CMD_DIR = "$(shell pwd)"/cmd/billpiggy
BIN_DIR = "$(shell pwd)"/bin
RELEASE_DIR = "$(shell pwd)"/release
TARGET_NAME = billpiggy
TARGET_OS = $(shell go env GOOS)
TARGET_ARCH = $(shell go env GOARCH)

build: generate
	@echo "Building for OS=$(TARGET_OS) Arch=$(TARGET_ARCH)"
	@echo "Version $(VERSION_TAG)-$(VERSION)"
	@mkdir -p $(BIN_DIR)
	GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -ldflags="-X 'github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler.BillPiggyVersion=$(VERSION)'" -o $(BIN_DIR)/$(TARGET_NAME) $(CMD_DIR)/main.go

test: generate
	@echo "Testing..."
	go test -race -coverprofile=coverage.out ./...

coverage: generate
	go test -race -coverprofile=coverage.out ./...

generate: generate-openapi
	go generate ./...

generate-openapi:
	go tool swag fmt
	go tool swag init --generalInfo cmd/billpiggy/main.go --parseInternal --output api --outputTypes yaml
	mv api/swagger.yaml api/openapi.yaml

helm-lint:
	helm lint charts/billpiggy --set image.repository=example.invalid/billpiggy

clean:
	@rm -rf ./bin
	@rm -rf ./release
