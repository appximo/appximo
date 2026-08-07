# Recipes use bash (not the default /bin/sh → dash) so `set -o pipefail` works in
# the gotestfmt-piped targets.
SHELL := bash

.PHONY: build engine worker lint fmt-check run build-pgo collect-profile \
	test test-integration test-e2e test-resilience test-perf test-security test-all bench \
	dev dev-fast dev-serve build-cli spec install stop help \
	fleet fleet-fast fleet-init fleet-serve

build: ## Compile every package (go build ./...)
	go build ./...

# engine: the CANONICAL engine binary build (static, version-stamped from git).
# Same script the Dockerfile, release.yml and the devhub deploy pipeline use —
# never hand-write `go build` for a deployable engine binary.
engine: ## Canonical engine binary build (static, version-stamped — scripts/build-engine.sh)
	./scripts/build-engine.sh appximo

# worker: the CANONICAL worker binary build (cmd/appximo-worker, ADR-016
# §Class 2). Same static/version-stamped flags as `engine`; ships in the same
# Docker image (run it with the `worker` entrypoint keyword).
worker: ## Canonical worker binary build (cmd/appximo-worker)
	./scripts/build-worker.sh appximo-worker

lint: ## golangci-lint run ./...
	golangci-lint run ./...

# fmt-check: the formatting gate CI enforces (run it locally before committing).
# Fails listing every file gofmt would rewrite; empty output = clean tree.
# node_modules is excluded defensively (the UI trees ship no .go, but a dep might).
fmt-check: ## The gofmt gate CI enforces
	@out=$$(gofmt -l . 2>&1 | grep -v node_modules || true); \
	if [ -n "$$out" ]; then \
		echo "✗ gofmt gate — fix these (gofmt -w):"; echo "$$out"; exit 1; \
	fi; \
	echo "✓ gofmt clean"

run:
	go run ./cmd/appximo/main.go

# ─────────────────────────────────────────────────────────────────────────────
# Daily flow (DX-S1) — one command per task. `make help` lists everything.
# ─────────────────────────────────────────────────────────────────────────────

# The dev server's inputs, all overridable per invocation:
#   make dev                              # blank schema, port 8080
#   make dev SCHEMA=optica.json PORT=9000 # your schema, your port
# DEV_ENV is the env-file with DATABASE_URL / JWT_SECRET / ADMIN_KEY for the
# LAUNCHED PROCESS (make can't export into your shell; it loads it for serve).
DEV_ENV ?= .env.dev
SCHEMA  ?= examples/blank/schema.json
PORT    ?= 8080
PREFIX  ?= /usr/local

build-cli: ## Compile the dev CLI/engine binary to ./appximo-dev (no serve)
	go build -o appximo-dev ./cmd/appximo

# dev: correctness over speed — it rebuilds the editor SPA so /editor can never
# silently serve stale assets. If you haven't touched pkg/editorui/web, use
# dev-fast. The boot schema is a BLANK app (examples/blank) by design: opening
# Studio must not impose a demo ERP — load/paste your real schema (Code view)
# or pass SCHEMA=.
dev: editor-ui build-cli dev-serve ## Build editor+engine, load dev secrets, serve (SCHEMA=…, PORT=…)

dev-fast: build-cli dev-serve ## Like dev but skips the editor SPA rebuild (assumes it's built)

# dev-serve: the shared serve step (not meant to be called directly). Loads
# DEV_ENV into the child process only — your shell is untouched.
dev-serve:
	@test -f "$(DEV_ENV)" || { \
		echo "✗ dev env file $(DEV_ENV) not found."; \
		echo "  Create it with the three required vars (see .env.example):"; \
		echo "    DATABASE_URL=postgres://user:pass@localhost:5432/db"; \
		echo "    JWT_SECRET=<at least 32 characters>"; \
		echo "    ADMIN_KEY=<admin key>"; \
		echo "  Or point elsewhere: make dev DEV_ENV=path/to/env"; exit 1; }
	@test -f "$(SCHEMA)" || { echo "✗ schema $(SCHEMA) not found — pass SCHEMA=path/to/schema.json"; exit 1; }
	@echo "→ serving $(SCHEMA) on :$(PORT)"
	@echo "  Studio  http://localhost:$(PORT)/editor    admin  http://localhost:$(PORT)/admin"
	@echo "  docs    http://localhost:$(PORT)/docs      stop with Ctrl+C (or: make stop PORT=$(PORT))"
	@set -a; . "$(DEV_ENV)"; set +a; exec ./appximo-dev serve --schema "$(SCHEMA)" --port $(PORT)

# ── Fleet (FLEET-CONSOLE-S2): N apps, one command — the `make dev` of fleets ──
# make fleet-init   → scaffold fleet.json + fleet-secrets/ (gitignored, generated
#                     secrets — nothing pasted by hand) + a starter schema + DBs
# make fleet        → build UIs+engine, load the fleet secrets, serve everything
# make fleet PORT=9000 / CONFIG=other.json — parameterized like make dev.
FLEET_CONFIG ?= fleet.json
FLEET_ENV    ?= fleet-secrets/fleet.env

fleet: editor-ui admin-ui build-cli fleet-serve ## Build UIs+engine, load fleet secrets, serve all apps (CONFIG=…, PORT=…)

fleet-fast: build-cli fleet-serve ## Like fleet but skips the SPA rebuilds (assumes they're built)

fleet-init: build-cli ## Scaffold a working fleet: manifest + secrets (gitignored) + starter schema + DBs
	@set -a; test -f "$(DEV_ENV)" && . "$(DEV_ENV)"; set +a; \
		./appximo-dev fleet init --config "$(FLEET_CONFIG)"

# fleet-serve: the shared serve step (not meant to be called directly). Loads
# the fleet-level env file (operator key + operator admin password) into the
# CHILD process only; per-app secrets ride in each app's env_file.
fleet-serve:
	@test -f "$(FLEET_CONFIG)" || { \
		echo "✗ $(FLEET_CONFIG) not found."; \
		echo "  Scaffold a working fleet (manifest + secrets + starter schema + DBs) with:"; \
		echo "    make fleet-init"; \
		echo "  Or point elsewhere: make fleet FLEET_CONFIG=path/to/fleet.json"; exit 1; }
	@echo "→ fleet serving $(FLEET_CONFIG)$(if $(PORT), on :$(PORT),)"
	@echo "  console  http://localhost:$(or $(PORT),8080)/fleet?key=…  (operator key: $(FLEET_ENV))"
	@echo "  apps     http://<app>.localhost:$(or $(PORT),8080)/editor /admin /docs   stop: make stop PORT=$(or $(PORT),8080)"
	@set -a; test -f "$(FLEET_ENV)" && . "$(FLEET_ENV)"; set +a; \
		exec ./appximo-dev fleet serve --config "$(FLEET_CONFIG)" $(if $(PORT),--listen ":$(PORT)",)

spec: build-cli ## Regenerate appximo-spec.md (the LLM grammar pack for your agent)
	@./appximo-dev spec > appximo-spec.md
	@echo "✓ appximo-spec.md regenerated ($$(wc -l < appximo-spec.md) lines)"
	@echo "  Paste it into your agent's context (Claude Code / Cursor) — the flow: docs/SCHEMA_SPEC_LLM.md"

install: ## Install the appximo CLI into PREFIX/bin (default /usr/local/bin; may need sudo)
	@test -w "$(PREFIX)/bin" || { echo "✗ $(PREFIX)/bin is not writable — run: sudo make install   (or: make install PREFIX=$$HOME/.local)"; exit 1; }
	./scripts/build-engine.sh "$(PREFIX)/bin/appximo"
	@echo "✓ installed $$($(PREFIX)/bin/appximo version 2>/dev/null | tail -1) at $(PREFIX)/bin/appximo"
	@echo "  Now 'appximo spec', 'appximo serve', 'appximo validate --json …' work from any directory."

stop: ## Stop the dev server on PORT (default 8080) by its exact PID — never pkill
	@pid=$$(ss -ltnp 2>/dev/null | grep ":$(PORT) " | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2); \
	if [ -z "$$pid" ]; then \
		echo "nothing listening on :$(PORT)"; \
	else \
		echo "stopping PID $$pid (port :$(PORT)) — graceful drain"; kill $$pid; \
	fi

help: ## List the annotated targets
	@echo "Appximo — make targets (daily flow first; see the Makefile for the full test/bench lanes):"
	@grep -hE '^[a-zA-Z][a-zA-Z0-9_-]*:.*## ' $(MAKEFILE_LIST) | \
		awk -F':.*## ' '{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ─────────────────────────────────────────────────────────────────────────────
# Test lanes (context-docs/TESTING_PLAN.md)
# ─────────────────────────────────────────────────────────────────────────────

# test: fast unit lane — race detector + -short. No Docker needed: the S37
# tests/ suites are build-tagged (integration / e2e) and the legacy
# testcontainers suites carry testing.Short() guards (S40).
test: ## Fast unit lane (-race -short, no Docker, ~7 s warm)
	go test ./... -race -short

# test-integration: DB-backed integration suite (real Postgres via testcontainers).
test-integration:
	go test -tags integration -race -count=1 ./tests/integration/...

# test-e2e: full client scenarios (S38+). Pretty-prints with gotestfmt when present
# (which needs `-json` input), otherwise plain `go test -v`. pipefail keeps a test
# failure fatal through the gotestfmt pipe.
test-e2e:
	@if command -v gotestfmt >/dev/null 2>&1; then \
		set -o pipefail; go test ./tests/e2e/... -race -count=1 -tags e2e -timeout 300s -json 2>&1 | gotestfmt; \
	else \
		go test ./tests/e2e/... -race -count=1 -tags e2e -timeout 300s -v; \
	fi

# test-resilience: chaos/resilience suite (toxiproxy latency → circuit breaker,
# graceful shutdown under load). Real Postgres via testcontainers (needs Docker).
test-resilience:
	go test ./tests/resilience/... -race -count=1 -tags resilience -timeout 120s -v

# test-perf: k6 SLO gate. Exits 99 if p95>=15ms or error_rate>=1%.
# Requires a running server + BENCH_TOKEN (mint with `appximo token`).
# Override RATE/DURATION/TARGET_URL/TENANT_ID via env. See tests/performance/README.md.
test-perf:
	k6 run tests/performance/sustained_2krps.js

# test-security: DAST (nuclei + ZAP) — manual/nightly, wired in S40.
test-security:
	@echo "test-security: nuclei + ZAP DAST is scheduled for S40 (nightly), not yet wired."
	@echo "See context-docs/TESTING_PLAN.md and tests/security/."

# test-all: the turnkey, Docker-only suites that back every claim in the README /
# Show HN post — unit + integration + E2E scenarios + resilience (toxiproxy circuit
# breaker, graceful shutdown). test-perf is intentionally NOT included: the k6 SLO
# gate needs a separately-running server + token, so it is a standalone target.
test-all: test test-integration test-e2e test-resilience ## Unit + integration + E2E + resilience (needs Docker)

# bench: Go benchmarks. For A/B regression, save two runs and compare with benchstat:
#   go test -bench=. -benchmem -run='^$$' ./... | tee new.txt
#   benchstat old.txt new.txt
bench:
	go test -bench=. -benchmem -run='^$$' ./...

# ─────────────────────────────────────────────────────────────────────────────
# PGO
# ─────────────────────────────────────────────────────────────────────────────

# collect-profile: sample 30 s of CPU from the running dev server (port 6060)
# and save it as default.pgo. Run this while the server is under load.
collect-profile:
	@echo "Collecting 30s CPU profile from dev server (APPXIMO_ENV=development)..."
	curl -sf "http://localhost:6060/pprof/profile?seconds=30" -o default.pgo
	@echo "Profile saved to default.pgo — now run: make build-pgo"

# build-pgo: compile with the profile collected by collect-profile.
# Requires default.pgo to exist (run collect-profile first).
# -trimpath strips local paths; -ldflags="-s -w" drops the symbol table + DWARF
# (~30% smaller binary). The runtime pclntab is kept, so runtime.CallersFrames —
# and therefore the error trace explorer's symbolized stacks — still work.
build-pgo: default.pgo
	go build -pgo=default.pgo -trimpath -ldflags="-s -w" -o appximo ./cmd/appximo/
	@echo "PGO binary written to ./appximo"

# ─── DEV TOOLS (S41) ─────────────────────────────────────────
.PHONY: test-watch test-watch-pkg test-watch-integration test-reflex \
        cover cover-treemap test-perf-dashboard test-perf-smoke \
        dev-layout dev-metrics dev-setup devhub-run devhub-build admin-ui editor-ui bench-protocol

test-watch:
	gotestsum --watch --format testdox --watch-chdir -- -race -short ./...

test-watch-pkg:
	@test -n "$(PKG)" || (echo "Uso: make test-watch-pkg PKG=./pkg/query" && exit 1)
	gotestsum --watch --format testdox --watch-chdir -- -race -short $(PKG)

test-watch-integration:
	gotestsum --watch --format testdox -- -race -tags integration -timeout 120s ./tests/integration/...

test-reflex:
	reflex -r '\.go$$' -s -- sh -c 'go test -race -short -count=1 ./...'

cover:
	go test -coverprofile=coverage.out -coverpkg=./... ./... -short -count=1 2>/dev/null || true
	go tool cover -html=coverage.out -o coverage.html
	@echo "→ tunnel: ssh -L 8090:localhost:8090 <your-server>"
	python3 -m http.server 8090

cover-treemap:
	go test -coverprofile=coverage.out ./... -short -count=1 2>/dev/null || true
	go-cover-treemap -coverprofile coverage.out > coverage.svg

test-perf-dashboard:
	K6_WEB_DASHBOARD=true \
	K6_WEB_DASHBOARD_EXPORT=perf-report-$(shell date +%Y%m%d-%H%M%S).html \
	k6 run tests/performance/sustained_2krps.js

test-perf-smoke:
	k6 run --duration 30s tests/performance/sustained_2krps.js

dev-layout:
	@which tmux >/dev/null 2>&1 || (echo "apt install tmux -y" && exit 1)
	bash scripts/dev-tmux.sh

dev-metrics: prometheus.dev.yml
	@which prometheus >/dev/null 2>&1 || \
	  (curl -sL https://github.com/prometheus/prometheus/releases/download/v3.4.0/prometheus-3.4.0.linux-amd64.tar.gz \
	   | tar xz --strip-components=1 -C /tmp prometheus-3.4.0.linux-amd64/prometheus && \
	   mv /tmp/prometheus /usr/local/bin/prometheus)
	prometheus --config.file=prometheus.dev.yml \
	  --storage.tsdb.retention.time=1h \
	  --storage.tsdb.path=/tmp/promdata-dev \
	  --web.listen-address=:9090

dev-setup:
	go install gotest.tools/gotestsum@latest
	go install github.com/cespare/reflex@latest
	go install github.com/nikolaydubina/go-cover-treemap@latest
	apt-get install -y tmux 2>/dev/null || true
	echo "fs.inotify.max_user_watches=524288" | tee /etc/sysctl.d/99-inotify.conf
	sysctl -p /etc/sysctl.d/99-inotify.conf

# El devhub "de verdad" corre como servicio systemd (tools/devhub/devhub.service
# → systemctl {status,restart} devhub; binario /root/appximo/devhub vía
# devhub-build). Este target queda SOLO para desarrollar el devhub mismo
# (-tags dev: sin UI embebida, Vite aparte) — pará el servicio antes o el
# puerto :3099 va a estar tomado.
devhub-run:
	APPXIMO_DIR=$(shell pwd) go run -tags dev ./tools/devhub/

devhub-build:
	cd tools/devhub/web && npm run build
	go build -trimpath -ldflags="-s -w" -o devhub ./tools/devhub/

# admin-ui: build the embedded SolidJS admin panel (ADMIN-UI-V1). Since ADR-025
# the BUILT dist is COMMITTED (the module ships working UIs; a bare `go build`
# includes them) — run this after touching pkg/adminui/web/src and commit the
# rebuilt assets WITH the src change. Release still rebuilds for freshness.
admin-ui: ## Build the embedded admin panel SPA (after touching its src; commit the dist)
	cd pkg/adminui/web && npm install --no-audit --no-fund && npm run build

# editor-ui: build the embedded visual schema editor SPA (Appximo Studio, UI-F0-S1).
# Plain Vite + Svelte 5 → static files in pkg/editorui/web/build. Same ADR-025
# pattern as admin-ui: the built assets are COMMITTED — rebuild after touching
# web/src and commit the new assets with the src change.
editor-ui: ## Build the embedded Studio SPA (after touching its src; commit the build)
	cd pkg/editorui/web && npm install --no-audit --no-fund && npm run test && npm run build

# Protocolo de benchmark científico: N runs + warmup + cooldown + import a SQLite
# Línea base canónica (OPS-9): make dev-fast (examples/blank) + tenant acme +
# RATE=100 (el 105 compartido no es confiable por encima de ~100) — el endpoint
# se deriva solo de la superficie servida. Ver el header de bench-protocol.sh.
# Uso: make bench-protocol RUNS=10 LABEL=mi-cambio [RATE=100] [DURATION=30s] [SCRIPT=…]
bench-protocol:
	@test -n "$(LABEL)" || (echo "Uso: make bench-protocol RUNS=10 LABEL=nombre" && exit 1)
	bash scripts/bench-protocol.sh $(or $(RUNS),10) $(LABEL) $(or $(RATE),100) $(or $(DURATION),30s) $(or $(SCRIPT),tests/performance/sustained_2krps.js)
