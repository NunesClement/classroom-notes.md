GO ?= go
GOFMT ?= gofmt
VERSION ?= dev
IMAGE ?= ghcr.io/clementnunes/sage-resilient-urgent-scheduler
BIN_DIR ?= bin
BIN_DIR_ABS := $(abspath $(BIN_DIR))

.PHONY: all build build-policyctl build-chaoslab build-sage test test-core \
	test-sage test-race vet fmt-check validate-config example chaos-example \
	docker-build ci clean

all: ci

build: build-policyctl build-chaoslab build-sage

build-policyctl:
	mkdir -p "$(BIN_DIR_ABS)"
	$(GO) build -trimpath -o "$(BIN_DIR_ABS)/policyctl" ./cmd/policyctl

build-chaoslab:
	mkdir -p "$(BIN_DIR_ABS)"
	$(GO) build -trimpath -o "$(BIN_DIR_ABS)/chaoslab" ./cmd/chaoslab

build-sage:
	mkdir -p "$(BIN_DIR_ABS)"
	cd integrations/sage && $(GO) build -trimpath \
		-ldflags "-X main.Version=$(VERSION)" \
		-o "$(BIN_DIR_ABS)/sage-nodescheduler" ./cmd/waggle-nodescheduler

test: test-core test-sage

test-core:
	$(GO) test ./...

test-sage:
	cd integrations/sage && $(GO) test ./...

test-race:
	$(GO) test -race ./...
	cd integrations/sage && $(GO) test -race ./...

vet:
	$(GO) vet ./...
	cd integrations/sage && $(GO) vet ./...

fmt-check:
	@set -eu; \
	files="$$(find cmd pkg integrations/sage -name '*.go' -type f)"; \
	unformatted="$$($(GOFMT) -l $$files)"; \
	test -z "$$unformatted" \
		|| (echo "Go files need gofmt"; echo "$$unformatted"; exit 1)

validate-config:
	$(GO) run ./cmd/policyctl \
		--config config/policy.example.yaml \
		--validate-config
	cd integrations/sage && $(GO) run ./cmd/waggle-nodescheduler \
		--policy-config ../../config/policy.example.yaml \
		--validate-config

example:
	$(GO) run ./cmd/policyctl \
		--config config/policy.example.yaml \
		--snapshot examples/snapshots/urgent-vs-routine.json

chaos-example:
	$(GO) run ./cmd/chaoslab \
		--config config/policy.example.yaml \
		--experiment examples/chaos/sensitivity.json

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--tag $(IMAGE):$(VERSION) \
		.

ci: fmt-check test vet build validate-config example chaos-example

clean:
	@case "$(BIN_DIR_ABS)" in \
		"$(CURDIR)"/*) ;; \
		*) echo "refusing to clean BIN_DIR outside the repository: $(BIN_DIR_ABS)"; exit 1 ;; \
	esac
	rm -f -- \
		"$(BIN_DIR_ABS)/policyctl" \
		"$(BIN_DIR_ABS)/chaoslab" \
		"$(BIN_DIR_ABS)/sage-nodescheduler"
	-rmdir -- "$(BIN_DIR_ABS)"
