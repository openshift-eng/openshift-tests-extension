GO_PKG_NAME := github.com/openshift-eng/openshift-tests-extension

GIT_COMMIT := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_TREE_STATE := $(shell if git diff --quiet; then echo clean; else echo dirty; fi)

LDFLAGS := -X '$(GO_PKG_NAME)/pkg/version.CommitFromGit=$(GIT_COMMIT)' \
           -X '$(GO_PKG_NAME)/pkg/version.BuildDate=$(BUILD_DATE)' \
           -X '$(GO_PKG_NAME)/pkg/version.GitTreeState=$(GIT_TREE_STATE)'

.PHONY: verify test lint clean unit integration example-tests framework-tests

all: unit build integration

verify: lint

build: example-tests framework-tests

example-tests:
	# GO_COMPLIANCE_POLICY="exempt_all" must only be used for test related binaries.
	# It prevents various FIPS compliance policies from being applied to this compilation.
	# Do not set globally.
	GO_COMPLIANCE_POLICY="exempt_all" go build -ldflags "$(LDFLAGS)" ./cmd/example-tests/...

framework-tests:
	# GO_COMPLIANCE_POLICY="exempt_all" must only be used for test related binaries.
	# It prevents various FIPS compliance policies from being applied to this compilation.
	# Do not set globally.
	GO_COMPLIANCE_POLICY="exempt_all" go build -ldflags "$(LDFLAGS)" ./cmd/framework-tests/...

test: unit integration

unit:
	go test ./...

integration: build
ifeq ($(OPENSHIFT_CI), true)
	$(eval TIMESTAMP := $(shell date +%Y%m%d-%H%M%S))
	./framework-tests run-suite openshift-tests-extension/framework --junit-path $(ARTIFACT_DIR)/junit_$(TIMESTAMP).xml --html-path $(ARTIFACT_DIR)/results-summary-$(TIMESTAMP).html
else
	./framework-tests run-suite openshift-tests-extension/framework
endif

lint:
	./hack/go-lint.sh run ./...

clean:
	rm -f example-tests framework-tests
