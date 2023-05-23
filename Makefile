export GO111MODULE=on
.PHONY: push container clean container-name container-latest push-latest fmt lint test unit vendor header client manifest manfest-latest manifest-annotate manifest manfest-latest manifest-annotate

ARCH ?= amd64
ALL_ARCH := amd64 arm arm64
DOCKER_ARCH := "amd64" "arm v7" "arm64 v8"
BINS := $(addprefix bin/$(ARCH)/,generic-device-plugin)
PROJECT := generic-device-plugin
PKG := github.com/squat/$(PROJECT)
REGISTRY ?= index.docker.io
IMAGE ?= $(REGISTRY)/squat/$(PROJECT)

TAG := $(shell git describe --abbrev=0 --tags HEAD 2>/dev/null)
COMMIT := $(shell git rev-parse HEAD)
VERSION := $(COMMIT)
ifneq ($(TAG),)
    ifeq ($(COMMIT), $(shell git rev-list -n1 $(TAG)))
        VERSION := $(TAG)
    endif
endif
DIRTY := $(shell test -z "$$(git diff --shortstat 2>/dev/null)" || echo -dirty)
VERSION := $(VERSION)$(DIRTY)
LD_FLAGS := -ldflags "-X $(PKG)/version.Version=$(VERSION) -extldflags -static"
SRC := $(shell find . -type f -name '*.go' -not -path "./vendor/*")
GO_FILES ?= $$(find . -name '*.go' -not -path './vendor/*')
GO_PKGS ?= $$(go list ./... | grep -v "$(PKG)/vendor")

STATICCHECK_BINARY := bin/staticcheck
EMBEDMD_BINARY := bin/embedmd

BUILD_IMAGE ?= golang:1.19.7-alpine

build: $(BINS)

build-%:
	@$(MAKE) --no-print-directory ARCH=$* build

container-latest-%:
	@$(MAKE) --no-print-directory ARCH=$* container-latest

container-%:
	@$(MAKE) --no-print-directory ARCH=$* container

push-latest-%:
	@$(MAKE) --no-print-directory ARCH=$* push-latest

push-%:
	@$(MAKE) --no-print-directory ARCH=$* push

all-build: $(addprefix build-, $(ALL_ARCH))

all-container: $(addprefix container-, $(ALL_ARCH))

all-push: $(addprefix push-, $(ALL_ARCH))

all-container-latest: $(addprefix container-latest-, $(ALL_ARCH))

all-push-latest: $(addprefix push-latest-, $(ALL_ARCH))

CONTAINERIZE_BUILD ?= true
BUILD_PREFIX :=
BUILD_SUFIX :=
ifeq ($(CONTAINERIZE_BUILD), true)
	BUILD_PREFIX := docker run --rm \
	    -u $$(id -u):$$(id -g) \
	    -v $$(pwd):/src \
	    -w /src \
	    --entrypoint '' \
	    $(BUILD_IMAGE) \
	    /bin/sh -c ' \
	        GOCACHE=$$(pwd)/.cache
	BUILD_SUFIX := '
endif

$(BINS): $(SRC) go.mod
	@mkdir -p bin/$(ARCH)
	@echo "building: $@"
	@$(BUILD_PREFIX) \
	        GOARCH=$(ARCH) \
	        GOOS=linux \
		CGO_ENABLED=0 \
		go build -o $@ \
		    $(LD_FLAGS) \
		    . \
	$(BUILD_SUFIX)

fmt:
	@echo $(GO_PKGS)
	gofmt -w -s $(GO_FILES)

lint: header $(STATICCHECK_BINARY)
	@echo 'go vet $(GO_PKGS)'
	@vet_res=$$(GO111MODULE=on go vet $(GO_PKGS) 2>&1); if [ -n "$$vet_res" ]; then \
		echo ""; \
		echo "Go vet found issues. Please check the reported issues"; \
		echo "and fix them if necessary before submitting the code for review:"; \
		echo "$$vet_res"; \
		exit 1; \
	fi
	@echo '$(STATICCHECK_BINARY) $(GO_PKGS)'
	@lint_res=$$($(STATICCHECK_BINARY) $(GO_PKGS)); if [ -n "$$lint_res" ]; then \
		echo ""; \
		echo "Staticcheck found style issues. Please check the reported issues"; \
		echo "and fix them if necessary before submitting the code for review:"; \
		echo "$$lint_res"; \
		exit 1; \
	fi
	@echo 'gofmt -d -s $(GO_FILES)'
	@fmt_res=$$(gofmt -d -s $(GO_FILES)); if [ -n "$$fmt_res" ]; then \
		echo ""; \
		echo "Gofmt found style issues. Please check the reported issues"; \
		echo "and fix them if necessary before submitting the code for review:"; \
		echo "$$fmt_res"; \
		exit 1; \
	fi

unit:
	go test --race ./...

test: lint unit

header: .header
	@HEADER=$$(cat .header); \
	HEADER_LEN=$$(wc -l .header | awk '{print $$1}'); \
	FILES=; \
	for f in $(GO_FILES); do \
		for i in 0 1 2 3 4 5; do \
			FILE=$$(tail -n +$$i $$f | head -n $$HEADER_LEN | sed "s/[0-9]\{4\}/YEAR/"); \
			[ "$$FILE" = "$$HEADER" ] && continue 2; \
		done; \
		FILES="$$FILES$$f "; \
	done; \
	if [ -n "$$FILES" ]; then \
		printf 'the following files are missing the license header: %s\n' "$$FILES"; \
		exit 1; \
	fi

container: .container-$(ARCH)-$(VERSION) container-name
.container-$(ARCH)-$(VERSION): $(BINS) Dockerfile
	@i=0; for a in $(ALL_ARCH); do [ "$$a" = $(ARCH) ] && break; i=$$((i+1)); done; \
	ia=""; iv=""; \
	j=0; for a in $(DOCKER_ARCH); do \
	    [ "$$i" -eq "$$j" ] && ia=$$(echo "$$a" | awk '{print $$1}') && iv=$$(echo "$$a" | awk '{print $$2}') && break; j=$$((j+1)); \
	done; \
	docker build -t $(IMAGE):$(ARCH)-$(VERSION) --build-arg GOARCH=$(ARCH) .
	@docker images -q $(IMAGE):$(ARCH)-$(VERSION) > $@

container-latest: .container-$(ARCH)-$(VERSION)
	@docker tag $(IMAGE):$(ARCH)-$(VERSION) $(IMAGE):$(ARCH)-latest
	@echo "container: $(IMAGE):$(ARCH)-latest"

container-name:
	@echo "container: $(IMAGE):$(ARCH)-$(VERSION)"

manifest: .manifest-$(VERSION) manifest-name
.manifest-$(VERSION): Dockerfile $(addprefix push-, $(ALL_ARCH))
	@docker manifest create --amend $(IMAGE):$(VERSION) $(addsuffix -$(VERSION), $(addprefix $(IMAGE):, $(ALL_ARCH)))
	@$(MAKE) --no-print-directory manifest-annotate-$(VERSION)
	@docker manifest push $(IMAGE):$(VERSION) > $@

manifest-latest: Dockerfile $(addprefix push-latest-, $(ALL_ARCH))
	@docker manifest create --amend $(IMAGE):latest $(addsuffix -latest, $(addprefix $(IMAGE):, $(ALL_ARCH)))
	@$(MAKE) --no-print-directory manifest-annotate-latest
	@docker manifest push $(IMAGE):latest
	@echo "manifest: $(IMAGE):latest"

manifest-annotate: manifest-annotate-$(VERSION)

manifest-annotate-%:
	@i=0; \
	for a in $(ALL_ARCH); do \
	    annotate=; \
	    j=0; for da in $(DOCKER_ARCH); do \
		if [ "$$j" -eq "$$i" ] && [ -n "$$da" ]; then \
		    annotate="docker manifest annotate $(IMAGE):$* $(IMAGE):$$a-$* --os linux --arch"; \
		    k=0; for ea in $$da; do \
			[ "$$k" = 0 ] && annotate="$$annotate $$ea"; \
			[ "$$k" != 0 ] && annotate="$$annotate --variant $$ea"; \
			k=$$((k+1)); \
		    done; \
		    $$annotate; \
		fi; \
		j=$$((j+1)); \
	    done; \
	    i=$$((i+1)); \
	done

manifest-name:
	@echo "manifest: $(IMAGE):$(VERSION)"

push: .push-$(ARCH)-$(VERSION) push-name
.push-$(ARCH)-$(VERSION): .container-$(ARCH)-$(VERSION)
	@docker push $(IMAGE):$(ARCH)-$(VERSION)
	@docker images -q $(IMAGE):$(ARCH)-$(VERSION) > $@

push-latest: container-latest
	@docker push $(IMAGE):$(ARCH)-latest
	@echo "pushed: $(IMAGE):$(ARCH)-latest"

push-name:
	@echo "pushed: $(IMAGE):$(ARCH)-$(VERSION)"

clean: container-clean bin-clean
	rm -rf .cache

container-clean:
	rm -rf .container-* .manifest-* .push-*

bin-clean:
	rm -rf bin

vendor:
	go mod tidy
	go mod vendor

tmp/help.txt: bin/$(ARCH)/generic-device-plugin
	mkdir -p tmp
	bin/$(ARCH)/generic-device-plugin --help 2>&1 | head -n -1 > $@

README.md: $(EMBEDMD_BINARY) tmp/help.txt
	$(EMBEDMD_BINARY) -w $@

$(STATICCHECK_BINARY):
	go build -o $@ honnef.co/go/tools/cmd/staticcheck

$(EMBEDMD_BINARY):
	go build -o $@ github.com/campoy/embedmd
