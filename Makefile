VERSION = $(shell git rev-parse HEAD)
GIT_TAG = $(shell git rev-list --tags --max-count=1)
VERSION_TAG = $(if $(GIT_TAG),$(shell git describe --tags $(GIT_TAG)),v0)
CMD_DIR = "$(shell pwd)"/cmd/billpiggy
BIN_DIR = "$(shell pwd)"/bin
RELEASE_DIR = "$(shell pwd)"/release
TARGET_NAME = billpiggy
TARGET_OS = $(shell go env GOOS)
TARGET_ARCH = $(shell go env GOARCH)

build: gen
	@echo "Building for OS=$(TARGET_OS) Arch=$(TARGET_ARCH)"
	@echo "Version $(VERSION_TAG)-$(VERSION)"
	@mkdir -p $(BIN_DIR)
	GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -ldflags="-X 'github.com/ownerofglory/billpiggy/internal/handler.BillPiggyVersion=$(VERSION_TAG)'" -o $(BIN_DIR)/$(TARGET_NAME) $(CMD_DIR)/main.go

test: gen
	@echo "Testing..."
	go test -race -coverprofile=coverage.out ./...

gen:
	@echo "Generating code"
	go generate ./...

clean:
	@rm -rf ./bin
	@rm -rf ./release