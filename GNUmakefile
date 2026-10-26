default: build

BINARY_NAME=terraform-provider-trino
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
# Trino server version the acceptance tests run against; docker-compose.yml reads
# the same variable. Override on the command line to test another version.
TRINO_VERSION ?= 484
# Compose implementation. Override for Podman:
#   make testacc-docker COMPOSE=podman-compose
COMPOSE ?= docker compose
# Supported versions, single-sourced with the CI matrix. Override for a quick
# local check: TRINO_VERSIONS="484 483" make testacc-matrix
TRINO_VERSIONS ?= $(shell tr '\n' ' ' < .github/trino-versions.txt)

build:
	go build -o bin/$(BINARY_NAME) -ldflags "-X main.version=$(VERSION)" .

test:
	go test -v -cover ./internal/...

# Unit coverage only: `TestAcc*` needs TF_ACC and a reachable Trino coordinator,
# so it is skipped to keep the profile independent of the environment.
coverage:
	go test -count=1 -skip '^TestAcc' -covermode=atomic -coverprofile=coverage.out ./internal/...
	@echo
	@./scripts/coverage-summary.sh coverage.out

coverage-html: coverage
	go tool cover -html=coverage.out

# Unit coverage (coverage.out) plus the acceptance profile written by
# docker-compose.yml (coverage-acceptance.out) as one number.
coverage-merge:
	./scripts/merge-coverage.sh coverage-union.out coverage.out coverage-acceptance.out
	@echo
	@./scripts/coverage-summary.sh coverage-union.out

# Regenerate the coverage badge and floor statement in README.md from
# .github/coverage-baseline.txt; `make coverage-readme-check` fails when the
# committed README has drifted from the baseline.
coverage-readme:
	./scripts/coverage-readme.sh

coverage-readme-check:
	./scripts/coverage-readme.sh --check

# Fail when coverage.out (unit + protocol) is below the floor in
# .github/coverage-baseline.txt. Acceptance coverage is reported separately and
# is not gated: it depends on Docker being available (design §10).
coverage-gate:
	./scripts/coverage-gate.sh coverage.out

testacc:
	TF_ACC=1 go test -v -cover ./internal/...

testacc-docker:
	TRINO_VERSION=$(TRINO_VERSION) $(COMPOSE) run --rm test

# Fuzz the untrusted-input parsers for a short, fixed time each. Fuzzing can
# only target one package and one target per invocation. CI runs the committed
# seed corpora (go test ./internal/... -run '^Fuzz'), which is bounded; this
# target explores new inputs. Override FUZZTIME for a longer run.
FUZZTIME ?= 10s
fuzz:
	go test ./internal/trino -fuzz '^FuzzQuoteIdentifier$$' -fuzztime=$(FUZZTIME)
	go test ./internal/trino -fuzz '^FuzzQuoteLiteral$$' -fuzztime=$(FUZZTIME)
	go test ./internal/resources/schema -fuzz '^FuzzParseShowCreateSchema$$' -fuzztime=$(FUZZTIME)

# Run the docker-based acceptance suite against every supported Trino version.
# Each version runs and tears down independently so one failure still lets the
# others run; the overall exit status is non-zero if any version failed.
testacc-matrix:
	@test -n "$(TRINO_VERSIONS)" || { \
		echo "TRINO_VERSIONS is empty; check .github/trino-versions.txt" >&2; \
		exit 1; \
	}
	@results=$$(mktemp); status=0; \
	for v in $(TRINO_VERSIONS); do \
		echo "==> Acceptance tests against Trino $$v"; \
		if COMPOSE_PROJECT_NAME=trino-acc-$$v TRINO_EXPECT_VERSION=$$v \
			$(MAKE) --no-print-directory testacc-docker TRINO_VERSION=$$v; then \
			echo "PASS Trino $$v" >> "$$results"; \
		else \
			echo "FAIL Trino $$v" >> "$$results"; status=1; \
		fi; \
		COMPOSE_PROJECT_NAME=trino-acc-$$v $(COMPOSE) down -v >/dev/null 2>&1 || true; \
	done; \
	echo; echo "==> Acceptance matrix summary"; cat "$$results"; rm -f "$$results"; \
	exit $$status

lint:
	golangci-lint run --config .golangci.yml ./...

lint-fix:
	golangci-lint run --config .golangci.yml --fix ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/caberdo/trino/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)
	cp bin/$(BINARY_NAME) ~/.terraform.d/plugins/registry.terraform.io/caberdo/trino/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)/$(BINARY_NAME)

validate-examples:
	./scripts/validate-examples.sh

generate:
	go generate ./...

# Fail when the release metadata a release pull request carries is inconsistent.
# The tag workflow enforces the same agreement on main; this keeps the failure
# in the pull request instead of after the merge.
release-check:
	./scripts/release-check.sh

.PHONY: default build test coverage coverage-html coverage-merge coverage-readme coverage-readme-check coverage-gate testacc testacc-docker testacc-matrix fuzz lint lint-fix fmt vet install validate-examples generate release-check
