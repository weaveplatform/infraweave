SHELL := /bin/bash
.DEFAULT_GOAL := help

# Built outside any parent go.work: local runs match CI.
export GOWORK := off

GO ?= go
GOLANGCI_LINT ?= golangci-lint
MODULE := $(shell GOWORK=off $(GO) list -m)
COVER_DIR := cover
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(MODULE)/internal/platform/buildinfo.version=$(VERSION) -X $(MODULE)/internal/platform/buildinfo.commit=$(COMMIT) -X $(MODULE)/internal/platform/buildinfo.buildDate=$(BUILD_DATE)
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'

## init: rename the module after creating a repository from the template (make init NAME=<repo>)
init:
	@test -n "$(NAME)" || { echo "usage: make init NAME=<repository name>"; exit 1; }
	@grep -rl --exclude-dir=.git infraweave . | xargs sed -i.bak 's/infraweave/$(NAME)/g'
	@find . -name '*.bak' -not -path './.git/*' -delete
	$(GO) mod tidy
	@echo "renamed to github.com/weaveplatform/$(NAME); review README.md and CODEOWNERS"

## fmt: apply the formatters configured in .golangci.yml
fmt:
	$(GOLANGCI_LINT) fmt --config .golangci.yml ./...

## lint: golangci-lint over the whole module
lint:
	$(GOLANGCI_LINT) run --config .golangci.yml --new=false --fix=false ./...

# The compiler's internal/ rule stops type-checking, and golangci-lint then
# reports nothing else, so the package that breaks it is linted on its own.
DEPGUARD_FIXTURES := test/depguard
FIXTURE_LINT = $(GOLANGCI_LINT) run --config $(CURDIR)/.golangci.yml --fix=false --issues-exit-code=0 \
	--show-stats=false --output.text.path=stdout --output.text.colors=false \
	--output.text.print-issued-lines=false --output.sarif.path=/dev/null

## lint-fixtures: prove each depguard rule and the time.Sleep ban fire, on the test/depguard module
lint-fixtures:
	@cd $(DEPGUARD_FIXTURES) && { \
		$(FIXTURE_LINT) $$($(GO) list -e ./... | grep -v '/leak$$' | sed 's|^$(MODULE)|.|'); \
		$(FIXTURE_LINT) ./internal/stacks/leak; \
	} 2>&1 | grep -v '^level=' | sed -E -f normalize.sed | sort | diff -u expected.txt - \
		&& echo "lint-fixtures: every rule fired as expected.txt says"

## vet: go vet
vet:
	$(GO) vet ./...

## test: every TestUnit test (race, shuffle); coverage to cover/unit
test:
	@rm -rf $(COVER_DIR)/unit && mkdir -p $(COVER_DIR)/unit
	$(GO) test -race -shuffle=on -count=1 -run '^TestUnit' -cover -coverpkg=$(MODULE)/... ./... -args -test.gocoverdir=$(CURDIR)/$(COVER_DIR)/unit

## cover: merge every cover/* directory and enforce .testcoverage.yml (>=95% total, >=90% per package)
cover:
	@dirs=$$(find $(COVER_DIR) -mindepth 1 -maxdepth 1 -type d ! -name '.merged' | paste -sd, -); \
	if [ -z "$$dirs" ]; then echo "no coverage data; run make test first"; exit 1; fi; \
	rm -rf $(COVER_DIR)/.merged && mkdir -p $(COVER_DIR)/.merged && \
	$(GO) tool covdata merge -i=$$dirs -o=$(COVER_DIR)/.merged && \
	$(GO) tool covdata textfmt -i=$(COVER_DIR)/.merged -o=$(COVER_DIR)/coverage.out && \
	$(GO) tool covdata percent -i=$(COVER_DIR)/.merged
	$(GO) tool go-test-coverage --config=.testcoverage.yml

## vuln: govulncheck (version pinned in go.mod's tool block, kept current with everything else)
vuln:
	$(GO) tool govulncheck ./...

## build: cross-compile every cmd/* binary for each release platform into bin/ (CGO disabled)
build:
	@mkdir -p $(BIN_DIR)
	@for d in $$(find cmd -mindepth 1 -maxdepth 1 -type d); do \
		name=$${d#cmd/}; \
		for p in $(PLATFORMS); do \
			os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
			echo "$$name $$os/$$arch"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
				-o $(BIN_DIR)/$$name-$$os-$$arch$$ext ./$$d || exit 1; \
		done; \
	done

## accept: every TestAcc test, the godog features and the suites beside the code; coverage to cover/accept
# atomic matches the counter mode -race forces on the unit run; covdata
# refuses to merge profiles whose modes differ.
accept:
	@rm -rf $(COVER_DIR)/accept && mkdir -p $(COVER_DIR)/accept
	$(GO) test -count=1 -run '^TestAcc' -covermode=atomic -cover -coverpkg=$(MODULE)/... ./... -args -test.gocoverdir=$(CURDIR)/$(COVER_DIR)/accept

# PRODUCTS are the Spacelift products with a reference; specref --product
# picks each one's reference directory, inventory outputs and parity
# baseline. Narrow it to run a target for one product (PRODUCTS=flows).
PRODUCTS := deploy flows
# PRODUCT is the one product scaffold copies from.
PRODUCT ?= deploy

## reference-fetch: refresh each product's snapshots and sources.lock.yaml (network; terraform for deploy; pins from the lock unless overridden by SPECREF_*)
reference-fetch:
	@for p in $(PRODUCTS); do $(GO) run ./tools/specref fetch --product $$p || exit 1; done

## scaffold: copy one reference tag into an API fragment and rebundle (make scaffold [PRODUCT=flows] TAG=Stacks FAMILY=stacks [FORCE=1])
scaffold:
	@test -n "$(TAG)" -a -n "$(FAMILY)" || { echo "usage: make scaffold [PRODUCT=<deploy|flows>] TAG=<reference tag> FAMILY=<fragment name>"; exit 1; }
	$(GO) run ./tools/specref scaffold --product $(PRODUCT) --tag "$(TAG)" --out api/src/api/paths-$(FAMILY).yaml $(if $(FORCE),--force)
	$(MAKE) bundle

## bundle: merge api/src/<service>/*.yaml fragments into api/<service>.openapi.yaml
bundle:
	$(GO) run ./tools/openapi-bundle --src api/src --out api

# Run from its own module graph rather than as a go.mod tool: built against
# ours, its dependencies would be upgraded past what it was released with.
# deps-refresh.yml moves it to the latest release.
OAPI_CODEGEN := github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2

# One per api/src/<service>/; each needs api/oapi-{server,client}-<service>.yaml.
SPECS := $(patsubst api/src/%/info.yaml,%,$(wildcard api/src/*/info.yaml))

DATA_VIEW := docs/implementation/tiers.md

## generate: bundle the specs, generate each strict chi server, Go client and 501 stubs, and render the docs/data view
generate: bundle
	@for s in $(SPECS); do \
		echo "generate $$s"; \
		$(GO) run $(OAPI_CODEGEN) -config api/oapi-server-$$s.yaml api/$$s.openapi.yaml || exit 1; \
		$(GO) run $(OAPI_CODEGEN) -config api/oapi-client-$$s.yaml api/$$s.openapi.yaml || exit 1; \
		$(GO) run ./tools/stubgen internal/platform/gen/$$s/server.gen.go api/$$s.openapi.yaml || exit 1; \
	done
	$(GO) run ./tools/dataview docs/data $(DATA_VIEW)

## verify-generate: fail if the committed bundles, generated code or docs/data view differ from what make generate writes
verify-generate: generate
	@git diff --exit-code -- api/*.openapi.yaml internal/platform/gen pkg/client $(DATA_VIEW) || \
		{ echo "generated files are stale; run make generate and commit the result"; exit 1; }

## reference: regenerate each product's OpenAPI reference and coverage report from its snapshots
reference:
	@for p in $(PRODUCTS); do $(GO) run ./tools/specref generate --product $$p || exit 1; done

## reference-inventory: regenerate each product's inventory: the GraphQL register and report 29 (deploy), the SDK register (flows)
reference-inventory:
	@for p in $(PRODUCTS); do $(GO) run ./tools/specref inventory --product $$p || exit 1; done

## verify-reference: fail if a committed reference, report 29 or an inventory register is not what the snapshots generate
verify-reference:
	@for p in $(PRODUCTS); do \
		$(GO) run ./tools/specref check --product $$p && \
		$(GO) run ./tools/specref inventory --check --product $$p || exit 1; \
	done

## parity: print, for each product, every reference operation's outcome (served, served with divergence, superseded, missing) and the infraweave-only ones by tag, and how they differ from api/parity-baseline.<product>.json
parity:
	@for p in $(PRODUCTS); do $(GO) run ./tools/specref parity --product $$p --list || exit 1; done

## verify-parity: fail when a served operation diverges unrecorded or is superseded, a baseline is stale, or capabilities.json claims a state cover/accept/cucumber.json does not show
verify-parity:
	@for p in $(PRODUCTS); do $(GO) run ./tools/specref parity --product $$p --states --check || exit 1; done

## parity-baseline: rewrite each api/parity-baseline.<product>.json to accept the current parity report, keeping recorded reasons (review the diff)
parity-baseline:
	@for p in $(PRODUCTS); do $(GO) run ./tools/specref parity --product $$p --write-baseline || exit 1; done

## docs-check: links, report headers and decision statuses under docs/ (internal/devtools/docscheck)
docs-check:
	$(GO) test -count=1 -run '^TestUnitDocs$$' ./internal/devtools/docscheck

## gate: everything CI runs, in order
gate: docs-check vet lint lint-fixtures test accept verify-parity verify-reference verify-generate cover vuln build

.PHONY: help init docs-check fmt lint lint-fixtures vet test accept reference-fetch scaffold bundle generate verify-generate reference reference-inventory verify-reference parity verify-parity parity-baseline cover vuln build gate
