VERSION ?= dev
REVISION ?= $(shell git rev-parse --verify HEAD 2>/dev/null || echo unknown)
TARGET ?= $(shell go env GOOS)/$(shell go env GOARCH)
TARGETS := darwin/arm64 darwin/amd64 linux/amd64 linux/386 windows/amd64 windows/386
DIST_DIR ?= dist
TARGET_OS = $(word 1,$(subst /, ,$(TARGET)))
TARGET_ARCH = $(word 2,$(subst /, ,$(TARGET)))
EXT = $(if $(filter windows,$(TARGET_OS)),.exe,)
OUTPUT = bin/$(TARGET_OS)-$(TARGET_ARCH)/datalens$(EXT)
LDFLAGS = -X github.com/datalens-tech/datalens-cli/internal/buildinfo.Version=$(VERSION) -X github.com/datalens-tech/datalens-cli/internal/buildinfo.Revision=$(REVISION)

.PHONY: build test check build-all dist verify-dist

build: export OUTPUT := $(OUTPUT)
build:
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) go build -trimpath -ldflags '$(LDFLAGS)' -o "$$OUTPUT" ./cmd/datalens

test:
	go test -race ./...

check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...

build-all:
	@set -e; for target in $(TARGETS); do $(MAKE) build TARGET=$$target; done

dist: export MAKE := $(MAKE)
dist verify-dist: export VERSION := $(VERSION)
dist verify-dist: export REVISION := $(REVISION)
dist verify-dist: export DIST_DIR := $(DIST_DIR)
dist:
	python3 scripts/dist.py $(TARGETS)

verify-dist:
	python3 -B scripts/verify_dist.py $(TARGETS)
