SERVERS = mcp-tg mcp-jira mcp-gitlab mcp-confluence
BUILD_TARGETS = $(SERVERS:%=build-%)
INSTALL_TARGETS = $(SERVERS:%=install-%)
INSTALL_DIR = $(shell go env GOPATH)/bin

.PHONY: generate lint test build install $(BUILD_TARGETS) $(INSTALL_TARGETS)

generate:
	go generate ./...

lint:
	go tool golangci-lint run ./...

test:
	go test ./...

build: $(BUILD_TARGETS)

$(BUILD_TARGETS): build-%:
	go build -o bin/$* ./$*/cmd/$*

install: $(INSTALL_TARGETS)

$(INSTALL_TARGETS): install-%:
	test -z "$$(git status --porcelain)" || { echo "рабочее дерево не чистое: install только из закоммиченного состояния" >&2; exit 1; }
	go mod verify
	GOBIN="$(INSTALL_DIR)" GOFLAGS=-mod=readonly go install ./$*/cmd/$*
	codesign -s - -f -o runtime "$(INSTALL_DIR)/$*"
