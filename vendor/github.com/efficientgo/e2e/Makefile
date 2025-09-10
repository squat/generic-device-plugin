include .bingo/Variables.mk

MODULES ?= $(shell find $(PWD) -name "go.mod" | grep -v ".bingo" | xargs -I {} dirname {})

GO111MODULE       ?= on
export GO111MODULE

GOBIN ?= $(firstword $(subst :, ,${GOPATH}))/bin

# Tools.
GIT ?= $(shell which git)

# Support gsed on OSX (installed via brew), falling back to sed. On Linux
# systems gsed won't be installed, so will use sed as expected.
SED ?= $(shell which gsed 2>/dev/null || which sed)
# Same as above, but for xargs.
XARGS ?= $(shell which gxargs 2>/dev/null || which xargs)

define require_clean_work_tree
	@git update-index -q --ignore-submodules --refresh

    @if ! git diff-files --quiet --ignore-submodules --; then \
        echo >&2 "$1: you have unstaged changes."; \
        git diff-files --name-status -r --ignore-submodules -- >&2; \
        echo >&2 "Please commit or stash them."; \
        exit 1; \
    fi

    @if ! git diff-index --cached --quiet HEAD --ignore-submodules --; then \
        echo >&2 "$1: your index contains uncommitted changes."; \
        git diff-index --cached --name-status -r --ignore-submodules HEAD -- >&2; \
        echo >&2 "Please commit or stash them."; \
        exit 1; \
    fi

endef

help: ## Displays help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: all
all: format build

.PHONY: build
build: ## Build all modules
	@echo ">> building all modules: $(MODULES)"
	for dir in $(MODULES) ; do \
  		echo ">> building in $${dir}"; \
		cd $${dir} && go test -run=nope ./...; \
	done

.PHONY: deps
deps: ## Cleans up deps for all modules
	@echo ">> running deps tidy for all modules: $(MODULES)"
	for dir in $(MODULES) ; do \
		cd $${dir} && go mod tidy; \
	done

define MDOX_VALIDATORS
validators:
- regex: '^(http://.*|https://.*)'
  type: ignore
endef

export MDOX_VALIDATORS
.PHONY: docs
docs: $(MDOX) ## Generates config snippets and doc formatting.
	@echo ">> generating docs $(PATH)"
	@$(MDOX) fmt -l --links.validate.config="$$MDOX_VALIDATORS" *.md

.PHONY: format
format: ## Formats Go code.
format: $(GOIMPORTS)
	@echo ">> formatting  all modules Go code: $(MODULES)"
	@$(GOIMPORTS) -w $(MODULES)

.PHONY: test
test: ## Runs all Go unit tests.
	@echo ">> running tests for all modules: $(MODULES)"
	for dir in $(MODULES) ; do \
		cd $${dir} && go test -v -race $(go list ./... | grep -v examples/exampleapp); \
	done

.PHONY: run-example
run-example: ## Runs our standalone Thanos example using e2e.
	@echo ">> running example"
	cd examples/thanos && go run .

.PHONY: run-example-app
run-example-app:
	@echo ">> running example application"
	cd examples/exampleapp && go test -v -count=1 .

.PHONY: check-git
check-git:
ifneq ($(GIT),)
	@test -x $(GIT) || (echo >&2 "No git executable binary found at $(GIT)."; exit 1)
else
	@echo >&2 "No git binary found."; exit 1
endif

# PROTIP:
# Add
#      --cpu-profile-path string   Path to CPU profile output file
#      --mem-profile-path string   Path to memory profile output file
# to debug big allocations during linting.
lint: ## Runs various static analysis against our code.
lint: $(FAILLINT) $(GOLANGCI_LINT) $(MISSPELL) $(COPYRIGHT) build format docs check-git deps
	$(call require_clean_work_tree,"detected not clean master before running lint - run make lint and commit changes.")
	@echo ">> verifying imported "
	@for dir in $(MODULES) ; do \
		cd $${dir} && $(FAILLINT) -paths "fmt.{Print,PrintfPrintln}" -ignore-tests ./... && \
		$(FAILLINT) -paths "github.com/stretchr/testify=github.com/efficientgo/core/testutil,fmt.{Errorf}=github.com/efficientgo/core/errors" ./...; \
	done
	@echo ">> examining all of the Go files"
	@for dir in $(MODULES) ; do \
		cd $${dir} && go vet -stdmethods=false ./...; \
	done
	@echo ">> linting all of the Go files GOGC=${GOGC}"
	@for dir in $(MODULES) ; do \
		cd $${dir} && $(GOLANGCI_LINT) run; \
	done
	@echo ">> detecting misspells"
	@find . -type f | grep -v vendor/ | grep -vE '\./\..*' | $(XARGS) $(MISSPELL) -error
	@echo ">> ensuring Copyright headers"
	@$(COPYRIGHT) $(shell go list -f "{{.Dir}}" ./... | $(XARGS) -i find "{}" -name "*.go")
	$(call require_clean_work_tree,"detected files without copyright - run make lint and commit changes.")
