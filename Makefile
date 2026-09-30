# Self-contained Makefile for github.com/codeactual/evolve.
#
# It includes nothing from any superproject, so it behaves identically inside a
# checkout that embeds it and in a standalone clone. The warnings gate, git
# version stamp, pinned static analyzers and the supply-chain scan targets
# replicate the monorepo's make/common.mk, make/go.mk and security_scan
# infrastructure; each analyzer is invoked through `go run` at a pinned version,
# so nothing has to be installed globally.
#
# security_scan is deliberately NOT a prerequisite of `ci` or any other target:
# run it explicitly wherever osv-scanner and trivy are installed.

# Treat undefined variables as errors (from the monorepo make/common.mk).
MAKEFLAGS += --warn-undefined-variables
ifndef MAKECMDGOALS
MAKECMDGOALS = all
endif
SHELL = bash

# Fails if `make -n <goals>` emits any warning (e.g. an undefined variable).
# An ordinary recipe does not receive its parent's jobserver descriptors, so
# remove only those unusable tokens from this target's nested dry-run.
no-make-warnings: MAKEFLAGS := $(filter-out -j% --jobserver-auth=%,$(MAKEFLAGS))
no-make-warnings:
	@! make -n $(MAKECMDGOALS) 2>&1 >/dev/null | grep warning

# The root module and the separate e2e module (the root `./...` never picks it
# up). Analyzers that take package patterns run once per module.
go_modules := . e2e

# Run $(1) from each module directory, stopping at the first failure.
define for-each-module
@set -e; for m in $(go_modules); do (cd "$$m" && $(1)); done
endef

# --- Version stamp (make/common.mk git helpers, make/go.mk go-version-ldflags) -
version_pkg := github.com/codeactual/evolve/internal/version
git-ref-sha   = $(shell git rev-parse --short HEAD)
git-ref-label = $(shell git symbolic-ref -q --short HEAD || git describe --tags --exact-match)
git-dirty     = $(shell git rev-parse --is-inside-work-tree >/dev/null 2>&1 && git status --porcelain --untracked-files=all | grep -q . && echo -dirty)

# Build into ./builds/evolve, stamping <go version>-<sha>-<label>[-dirty] into
# the version package, then assert the binary reports it. evolve exposes its
# version only through the `version` subcommand. `-X pkg.Sym=val` naming a
# symbol absent from the final link is a silent no-op, so an unstamped binary
# must fail the build rather than pass it quietly.
.PHONY: build
build:
	$(eval version-stamp := $(word 3, $(strip $(shell go version)))-$(git-ref-sha)-$(git-ref-label)$(git-dirty))
	@mkdir -p builds
	go build -trimpath -ldflags "-X $(version_pkg).Version=$(version-stamp)" -o builds/evolve ./cmd/evolve
	@printed="$$(./builds/evolve version | head -1)"; \
		echo "$$printed"; \
		case "$$printed" in \
			*"$(version-stamp)"*) ;; \
			*) echo "error: builds/evolve version did not report the injected stamp '$(version-stamp)': got '$$printed'" >&2; exit 1 ;; \
		esac

.PHONY: test
test:
	CGO_ENABLED=1 go test -v -race ./...

.PHONY: vet
vet:
	go vet ./...
	go -C e2e vet ./...

# --- Formatting (goimports, then gofumpt) -------------------------------------
# goimports is invoked through `go run`, so neither module needs a `tool`
# directive. imports-check and go-gofumpt-check never write; fmt does.
goimports = GOWORK=off go run golang.org/x/tools/cmd/goimports@v0.49.0
gofumpt   = GOWORK=off go run mvdan.cc/gofumpt@v0.10.0

.PHONY: fmt
fmt:
	$(goimports) -w .
	$(gofumpt) -w .

.PHONY: imports-check
imports-check: no-make-warnings
	@files="$$($(goimports) -l .)" && \
		test -z "$$files" || (echo "goimports needed on:"; printf '%s\n' "$$files"; exit 1)

.PHONY: go-gofumpt-check
go-gofumpt-check: no-make-warnings
	@files="$$($(gofumpt) -l .)" && \
		test -z "$$files" || (echo "gofumpt needed on:"; printf '%s\n' "$$files"; exit 1)

.PHONY: tidy
tidy:
	go mod tidy
	go -C e2e mod tidy

# --- Static analysis (the same analyzers and pins as the monorepo's go.mk) ----
# gocyclo complexity of a function = 1 + (each `if` / `for` / `case` / `&&` /
# `||`). Blocking gate; exits non-zero if any function exceeds cyclo_max.
cyclo_max    ?= 40
cyclo_ignore ?= third_party|_test\.go
gocyclo       = GOWORK=off go run github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0

.PHONY: go-cyclo-gate
go-cyclo-gate: no-make-warnings
	@$(gocyclo) -over $(cyclo_max) -ignore '$(cyclo_ignore)' .

# ineffassign reports assignments whose values are overwritten or discarded
# before they are used.
ineffassign = GOWORK=off go run github.com/gordonklaus/ineffassign@v0.2.0

.PHONY: go-ineffassign
go-ineffassign: no-make-warnings
	$(call for-each-module,$(ineffassign) ./...)

# errcheck exits 1 for findings and 2 for operational failures.
errcheck = go run github.com/kisielk/errcheck@v1.20.0

.PHONY: go-errcheck
go-errcheck: no-make-warnings
	$(call for-each-module,$(errcheck) ./...)

# The public release tag 2026.1 resolves to Go module version v0.7.0.
staticcheck = go run honnef.co/go/tools/cmd/staticcheck@2026.1

.PHONY: go-staticcheck
go-staticcheck: no-make-warnings
	$(call for-each-module,$(staticcheck) ./...)

# Revive's built-in default rules. Isolate HOME and XDG_CONFIG_HOME so a
# developer's global revive.toml cannot change the CI contract. Preserve the Go
# caches explicitly because the go command otherwise derives them from HOME.
revive = HOME= XDG_CONFIG_HOME=/nonexistent \
	GOCACHE=$(shell go env GOCACHE) \
	GOMODCACHE=$(shell go env GOMODCACHE) \
	GOPATH=$(shell go env GOPATH) \
	go run github.com/mgechev/revive@v1.15.0

.PHONY: go-revive
go-revive: no-make-warnings
	$(call for-each-module,$(revive) -set_exit_status ./...)

# `go fix -diff` prints the modernizations it would make and exits non-zero when
# the diff is non-empty. It never writes: apply fixes with a bare `go fix ./...`.
.PHONY: go-fix-check
go-fix-check: no-make-warnings
	$(call for-each-module,go fix -diff ./...)

# The real end-to-end test in e2e/: `evolve run all` on the marketplace fixture.
# Needs the claude CLI and its credentials. See e2e/smoke_test.go.
.PHONY: smoke
smoke:
	@command -v claude >/dev/null 2>&1 || { echo "smoke: claude CLI not found in PATH" >&2; exit 2; }
	SMOKE_MODEL="$${SMOKE_MODEL:-claude-haiku-4-5}" go -C e2e test -v -count=1 -run '^TestSmoke$$' .

.PHONY: ci
ci: no-make-warnings vet imports-check go-gofumpt-check test go-cyclo-gate go-ineffassign go-errcheck go-staticcheck go-revive go-fix-check build

# --- Supply-chain scans (replicated from the monorepo's Makefile + scan_gate.sh) -
# Not a prerequisite of `ci` or any other target. Each scanner's exit status is
# preserved. On a non-zero status a best-effort `d0log send` alert fires, only
# when d0log is on PATH; a failing send never changes the status. Exit code 1
# (osv-scanner) and 7 (trivy) denote findings; anything else non-zero is an
# operational failure.
scan_domain ?= go_evolve

# $(call scan-gate,<sender>,<findings-exit-code>): reads the scanner's `$$status`.
define scan-gate
if [ "$$status" -ne 0 ] && command -v d0log >/dev/null 2>&1; then \
	if [ "$$status" -eq $(2) ]; then \
		d0log send "$(1)" "$(scan_domain),vulns" 'vulnerabilities found, remediation needed -- re-run the scan for the report' || true; \
	else \
		d0log send "$(1)" "$(scan_domain),scan_error" "SCAN FAILED (exit $$status) -- coverage incomplete, investigate the scanner" || true; \
	fi; \
fi; \
exit $$status
endef

# TRIVY_CONFIG pins trivy's offline config (baked DB, vuln scanner only, no
# telemetry). When that file is absent, never let an inherited value reach trivy:
# it runs with its built-in defaults instead.
TRIVY_CONFIG ?= /opt/trivy/trivy.yaml
ifneq ($(wildcard $(TRIVY_CONFIG)),)
export TRIVY_CONFIG
else
unexport TRIVY_CONFIG
endif

.PHONY: osv_scan
osv_scan: no-make-warnings
	@osv-scanner scan source --offline-vulnerabilities --download-offline-databases -r $(CURDIR); status=$$?; $(call scan-gate,osv_scan,1)

.PHONY: trivy_scan
trivy_scan: no-make-warnings
	@trivy fs --exit-code 7 $(CURDIR); status=$$?; $(call scan-gate,trivy_scan,7)

# Combined supply-chain scan: osv-scanner + trivy.
.PHONY: security_scan
security_scan: osv_scan trivy_scan
